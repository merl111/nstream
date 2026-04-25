package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// UploadJob tracks one background file upload (browser→server already done,
// server→NeoFS + auto-match running in a goroutine).
type UploadJob struct {
	ID         string  `json:"id"`
	Filename   string  `json:"filename"`
	Status     string  `json:"status"` // queued|neofs|probing|matching|done|error
	Pct        int     `json:"pct,omitempty"`
	VideoID    int64   `json:"video_id,omitempty"`
	MediaID    int64   `json:"media_id,omitempty"`
	MediaTitle string  `json:"media_title,omitempty"`
	Error      string  `json:"error,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

func (j *UploadJob) active() bool {
	return j.Status != "done" && j.Status != "error"
}

// uploadManager is a thread-safe in-memory store of recent upload jobs.
// Completed jobs are pruned after pruneAfter.
type uploadManager struct {
	mu         sync.RWMutex
	jobs       []*UploadJob
	pruneAfter time.Duration
}

func newUploadManager() *uploadManager {
	return &uploadManager{pruneAfter: 30 * time.Minute}
}

func (m *uploadManager) newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *uploadManager) add(filename string) *UploadJob {
	now := time.Now().UTC().Format(time.RFC3339)
	j := &UploadJob{
		ID:        m.newID(),
		Filename:  filename,
		Status:    "queued",
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.mu.Lock()
	m.prune()
	m.jobs = append([]*UploadJob{j}, m.jobs...)
	m.mu.Unlock()
	return j
}

// update mutates a job under the write lock and refreshes UpdatedAt.
func (m *uploadManager) update(id string, fn func(*UploadJob)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ID == id {
			fn(j)
			j.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			return
		}
	}
}

// list returns a snapshot of all jobs, newest first.
func (m *uploadManager) list() []UploadJob {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]UploadJob, len(m.jobs))
	for i, j := range m.jobs {
		out[i] = *j
	}
	return out
}

// prune removes completed jobs older than pruneAfter. Must be called under lock.
func (m *uploadManager) prune() {
	cutoff := time.Now().Add(-m.pruneAfter)
	kept := m.jobs[:0]
	for _, j := range m.jobs {
		if j.active() {
			kept = append(kept, j)
			continue
		}
		t, err := time.Parse(time.RFC3339, j.UpdatedAt)
		if err != nil || t.After(cutoff) {
			kept = append(kept, j)
		}
	}
	m.jobs = kept
}

// handleListUploadJobs returns the current job list.
// GET /api/v1/upload/jobs
func (a *API) handleListUploadJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	jobs := a.uploads.list()
	if jobs == nil {
		jobs = []UploadJob{}
	}
	writeJSON(w, http.StatusOK, jobs)
}
