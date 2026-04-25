package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"nstream/internal/db"
	"nstream/internal/probe"
)

type videoJSON struct {
	ID              int64    `json:"id"`
	ContainerID     int64    `json:"container_id"`
	ContainerCID    string   `json:"container_cid"`
	ObjectID        string   `json:"object_id"`
	Filename        string   `json:"filename"`
	Title           string   `json:"title"`
	DurationSec     *float64 `json:"duration_sec"`
	SizeBytes       *int64   `json:"size_bytes"`
	ContentType     string   `json:"content_type"`
	ThumbnailOID    string   `json:"thumbnail_oid,omitempty"`
	TranscodeStatus string   `json:"transcode_status"`
	TranscodeOID    string   `json:"transcode_oid,omitempty"`
	MediaID         *int64   `json:"media_id,omitempty"`
	EpisodeID       *int64   `json:"episode_id,omitempty"`
	IndexedAt       string   `json:"indexed_at"`
	StreamURL       string   `json:"stream_url"`
	HLSURL          string   `json:"hls_url"`
}

func toVideoJSON(v db.Video) videoJSON {
	j := videoJSON{
		ID:              v.ID,
		ContainerID:     v.ContainerID,
		ContainerCID:    v.ContainerCID,
		ObjectID:        v.ObjectID,
		Filename:        v.Filename,
		Title:           v.Title,
		DurationSec:     v.DurationSec,
		SizeBytes:       v.SizeBytes,
		ContentType:     v.ContentType,
		ThumbnailOID:    v.ThumbnailOID,
		TranscodeStatus: v.TranscodeStatus,
		TranscodeOID:    v.TranscodeOID,
		MediaID:         v.MediaID,
		EpisodeID:       v.EpisodeID,
		IndexedAt:       v.IndexedAt.Format("2006-01-02T15:04:05Z"),
		StreamURL:       fmt.Sprintf("/stream/%s/oid/%s", v.ContainerCID, v.ObjectID),
		HLSURL:          fmt.Sprintf("/hls/%d/master.m3u8", v.ID),
	}
	return j
}

func (a *API) handleListVideos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := queryInt(r, "limit", 40)
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 40
	}
	offset := (page - 1) * limit

	videos, total, err := a.db.ListVideos(r.Context(), q, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]videoJSON, len(videos))
	for i, v := range videos {
		out[i] = toVideoJSON(v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":  total,
		"page":   page,
		"limit":  limit,
		"videos": out,
	})
}

func (a *API) handleGetVideo(w http.ResponseWriter, r *http.Request) {
	idStr := pathSegment(r.URL.Path, "/api/v1/videos/")
	var id int64
	if _, err := parseID(idStr, &id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid video id")
		return
	}
	v, err := a.db.GetVideoByID(r.Context(), id)
	if err != nil || v == nil {
		writeError(w, http.StatusNotFound, "video not found")
		return
	}
	// Probe duration during "open video" flow so player has duration before play.
	// Keep timeout short to avoid making the endpoint feel slow.
	if v.DurationSec == nil && a.listenAddr != "" {
		streamURL := "http://localhost" + a.listenAddr + "/stream/" + v.ContainerCID + "/oid/" + v.ObjectID
		pctx, cancel := context.WithTimeout(r.Context(), 2500*time.Millisecond)
		if pi, perr := probe.URL(pctx, a.ffprobe, streamURL); perr == nil && pi.Duration > 0 {
			d := pi.Duration
			v.DurationSec = &d
			if err := a.db.UpdateVideoDuration(context.Background(), v.ID, d); err != nil {
				a.log.Debug("video: save probed duration failed (non-fatal)", "video", v.ID, "err", err)
			}
		}
		cancel()
	}
	writeJSON(w, http.StatusOK, toVideoJSON(*v))
}
