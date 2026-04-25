package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	cid "github.com/nspcc-dev/neofs-sdk-go/container/id"

	"nstream/internal/db"
	"nstream/internal/probe"
	"nstream/internal/tmdb"
)

// uploadEvent is the shape of every SSE data payload.
type uploadEvent struct {
	Phase   string     `json:"phase"`             // "receiving"|"received"|"queued"|"neofs"|"probing"|"matching"|"matched"|"unmatched"|"done"|"error"
	Pct     int        `json:"pct,omitempty"`     // 0-100, present during "receiving" and "neofs"
	Bytes   int64      `json:"bytes,omitempty"`   // bytes received from browser
	JobID   string     `json:"job_id,omitempty"`  // background job ID, present on "queued"
	Title   string     `json:"title,omitempty"`   // detected title
	IsTV    bool       `json:"is_tv,omitempty"`
	Season  int        `json:"season,omitempty"`
	Episode int        `json:"episode,omitempty"`
	MediaID int64      `json:"media_id,omitempty"` // matched DB media item
	TmdbID  int        `json:"tmdb_id,omitempty"`
	Video   *videoJSON `json:"video,omitempty"`   // present on "done" (legacy)
	Error   string     `json:"error,omitempty"`   // present on "error"
}

// sseWriter wraps a ResponseWriter and sends Server-Sent Event frames.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func newSSEWriter(w http.ResponseWriter) (*sseWriter, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	return &sseWriter{w: w, flusher: f}, true
}

func (s *sseWriter) send(ev uploadEvent) {
	b, _ := json.Marshal(ev)
	fmt.Fprintf(s.w, "data: %s\n\n", b)
	s.flusher.Flush()
}

// progressReader wraps an io.Reader and fires a callback every time a chunk
// is read, reporting the percentage of total bytes consumed.
type progressReader struct {
	r       io.Reader
	total   int64
	read    int64
	lastPct int
	cb      func(pct int)
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 && p.total > 0 {
		p.read += int64(n)
		pct := int(p.read * 100 / p.total)
		if pct != p.lastPct {
			p.lastPct = pct
			p.cb(pct)
		}
	}
	return n, err
}

// handleUpload handles POST /api/v1/upload.
//
// SSE event sequence (client can close after "queued"):
//
//	{"phase":"receiving","pct":N}    — browser→server progress  (0-100)
//	{"phase":"received","bytes":N}   — file fully on server
//	{"phase":"queued","job_id":"…"}  — NeoFS upload started in background; safe to close
//	{"phase":"error","error":"…"}    — something failed before queuing
func (a *API) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	sse, ok := newSSEWriter(w)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Wrap r.Body so we can report browser→server upload progress via SSE.
	// Content-Length covers the whole multipart body; good-enough for a progress bar.
	if r.ContentLength > 0 {
		pr := &progressReader{
			r:     r.Body,
			total: r.ContentLength,
			cb:    func(pct int) { sse.send(uploadEvent{Phase: "receiving", Pct: pct}) },
		}
		r.Body = io.NopCloser(pr)
	}

	// ParseMultipartForm reads r.Body; progress callbacks fire during this call.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		sse.send(uploadEvent{Phase: "error", Error: "multipart parse: " + err.Error()})
		return
	}

	var containerID int64
	if _, err := parseID(r.FormValue("container_id"), &containerID); err != nil || containerID == 0 {
		sse.send(uploadEvent{Phase: "error", Error: "container_id required"})
		return
	}

	container, err := a.db.GetContainerByID(r.Context(), containerID)
	if err != nil || container == nil {
		sse.send(uploadEvent{Phase: "error", Error: "container not found"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		sse.send(uploadEvent{Phase: "error", Error: "file field required"})
		return
	}
	// NOTE: do NOT defer file.Close() here — ownership transfers to the goroutine.

	filename := filepath.Base(header.Filename)
	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = mimeFromFilename(filename)
	}

	var cidObj cid.ID
	if err := cidObj.DecodeString(container.CID); err != nil {
		file.Close()
		sse.send(uploadEvent{Phase: "error", Error: "invalid container cid"})
		return
	}

	// File is fully on the server.
	sse.send(uploadEvent{Phase: "received", Bytes: header.Size})

	// Register a background job and hand off to a goroutine.  The HTTP handler
	// returns immediately so the browser/modal can be closed.
	job := a.uploads.add(filename)
	form := r.MultipartForm // keep reference so temp files survive handler return
	go a.runUploadJob(job, file, form, container, cidObj, filename, contentType, header.Size)

	// Tell the client it's safe to close.
	sse.send(uploadEvent{Phase: "queued", JobID: job.ID})
}

// runUploadJob is the background goroutine that drives NeoFS + auto-match for
// one uploaded file.  It owns the multipart.File and form until it returns.
func (a *API) runUploadJob(
	job *UploadJob,
	file multipart.File,
	form *multipart.Form,
	container *db.Container,
	cidObj cid.ID,
	filename, contentType string,
	size int64,
) {
	defer file.Close()
	defer func() { _ = form.RemoveAll() }()

	setErr := func(msg string) {
		a.log.Error("upload job failed", "job", job.ID, "filename", filename, "err", msg)
		a.uploads.update(job.ID, func(j *UploadJob) {
			j.Status = "error"
			j.Error = msg
		})
	}

	// ── NeoFS put ──────────────────────────────────────────────────────────
	a.uploads.update(job.ID, func(j *UploadJob) { j.Status = "neofs" })
	a.log.Info("upload: putting to NeoFS", "filename", filename, "container", container.CID, "job", job.ID)

	pr := &progressReader{
		r:     file,
		total: size,
		cb: func(pct int) {
			a.uploads.update(job.ID, func(j *UploadJob) { j.Pct = pct })
		},
	}

	oidObj, err := a.nfs.Put(a.serverCtx, cidObj, filename, contentType, pr)
	if err != nil {
		setErr("neofs: " + err.Error())
		return
	}

	// ── DB record ──────────────────────────────────────────────────────────
	v := db.Video{
		ContainerID: container.ID,
		ObjectID:    oidObj.String(),
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   &size,
	}
	if err := a.db.UpsertVideo(a.serverCtx, v); err != nil {
		setErr("db: " + err.Error())
		return
	}
	created, err := a.db.GetVideoByObjectID(a.serverCtx, container.ID, oidObj.String())
	if err != nil || created == nil {
		setErr("db read-back failed")
		return
	}
	a.log.Info("upload: NeoFS done", "filename", filename, "oid", oidObj, "video_id", created.ID, "job", job.ID)
	a.uploads.update(job.ID, func(j *UploadJob) {
		j.VideoID = created.ID
		j.Pct = 100
	})

	// ── Auto-match + duration probe ────────────────────────────────────────
	a.uploads.update(job.ID, func(j *UploadJob) { j.Status = "probing" })
	res, _ := a.matchVideo(a.serverCtx, created, container.CID)

	// Save the probed duration so the HLS manager can build a complete VOD
	// manifest immediately (full seekbar from first play).
	if res != nil && res.Duration > 0 {
		dur := res.Duration
		if err := a.db.UpsertVideo(a.serverCtx, db.Video{
			ContainerID: created.ContainerID,
			ObjectID:    created.ObjectID,
			Filename:    created.Filename,
			ContentType: created.ContentType,
			SizeBytes:   created.SizeBytes,
			DurationSec: &dur,
		}); err != nil {
			a.log.Debug("upload: save duration failed (non-fatal)", "err", err)
		}
	}

	if res != nil && res.Matched && res.MediaItem != nil {
		a.uploads.update(job.ID, func(j *UploadJob) {
			j.Status = "done"
			j.MediaID = res.MediaItem.ID
			j.MediaTitle = res.MediaItem.Title
		})
	} else {
		a.uploads.update(job.ID, func(j *UploadJob) { j.Status = "done" })
	}
}

// matchResult carries the outcome of an auto-match attempt.
type matchResult struct {
	Matched   bool
	MediaItem *db.MediaItem
	TmdbID    int
	Parsed    tmdb.ParsedFilename
	Duration  float64 // seconds from ffprobe; 0 if unknown
}

// matchVideo probes the video via ffprobe, parses its title, searches TMDB
// and (if found) imports + links the media item.  It is safe to call from
// both the SSE upload path and from the standalone /match endpoint.
func (a *API) matchVideo(ctx context.Context, v *db.Video, containerCID string) (*matchResult, error) {
	host := "localhost" + a.listenAddr
	streamURL := "http://" + host + "/stream/" + containerCID + "/oid/" + v.ObjectID

	probed, err := probe.URL(ctx, a.ffprobe, streamURL)
	if err != nil {
		a.log.Debug("match: probe failed (non-fatal)", "err", err)
		probed = &probe.Result{}
	}
	probedDuration := probed.Duration

	// Build a prioritised list of ParsedFilename candidates to try with TMDB.
	// We always try the filename as a fallback even when the probed title is
	// non-empty, because embedded tags can contain garbled content (archive
	// URLs, full season titles, etc.) that confuses TMDB.
	var candidates []tmdb.ParsedFilename

	filenameP := tmdb.ParseFilename(v.Filename)

	var probedP tmdb.ParsedFilename
	if t := strings.TrimSpace(probed.Title); t != "" && !isGarbageTitle(t) {
		probedP = tmdb.ParseFilename(t)
	}

	// If the filename has a SxxExx pattern but the probed title doesn't, put
	// the filename parse first — it will produce a cleaner TMDB query.
	if filenameP.IsTV && !probedP.IsTV {
		if filenameP.Title != "" {
			candidates = append(candidates, filenameP)
		}
		if probedP.Title != "" {
			candidates = append(candidates, probedP)
		}
	} else {
		// Otherwise try the probed title first (richer metadata when good),
		// and fall back to the filename.
		if probedP.Title != "" {
			candidates = append(candidates, probedP)
		}
		if filenameP.Title != "" && filenameP.Title != probedP.Title {
			candidates = append(candidates, filenameP)
		}
	}

	if len(candidates) == 0 {
		return &matchResult{Duration: probedDuration}, nil
	}

	if a.tmdb == nil || !a.tmdb.Enabled() {
		return &matchResult{Parsed: candidates[0], Duration: probedDuration}, nil
	}

	// Try each candidate in order; stop at the first TMDB hit.
	for _, parsed := range candidates {
		mediaType := "movie"
		if parsed.IsTV {
			mediaType = "tv"
		}

		var results []tmdb.SearchResult
		if parsed.IsTV {
			results, err = a.tmdb.SearchTV(ctx, parsed.Title, parsed.Year, "en-US")
		} else {
			results, err = a.tmdb.SearchMovie(ctx, parsed.Title, parsed.Year, "en-US")
		}
		if err != nil || len(results) == 0 {
			a.log.Debug("match: TMDB found nothing, trying next candidate", "title", parsed.Title, "err", err)
			continue
		}

		tmdbID := results[0].ID
		mediaItem, err := a.importFromTMDB(ctx, tmdbID, mediaType, "en-US")
		if err != nil {
			a.log.Debug("match: import failed (non-fatal)", "err", err)
			continue
		}

		var episodeID *int64
		if parsed.IsTV && parsed.Season > 0 && parsed.Episode > 0 {
			ep, epErr := a.db.GetEpisodeByNumber(ctx, mediaItem.ID, parsed.Season, parsed.Episode)
			if epErr == nil && ep != nil {
				episodeID = &ep.ID
			}
		}
		if err := a.db.LinkVideoToMedia(ctx, v.ID, mediaItem.ID, episodeID); err != nil {
			a.log.Debug("match: link failed (non-fatal)", "err", err)
		}

		return &matchResult{Matched: true, MediaItem: mediaItem, TmdbID: tmdbID, Parsed: parsed, Duration: probedDuration}, nil
	}

	// Exhausted all candidates without a match.
	return &matchResult{Parsed: candidates[0], Duration: probedDuration}, nil
}

// isGarbageTitle returns true for embedded metadata values that should not be
// sent to TMDB as-is (archive URLs, overly long strings, etc.).
func isGarbageTitle(s string) bool {
	return strings.Contains(s, "://") ||
		strings.Contains(s, "archive.org") ||
		strings.ContainsAny(s, "<>{}[]|\\") ||
		len(s) > 200
}

// autoMatchSSE wraps matchVideo and emits SSE events for the upload flow.
func (a *API) autoMatchSSE(ctx context.Context, sse *sseWriter, v *db.Video, containerCID string) {
	sse.send(uploadEvent{Phase: "probing"})

	res, _ := a.matchVideo(ctx, v, containerCID)
	if res == nil || res.Parsed.Title == "" {
		sse.send(uploadEvent{Phase: "unmatched"})
		return
	}

	sse.send(uploadEvent{
		Phase:   "matching",
		Title:   res.Parsed.Title,
		IsTV:    res.Parsed.IsTV,
		Season:  res.Parsed.Season,
		Episode: res.Parsed.Episode,
	})

	if !res.Matched {
		sse.send(uploadEvent{Phase: "unmatched", Title: res.Parsed.Title})
		return
	}

	sse.send(uploadEvent{
		Phase:   "matched",
		Title:   res.MediaItem.Title,
		MediaID: res.MediaItem.ID,
		TmdbID:  res.TmdbID,
		IsTV:    res.Parsed.IsTV,
		Season:  res.Parsed.Season,
		Episode: res.Parsed.Episode,
	})
}

// handleMatchVideo handles POST /api/v1/videos/{id}/match.
// It probes the video's embedded metadata, searches TMDB, and links the best
// match — all without downloading the file.
func (a *API) handleMatchVideo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var videoID int64
	idStr := pathSegment(r.URL.Path, "/api/v1/videos/")
	idStr = strings.TrimSuffix(idStr, "/match")
	if _, err := parseID(idStr, &videoID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid video id")
		return
	}

	video, err := a.db.GetVideoByID(r.Context(), videoID)
	if err != nil || video == nil {
		writeError(w, http.StatusNotFound, "video not found")
		return
	}

	container, err := a.db.GetContainerByID(r.Context(), video.ContainerID)
	if err != nil || container == nil {
		writeError(w, http.StatusInternalServerError, "container not found")
		return
	}

	res, err := a.matchVideo(r.Context(), video, container.CID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type response struct {
		Matched   bool         `json:"matched"`
		Title     string       `json:"title,omitempty"`
		MediaID   int64        `json:"media_id,omitempty"`
		TmdbID    int          `json:"tmdb_id,omitempty"`
		IsTV      bool         `json:"is_tv,omitempty"`
		Season    int          `json:"season,omitempty"`
		Episode   int          `json:"episode,omitempty"`
	}
	out := response{
		Matched: res.Matched,
		Title:   res.Parsed.Title,
		IsTV:    res.Parsed.IsTV,
		Season:  res.Parsed.Season,
		Episode: res.Parsed.Episode,
	}
	if res.Matched && res.MediaItem != nil {
		out.Title = res.MediaItem.Title
		out.MediaID = res.MediaItem.ID
		out.TmdbID = res.TmdbID
	}
	writeJSON(w, http.StatusOK, out)
}

// mimeFromFilename returns a best-guess MIME type from the file extension.
func mimeFromFilename(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".ts":
		return "video/MP2T"
	case ".flv":
		return "video/x-flv"
	case ".wmv":
		return "video/x-ms-wmv"
	case ".mpg", ".mpeg":
		return "video/mpeg"
	case ".3gp":
		return "video/3gpp"
	default:
		return fmt.Sprintf("video/%s", strings.TrimPrefix(ext, "."))
	}
}
