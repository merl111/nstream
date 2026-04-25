// Package scanner periodically scans NeoFS containers and indexes discovered
// video objects into the nstream SQLite library.
package scanner

import (
	"context"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/nspcc-dev/neofs-sdk-go/object"
	"nstream/internal/db"
	"nstream/internal/neofs"

	cid "github.com/nspcc-dev/neofs-sdk-go/container/id"
)

// videoExtensions is the set of filename extensions we treat as video.
var videoExtensions = map[string]bool{
	".mp4": true, ".mkv": true, ".webm": true, ".avi": true,
	".mov": true, ".m4v": true, ".ts": true, ".flv": true,
	".wmv": true, ".mpg": true, ".mpeg": true, ".3gp": true,
}

// isVideoFilename returns true if the filename has a recognised video extension.
func isVideoFilename(name string) bool {
	dot := strings.LastIndexByte(name, '.')
	if dot < 0 {
		return false
	}
	return videoExtensions[strings.ToLower(name[dot:])]
}

// Scanner indexes NeoFS containers into the DB.
type Scanner struct {
	db       *db.DB
	nfs      *neofs.Client
	interval time.Duration
	ffprobe  string
	log      *slog.Logger
}

// New creates a Scanner.
func New(database *db.DB, nfs *neofs.Client, interval time.Duration, ffprobe string, log *slog.Logger) *Scanner {
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	return &Scanner{db: database, nfs: nfs, interval: interval, ffprobe: ffprobe, log: log}
}

// Run starts the periodic scan loop; it returns when ctx is cancelled.
func (s *Scanner) Run(ctx context.Context) {
	s.scanAll(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.scanAll(ctx)
		}
	}
}

// ScanContainer performs a single on-demand scan of one container.
// Implements api.ScannerI.
func (s *Scanner) ScanContainer(c db.Container) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s.scanOne(ctx, c)
}

func (s *Scanner) scanAll(ctx context.Context) {
	containers, err := s.db.ScanEnabledContainers(ctx)
	if err != nil {
		s.log.Error("scanner: list containers", "err", err)
		return
	}
	for _, c := range containers {
		if ctx.Err() != nil {
			return
		}
		s.scanOne(ctx, c)
	}
}

func (s *Scanner) scanOne(ctx context.Context, c db.Container) {
	s.log.Info("scanner: scanning container", "cid", c.CID, "name", c.Name)

	var cidObj cid.ID
	if err := cidObj.DecodeString(c.CID); err != nil {
		s.log.Error("scanner: invalid cid", "cid", c.CID, "err", err)
		return
	}

	filters := object.NewSearchFilters()
	filters.AddRootFilter()

	results, _, err := s.nfs.Search(ctx, cidObj, filters, nil)
	if err != nil {
		s.log.Error("scanner: search", "cid", c.CID, "err", err)
		return
	}

	var indexed int
	for _, oid := range results {
		if ctx.Err() != nil {
			return
		}
		oidStr := oid.String()

		// Skip if already indexed.
		exists, err := s.db.VideoExists(ctx, c.ID, oidStr)
		if err != nil || exists {
			continue
		}

		// Fetch header to get filename, size, content-type.
		hdr, err := s.nfs.Head(ctx, cidObj, oid, nil)
		if err != nil {
			s.log.Debug("scanner: head failed", "oid", oidStr, "err", err)
			continue
		}

		filename := ""
		contentType := ""
		for _, attr := range hdr.Attributes() {
			switch attr.Key() {
			case object.AttributeFileName:
				filename = attr.Value()
			case object.AttributeContentType:
				contentType = attr.Value()
			}
		}
		if filename == "" {
			filename = oidStr
		}

		// Filter: only index video files.
		if !isVideoFilename(filename) && !strings.HasPrefix(contentType, "video/") {
			continue
		}

		size := int64(hdr.PayloadSize())
		dur := s.probeDuration(ctx, cidObj.String(), oidStr)

		v := db.Video{
			ContainerID: c.ID,
			ObjectID:    oidStr,
			Filename:    filename,
			ContentType: contentType,
			SizeBytes:   &size,
			DurationSec: dur,
		}
		if err := s.db.UpsertVideo(ctx, v); err != nil {
			s.log.Error("scanner: upsert video", "oid", oidStr, "err", err)
			continue
		}
		indexed++
	}

	_ = s.db.TouchContainerScan(ctx, c.ID)
	s.log.Info("scanner: done", "cid", c.CID, "new", indexed)
}

// probeDuration runs ffprobe against the NeoFS stream URL to get the duration.
// Returns nil if ffprobe is unavailable or fails.
func (s *Scanner) probeDuration(ctx context.Context, cid, oid string) *float64 {
	// Build a URL that our own server would serve; but we're calling ffprobe
	// directly on the NeoFS gRPC. Use the /stream/ URL assuming the server is
	// on localhost:8080. If that's not reliable, skip silently.
	probe, err := exec.LookPath(s.ffprobe)
	if err != nil {
		return nil
	}
	// We probe via our own stream endpoint: this is a best-effort operation.
	// The timeout ensures it won't block scans.
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	url := "http://localhost:8080/stream/" + cid + "/oid/" + oid
	out, err := exec.CommandContext(probeCtx, probe,
		"-v", "quiet",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		url,
	).Output()
	if err != nil {
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || f <= 0 {
		return nil
	}
	return &f
}
