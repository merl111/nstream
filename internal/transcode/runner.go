package transcode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cid "github.com/nspcc-dev/neofs-sdk-go/container/id"
	oid "github.com/nspcc-dev/neofs-sdk-go/object/id"

	"nstream/internal/db"
	"nstream/internal/neofs"
)

// Runner processes background pre-transcode jobs from the DB.
type Runner struct {
	db      *db.DB
	nfs     *neofs.Client
	ffmpeg  string
	tempDir string
	log     *slog.Logger
	workers int
	kick    chan struct{}
	once    sync.Once
}

// NewRunner creates a Runner. workers controls the max parallel ffmpeg processes.
func NewRunner(database *db.DB, nfs *neofs.Client, ffmpeg, tempDir string, workers int, log *slog.Logger) *Runner {
	if workers <= 0 {
		workers = 2
	}
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	return &Runner{
		db:      database,
		nfs:     nfs,
		ffmpeg:  ffmpeg,
		tempDir: tempDir,
		workers: workers,
		log:     log,
		kick:    make(chan struct{}, 1),
	}
}

// Kick signals the runner to check for pending jobs immediately.
// Implements api.JobRunnerI.
func (r *Runner) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run starts the worker pool and processes jobs until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	sem := make(chan struct{}, r.workers)

	process := func() {
		for {
			job, err := r.db.ClaimPendingJob(ctx)
			if err != nil {
				r.log.Error("runner: claim job", "err", err)
				break
			}
			if job == nil {
				break
			}
			sem <- struct{}{}
			go func(j db.TranscodeJob) {
				defer func() { <-sem }()
				r.processJob(ctx, j)
			}(*job)
		}
	}

	// Drain any jobs left from a previous run.
	process()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
			process()
		case <-time.After(5 * time.Minute):
			process()
		}
	}
}

func (r *Runner) processJob(ctx context.Context, j db.TranscodeJob) {
	r.log.Info("runner: processing job", "id", j.ID, "video", j.VideoID)

	v, err := r.db.GetVideoByID(ctx, j.VideoID)
	if err != nil || v == nil {
		r.log.Error("runner: get video", "err", err, "job", j.ID)
		_ = r.db.FinishJob(ctx, j.ID, false, "video not found")
		return
	}

	workDir := filepath.Join(r.tempDir, fmt.Sprintf("job-%d", j.ID))
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		_ = r.db.FinishJob(ctx, j.ID, false, err.Error())
		return
	}
	defer os.RemoveAll(workDir)

	p := Profiles[j.Profile]
	if p.Name == "" {
		p = Profiles[DefaultProfile]
	}

	var containerID cid.ID
	if err := containerID.DecodeString(v.ContainerCID); err != nil {
		_ = r.db.FinishJob(ctx, j.ID, false, "invalid container cid: "+err.Error())
		return
	}
	var objectID oid.ID
	if err := objectID.DecodeString(v.ObjectID); err != nil {
		_ = r.db.FinishJob(ctx, j.ID, false, "invalid object id: "+err.Error())
		return
	}

	reader, err := r.nfs.Range(ctx, containerID, objectID, 0, 0, nil)
	if err != nil {
		_ = r.db.FinishJob(ctx, j.ID, false, "neofs range: "+err.Error())
		return
	}
	defer reader.Close()

	playlistPath := filepath.Join(workDir, "stream.m3u8")
	args := []string{"-y", "-i", "pipe:0"}
	args = append(args, p.VideoArgs...)
	args = append(args, p.AudioArgs...)
	args = append(args,
		"-hls_time", p.HLSTime,
		"-hls_list_size", "0",
		"-f", "hls",
		playlistPath,
	)

	cmd := exec.CommandContext(ctx, r.ffmpeg, args...)
	cmd.Stdin = reader
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		msg := fmt.Sprintf("ffmpeg: %v\n%s", err, stderrBuf.String())
		r.log.Error("runner: ffmpeg failed", "err", msg, "job", j.ID)
		_ = r.db.FinishJob(ctx, j.ID, false, msg)
		return
	}

	// Upload all generated files back to NeoFS.
	entries, err := os.ReadDir(workDir)
	if err != nil {
		_ = r.db.FinishJob(ctx, j.ID, false, "readdir: "+err.Error())
		return
	}

	var playlistOID string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ct := "video/MP2T"
		if strings.HasSuffix(name, ".m3u8") {
			ct = "application/vnd.apple.mpegurl"
		}
		fPath := filepath.Join(workDir, name)
		f, err := os.Open(fPath)
		if err != nil {
			_ = r.db.FinishJob(ctx, j.ID, false, "open segment: "+err.Error())
			return
		}
		uploadedOID, err := r.nfs.Put(ctx, containerID, name, ct, f)
		f.Close()
		if err != nil {
			_ = r.db.FinishJob(ctx, j.ID, false, "upload: "+err.Error())
			return
		}
		if name == "stream.m3u8" {
			playlistOID = uploadedOID.String()
		}
		r.log.Debug("runner: uploaded segment", "name", name, "oid", uploadedOID)
	}

	if err := r.db.SetTranscodeStatus(ctx, v.ID, "done", playlistOID); err != nil {
		r.log.Error("runner: set transcode status", "err", err)
	}
	if err := r.db.FinishJob(ctx, j.ID, true, ""); err != nil {
		r.log.Error("runner: finish job", "err", err)
	}
	r.log.Info("runner: job done", "id", j.ID, "playlist_oid", playlistOID)
}
