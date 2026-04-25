package transcode

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	cid "github.com/nspcc-dev/neofs-sdk-go/container/id"
	oid "github.com/nspcc-dev/neofs-sdk-go/object/id"

	"nstream/internal/db"
	"nstream/internal/neofs"
	"nstream/internal/probe"
)

// session holds state for one on-the-fly HLS transcode session.
// It is keyed by videoID so multiple browser tabs / requests for the same
// video share the same ffmpeg process instead of spawning duplicates.
type session struct {
	id       string             // UUID used in playlist / segment URLs
	dir      string             // temp directory holding .ts and .m3u8 files
	cancel   context.CancelFunc // kills the ffmpeg process
	done     chan struct{}      // closed when ffmpeg exits
	// lastUsed is driven by explicit keepalive heartbeats only (play state).
	// We intentionally do NOT refresh it on manifest/segment GET requests
	// because hls.js may continue polling those in background states.
	lastUsed time.Time
	mu       sync.Mutex
	video    db.Video
	// startSegment is the first segment index produced by the current ffmpeg
	// run. It is 0 for linear playback and jumps forward on seek-based restart.
	startSegment int
	running      bool
	lastJumpAt   time.Time
	jumpInProgress bool
	pendingJump    int
	pendingSince   time.Time

	// vodManifest stores the video's total duration as a decimal string
	// and full segment index for stream.m3u8 so the player can seek beyond the
	// currently transcoded edge. Empty when duration is unknown.
	vodManifest string
}

// Manager coordinates on-the-fly HLS transcode sessions.
type Manager struct {
	db         *db.DB
	nfs        *neofs.Client
	ffmpeg     string
	ffprobe    string
	streamBase string // e.g. "http://localhost:8080" — used to build probe URLs
	tempDir    string
	log        *slog.Logger
	caps       Capabilities // detected once at startup

	mu       sync.Mutex
	sessions map[int64]*session // key: videoID
}

// NewManager creates a Manager.
// streamBase is the base URL of the running HTTP server used to probe video
// streams (e.g. "http://localhost:8080").  Pass "" to disable probing (falls
// back to software transcoding).
func NewManager(database *db.DB, nfs *neofs.Client, ffmpeg, ffprobe, streamBase, tempDir string, log *slog.Logger) *Manager {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	m := &Manager{
		db:         database,
		nfs:        nfs,
		ffmpeg:     ffmpeg,
		ffprobe:    ffprobe,
		streamBase: streamBase,
		tempDir:    tempDir,
		log:        log,
		caps:       DetectCapabilities(ffmpeg, log),
		sessions:   make(map[int64]*session),
	}
	go m.cleanupLoop()
	return m
}

// ServeMasterPlaylist handles GET /hls/{videoID}/master.m3u8.
// If a live session already exists for this video it is reused.
func (m *Manager) ServeMasterPlaylist(w http.ResponseWriter, r *http.Request, videoID int64) {
	ctx := r.Context()
	v, err := m.db.GetVideoByID(ctx, videoID)
	if err != nil || v == nil {
		http.Error(w, "video not found", http.StatusNotFound)
		return
	}

	sessID, err := m.getOrStartSession(ctx, videoID, *v)
	if err != nil {
		m.log.Error("hls: start session", "err", err, "video", videoID)
		http.Error(w, "transcode error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-STREAM-INF:BANDWIDTH=2000000\n/hls/%d/%s/stream.m3u8\n", videoID, sessID)
}

// ServeSegment handles GET /hls/{videoID}/{sessionID}/{file}.
func (m *Manager) ServeSegment(w http.ResponseWriter, r *http.Request, videoID int64, sessID, file string) {
	m.mu.Lock()
	sess, ok := m.sessions[videoID]
	m.mu.Unlock()

	if !ok || sess.id != sessID {
		http.Error(w, "session not found or expired", http.StatusNotFound)
		return
	}

	sess.mu.Lock()
	vod := sess.vodManifest
	sess.mu.Unlock()

	if strings.HasSuffix(file, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		// Playlist activity means the player is still attached to this session.
		sess.mu.Lock()
		sess.lastUsed = time.Now()
		sess.mu.Unlock()
		// If the session has a known total duration, send it as a header so
		// the frontend JavaScript can set mediaSource.duration without needing
		// to parse or modify the HLS manifest.
		if vod != "" && filepath.Base(file) == "stream.m3u8" {
			w.Write([]byte(vod)) //nolint:errcheck
			return
		}
		// Always serve the live ffmpeg-written playlist so that the segment
		// list stays correct regardless of transcode mode (copy vs re-encode).
		// The frontend is responsible for showing the correct seekbar length.
		fpath := filepath.Join(sess.dir, filepath.Base(file))
		// Wait briefly for the first manifest to appear (ffmpeg writes it as
		// soon as the first segment is complete).
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(fpath); err == nil {
				break
			}
			select {
			case <-time.After(200 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		if _, err := os.Stat(fpath); err != nil {
			http.Error(w, "playlist not ready", http.StatusServiceUnavailable)
			return
		}
		http.ServeFile(w, r, fpath)
		return
	}

	// For segment files (.ts), wait up to 30 s for ffmpeg to produce them.
	// This is what makes seeking "work" for positions not yet transcoded:
	// the player requests the segment and we wait for ffmpeg to catch up.
	// Segment traffic is the strongest signal of active playback: refresh idle.
	sess.mu.Lock()
	sess.lastUsed = time.Now()
	sess.mu.Unlock()
	fpath := filepath.Join(sess.dir, filepath.Base(file))
	segIdx := parseSegmentIndex(file)
	seekTriggered := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(fpath); err == nil {
			if segIdx >= 0 {
				sess.mu.Lock()
				if segIdx >= sess.startSegment {
					sess.jumpInProgress = false
				}
				sess.mu.Unlock()
			}
			break
		}
		if !seekTriggered && segIdx >= 0 {
			seekTriggered = m.maybeSeekSession(sess, segIdx)
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
	}
	if _, err := os.Stat(fpath); err != nil {
		http.Error(w, "segment not ready", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "video/MP2T")
	http.ServeFile(w, r, fpath)
}

func parseSegmentIndex(file string) int {
	base := filepath.Base(file)
	if !strings.HasPrefix(base, "stream") || !strings.HasSuffix(base, ".ts") {
		return -1
	}
	n := strings.TrimSuffix(strings.TrimPrefix(base, "stream"), ".ts")
	v, err := strconv.Atoi(n)
	if err != nil || v < 0 {
		return -1
	}
	return v
}

func (m *Manager) maybeSeekSession(s *session, requestedSegment int) bool {
	s.mu.Lock()
	currentStart := s.startSegment
	// Ignore tiny forward requests; they usually happen during normal startup.
	if requestedSegment <= currentStart+24 {
		s.mu.Unlock()
		return false
	}

	now := time.Now()

	// Always keep the farthest outstanding jump target.
	if requestedSegment > s.pendingJump {
		s.pendingJump = requestedSegment
	}

	// If a previous jump is still warming up, queue the farthest request and
	// wait. This avoids restart thrashing (0->169->335 in quick succession).
	if s.jumpInProgress {
		s.mu.Unlock()
		return false
	}

	// Coalesce rapid consecutive segment misses (player may request multiple
	// candidate target segments). Wait a short window, then jump once to the
	// highest requested segment.
	if s.pendingSince.IsZero() {
		s.pendingSince = now
		s.mu.Unlock()
		return false
	}
	if now.Sub(s.pendingSince) < 700*time.Millisecond {
		s.mu.Unlock()
		return false
	}

	requestedSegment = s.pendingJump

	// Debounce jump restarts so repeated near-simultaneous segment requests
	// don't thrash ffmpeg with back-to-back cancels.
	// A jump restart can take several seconds to produce its first target
	// segment on remote MKV inputs; allow it to settle before considering
	// another jump.
	if !s.lastJumpAt.IsZero() && now.Sub(s.lastJumpAt) < 8*time.Second {
		s.mu.Unlock()
		return false
	}
	// Additional hysteresis: only jump again if request is *far* beyond the
	// current jump start. This prevents 0->168->335 cascades from a single seek.
	if requestedSegment <= currentStart+96 {
		s.mu.Unlock()
		return false
	}
	v := s.video
	oldCancel := s.cancel
	s.lastJumpAt = now
	s.jumpInProgress = true
	s.pendingJump = 0
	s.pendingSince = time.Time{}
	s.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	m.startSessionFFmpeg(s, v, requestedSegment)
	m.log.Info("hls: seek-jump restart",
		"video", v.ID, "from_segment", currentStart, "to_segment", requestedSegment)
	return true
}

// ServeKeepalive handles POST /hls/{videoID}/keepalive.
func (m *Manager) ServeKeepalive(w http.ResponseWriter, r *http.Request, videoID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	m.mu.Lock()
	s, ok := m.sessions[videoID]
	m.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// getOrStartSession returns the session UUID for videoID, reusing an existing
// live session or creating a new one as needed.
func (m *Manager) getOrStartSession(ctx context.Context, videoID int64, v db.Video) (string, error) {
	// Fast path: reuse an existing live session without probing.
	m.mu.Lock()
	if s, ok := m.sessions[videoID]; ok {
		select {
		case <-s.done:
			_ = os.RemoveAll(s.dir)
			delete(m.sessions, videoID)
		default:
			s.mu.Lock()
			s.lastUsed = time.Now()
			s.mu.Unlock()
			m.log.Debug("hls: reusing session", "video", videoID, "session", s.id)
			m.mu.Unlock()
			return s.id, nil
		}
	}
	m.mu.Unlock()

	// If duration is unknown, probe it now (before holding the sessions lock).
	// This is a one-time cost per video: once saved to the DB, subsequent plays
	// skip the probe entirely.
	if v.DurationSec == nil && m.streamBase != "" {
		streamURL := m.streamBase + "/stream/" + v.ContainerCID + "/oid/" + v.ObjectID
		probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		if pi, err := probe.URL(probeCtx, m.ffprobe, streamURL); err == nil && pi.Duration > 0 {
			dur := pi.Duration
			v.DurationSec = &dur
			// Persist for future sessions — do it in the background so we don't
			// block session startup on a DB write.
			go func() {
				if err := m.db.UpdateVideoDuration(context.Background(), videoID, dur); err != nil {
					m.log.Debug("hls: save duration failed (non-fatal)", "err", err)
				}
			}()
			m.log.Info("hls: probed duration", "video", videoID, "duration", dur)
		}
		cancel()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-check: another goroutine might have created a session while we probed.
	if s, ok := m.sessions[videoID]; ok {
		select {
		case <-s.done:
			_ = os.RemoveAll(s.dir)
			delete(m.sessions, videoID)
		default:
			s.mu.Lock()
			s.lastUsed = time.Now()
			s.mu.Unlock()
			m.log.Debug("hls: reusing session (post-probe)", "video", videoID, "session", s.id)
			return s.id, nil
		}
	}

	sessID := uuid.New().String()
	dir := filepath.Join(m.tempDir, fmt.Sprintf("%d-%s", videoID, sessID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	const hlsTimeSec = 4.0 // must match HLSTime in the profile

	s := &session{
		id:          sessID,
		dir:         dir,
		lastUsed:    time.Now(),
		vodManifest: buildVODManifest(v.DurationSec, hlsTimeSec),
		video:       v,
	}
	m.sessions[videoID] = s

	m.startSessionFFmpeg(s, v, 0)
	m.log.Info("hls: started session", "video", videoID, "session", sessID,
		"vod_manifest", s.vodManifest != "")
	return sessID, nil
}

// buildVODManifest returns a complete VOD manifest listing all expected segment
// files for the known duration. This allows the player to request future
// segments immediately (seek ahead) while ServeSegment blocks until those
// segments are produced or seek-jumps the transcoder.
func buildVODManifest(durationSec *float64, _ float64) string {
	if durationSec == nil || *durationSec <= 0 {
		return ""
	}
	const hlsTimeSec = 4.0
	dur := *durationSec
	targetDuration := int(math.Ceil(hlsTimeSec))
	numFull := int(dur / hlsTimeSec)
	lastSec := dur - float64(numFull)*hlsTimeSec

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:3\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", targetDuration)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")

	for i := 0; i < numFull; i++ {
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n", hlsTimeSec)
		fmt.Fprintf(&b, "stream%d.ts\n", i)
	}
	if lastSec > 0.01 {
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n", lastSec)
		fmt.Fprintf(&b, "stream%d.ts\n", numFull)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

func (m *Manager) startSessionFFmpeg(s *session, v db.Video, startSegment int) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	s.mu.Lock()
	s.cancel = cancel
	s.done = done
	s.video = v
	s.startSegment = startSegment
	s.running = true
	s.jumpInProgress = startSegment > 0
	if startSegment == 0 {
		s.pendingJump = 0
		s.pendingSince = time.Time{}
	}
	s.mu.Unlock()

	go func() {
		defer close(done)
		m.runFFmpeg(ctx, s, v, startSegment)
		s.mu.Lock()
		if s.done == done {
			s.running = false
		}
		s.mu.Unlock()
	}()
}

func (m *Manager) runFFmpeg(ctx context.Context, s *session, v db.Video, startSegment int) {

	var containerID cid.ID
	if err := containerID.DecodeString(v.ContainerCID); err != nil {
		m.log.Error("hls: bad container cid", "err", err)
		return
	}
	var objectID oid.ID
	if err := objectID.DecodeString(v.ObjectID); err != nil {
		m.log.Error("hls: bad object oid", "err", err)
		return
	}

	// ── Probe codec info ────────────────────────────────────────────────────
	// Only probe on the initial run. Seek-jump restarts must be fast and should
	// not block on metadata probing.
	var probeInfo *probe.Result
	mode := ModeSoftware
	if startSegment == 0 {
		if m.streamBase != "" {
			streamURL := m.streamBase + "/stream/" + v.ContainerCID + "/oid/" + v.ObjectID
			// Keep startup snappy: probing can take several seconds on some MKV files.
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			if pi, err := probe.URL(probeCtx, m.ffprobe, streamURL); err == nil {
				probeInfo = pi
			} else {
				m.log.Debug("hls: probe failed (non-fatal, will use software encode)", "err", err)
			}
			cancel()
		}
		mode = SelectMode(probeInfo, m.caps)
	}

	// ── Select transcode mode ───────────────────────────────────────────────
	codecDesc := "unknown"
	if probeInfo != nil {
		codecDesc = probeInfo.VideoCodec + "/" + probeInfo.AudioCodec
	}
	m.log.Info("hls: transcode mode selected",
		"video", v.ID, "mode", mode, "source_codec", codecDesc)

	// ── Build ffmpeg command ─────────────────────────────────────────────────
	p := Profiles[DefaultProfile]
	hlsTimeSec, _ := strconv.ParseFloat(p.HLSTime, 64)
	if hlsTimeSec <= 0 {
		hlsTimeSec = 4
	}

	// Runtime fallback chain when hardware mode fails at encoder init.
	modeChain := []TranscodeMode{mode}
	switch mode {
	case ModeVAAPI:
		if m.caps.NVENC {
			modeChain = append(modeChain, ModeNVENC)
		}
		modeChain = append(modeChain, ModeSoftware)
	case ModeNVENC:
		modeChain = append(modeChain, ModeSoftware)
	}

	streamURL := ""
	if m.streamBase != "" {
		streamURL = m.streamBase + "/stream/" + v.ContainerCID + "/oid/" + v.ObjectID
	}

	var lastErr error
	for i, tryMode := range modeChain {
		if i > 0 {
			m.log.Warn("hls: falling back transcode mode", "video", v.ID, "from", modeChain[i-1], "to", tryMode)
		}

		preInput, postInput := BuildFFmpegArgs(tryMode, probeInfo, m.caps)
		commonTail := append([]string{}, postInput...)
		commonTail = append(commonTail,
			"-hls_time", p.HLSTime,
			"-hls_list_size", "0",
			"-hls_segment_filename", filepath.Join(s.dir, "stream%d.ts"),
			"-hls_flags", "split_by_time",
			"-start_number", strconv.Itoa(startSegment),
			"-f", "hls",
			filepath.Join(s.dir, "stream.m3u8"),
		)

		// Attempt 1: stdin from NeoFS reader (linear mode only).
		if startSegment == 0 {
			reader, err := m.nfs.Get(ctx, containerID, objectID, nil)
			if err != nil {
				m.log.Error("hls: neofs get", "err", err)
				return
			}
			stdinArgs := []string{"-y"}
			stdinArgs = append(stdinArgs, preInput...)
			stdinArgs = append(stdinArgs, "-i", "pipe:0")
			stdinArgs = append(stdinArgs, commonTail...)

			cmd := exec.CommandContext(ctx, m.ffmpeg, stdinArgs...)
			cmd.Stdin = reader
			cmd.Stderr = &logPrefixWriter{prefix: "ffmpeg-hls: ", log: m.log}
			runErr := cmd.Run()
			_ = reader.Close()
			if runErr == nil || ctx.Err() != nil {
				return
			}
			lastErr = runErr
			m.log.Warn("hls: ffmpeg stdin input failed, retrying via HTTP stream URL",
				"err", runErr, "video", v.ID, "mode", tryMode)
		}

		// Attempt 2: HTTP stream URL.
		if streamURL != "" {
			retryArgs := []string{"-y"}
			retryArgs = append(retryArgs, preInput...)
			if startSegment > 0 {
				offsetSec := float64(startSegment) * hlsTimeSec
				retryArgs = append(retryArgs, "-ss", fmt.Sprintf("%.3f", offsetSec))
			}
			retryArgs = append(retryArgs, "-i", streamURL)
			retryArgs = append(retryArgs, commonTail...)

			retryCmd := exec.CommandContext(ctx, m.ffmpeg, retryArgs...)
			retryCmd.Stderr = &logPrefixWriter{prefix: "ffmpeg-hls: ", log: m.log}
			runErr := retryCmd.Run()
			if runErr == nil || ctx.Err() != nil {
				return
			}
			lastErr = runErr
		}
	}

	if ctx.Err() == nil && lastErr != nil {
		m.log.Error("hls: ffmpeg exited", "err", lastErr)
	}
}

// cleanupLoop kills and removes sessions idle for more than idleTimeout.
func (m *Manager) cleanupLoop() {
	const idleTimeout = 45 * time.Second
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for range t.C {
		m.mu.Lock()
		for id, s := range m.sessions {
			s.mu.Lock()
			idle := time.Since(s.lastUsed)
			s.mu.Unlock()
			if idle > idleTimeout {
				m.log.Info("hls: evicting idle session", "video", id, "idle", idle.Round(time.Second))
				s.cancel()
				_ = os.RemoveAll(s.dir)
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
	}
}

// logPrefixWriter writes ffmpeg stderr lines to slog.
type logPrefixWriter struct {
	prefix string
	log    *slog.Logger
	buf    strings.Builder
}

func (w *logPrefixWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		s := w.buf.String()
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			break
		}
		line := strings.TrimRight(s[:nl], "\r")
		if line != "" {
			w.log.Debug(w.prefix + line)
		}
		w.buf.Reset()
		w.buf.WriteString(s[nl+1:])
	}
	return len(p), nil
}

// Ensure Manager implements the api.HLSManagerI interface.
var _ interface {
	ServeMasterPlaylist(w http.ResponseWriter, r *http.Request, videoID int64)
	ServeSegment(w http.ResponseWriter, r *http.Request, videoID int64, session, file string)
	ServeKeepalive(w http.ResponseWriter, r *http.Request, videoID int64)
} = (*Manager)(nil)

// UploadFile uploads a local file to NeoFS and returns its object ID.
func UploadFile(ctx context.Context, nfs *neofs.Client, containerCID, localPath, filename, contentType string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	var cidObj cid.ID
	if err := cidObj.DecodeString(containerCID); err != nil {
		return "", fmt.Errorf("cid: %w", err)
	}

	oidObj, err := nfs.Put(ctx, cidObj, filename, contentType, f)
	if err != nil {
		return "", fmt.Errorf("put: %w", err)
	}
	return oidObj.String(), nil
}

// DrainToTempFile streams an io.Reader to a temporary file and returns its path.
func DrainToTempFile(dir, pattern string, r io.Reader) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return "", err
	}
	return f.Name(), nil
}
