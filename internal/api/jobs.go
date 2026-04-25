package api

import (
	"net/http"

	"nstream/internal/db"
)

func (a *API) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		VideoID int64  `json:"video_id"`
		Profile string `json:"profile"`
	}
	if err := readJSON(r, &req); err != nil || req.VideoID == 0 {
		writeError(w, http.StatusBadRequest, "video_id required")
		return
	}
	if req.Profile == "" {
		req.Profile = "hls-h264"
	}
	v, err := a.db.GetVideoByID(r.Context(), req.VideoID)
	if err != nil || v == nil {
		writeError(w, http.StatusNotFound, "video not found")
		return
	}
	job, err := a.db.CreateJob(r.Context(), req.VideoID, req.Profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if a.jobRunner != nil {
		a.jobRunner.Kick()
	}
	writeJSON(w, http.StatusCreated, toJobJSON(*job))
}

func (a *API) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := a.db.ListJobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]jobJSON, len(jobs))
	for i, j := range jobs {
		out[i] = toJobJSON(j)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleGetJob(w http.ResponseWriter, r *http.Request) {
	idStr := pathSegment(r.URL.Path, "/api/v1/jobs/")
	var id int64
	if _, err := parseID(idStr, &id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	j, err := a.db.GetJob(r.Context(), id)
	if err != nil || j == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, toJobJSON(*j))
}

type jobJSON struct {
	ID         int64   `json:"id"`
	VideoID    int64   `json:"video_id"`
	Status     string  `json:"status"`
	Profile    string  `json:"profile"`
	Error      string  `json:"error,omitempty"`
	StartedAt  *string `json:"started_at,omitempty"`
	FinishedAt *string `json:"finished_at,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

func toJobJSON(j db.TranscodeJob) jobJSON {
	out := jobJSON{
		ID:        j.ID,
		VideoID:   j.VideoID,
		Status:    j.Status,
		Profile:   j.Profile,
		Error:     j.Error,
		CreatedAt: j.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if j.StartedAt != nil {
		s := j.StartedAt.Format("2006-01-02T15:04:05Z")
		out.StartedAt = &s
	}
	if j.FinishedAt != nil {
		s := j.FinishedAt.Format("2006-01-02T15:04:05Z")
		out.FinishedAt = &s
	}
	return out
}
