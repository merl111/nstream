package db

import (
	"context"
	"database/sql"
	"time"
)

// TranscodeJob is a background transcoding work item.
type TranscodeJob struct {
	ID         int64
	VideoID    int64
	Status     string
	Profile    string
	Error      string
	StartedAt  *time.Time
	FinishedAt *time.Time
	CreatedAt  time.Time
}

// CreateJob inserts a new pending transcode job.
func (d *DB) CreateJob(ctx context.Context, videoID int64, profile string) (*TranscodeJob, error) {
	var j TranscodeJob
	err := d.QueryRowContext(ctx,
		`INSERT INTO transcode_jobs(video_id, profile) VALUES(?,?)
		 RETURNING id, video_id, status, profile, error, started_at, finished_at, created_at`,
		videoID, profile,
	).Scan(&j.ID, &j.VideoID, &j.Status, &j.Profile,
		nullScanString(&j.Error),
		(*sql.NullTime)(nil), (*sql.NullTime)(nil), &j.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// ClaimPendingJob atomically marks the next pending job as running and returns it.
func (d *DB) ClaimPendingJob(ctx context.Context) (*TranscodeJob, error) {
	var j TranscodeJob
	err := d.QueryRowContext(ctx,
		`UPDATE transcode_jobs SET status='running', started_at=datetime('now')
		 WHERE id=(SELECT id FROM transcode_jobs WHERE status='pending' ORDER BY created_at LIMIT 1)
		 RETURNING id, video_id, status, profile, error, started_at, finished_at, created_at`,
	).Scan(&j.ID, &j.VideoID, &j.Status, &j.Profile,
		nullScanString(&j.Error),
		scanNullTime(&j.StartedAt), scanNullTime(&j.FinishedAt), &j.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &j, err
}

// FinishJob marks a job as done or failed.
func (d *DB) FinishJob(ctx context.Context, id int64, success bool, errMsg string) error {
	status := "done"
	if !success {
		status = "failed"
	}
	_, err := d.ExecContext(ctx,
		`UPDATE transcode_jobs SET status=?, error=?, finished_at=datetime('now') WHERE id=?`,
		status, nullString(errMsg), id)
	return err
}

// ListJobs returns all jobs ordered by newest first.
func (d *DB) ListJobs(ctx context.Context) ([]TranscodeJob, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, video_id, status, profile, COALESCE(error,''), started_at, finished_at, created_at
		 FROM transcode_jobs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// GetJob fetches a single job by ID.
func (d *DB) GetJob(ctx context.Context, id int64) (*TranscodeJob, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, video_id, status, profile, COALESCE(error,''), started_at, finished_at, created_at
		 FROM transcode_jobs WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	js, err := scanJobs(rows)
	if err != nil || len(js) == 0 {
		return nil, err
	}
	return &js[0], nil
}

func scanJobs(rows *sql.Rows) ([]TranscodeJob, error) {
	var out []TranscodeJob
	for rows.Next() {
		var j TranscodeJob
		if err := rows.Scan(
			&j.ID, &j.VideoID, &j.Status, &j.Profile, &j.Error,
			scanNullTime(&j.StartedAt), scanNullTime(&j.FinishedAt), &j.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// nullScanString is a dest that scans a nullable TEXT into a *string.
func nullScanString(dst *string) *sql.NullString {
	return &sql.NullString{}
}

// scanNullTime returns a scan destination that writes into *time.Time.
type nullTimeDest struct{ p **time.Time }

func (n nullTimeDest) Scan(v any) error {
	if v == nil {
		*n.p = nil
		return nil
	}
	nt := new(time.Time)
	switch val := v.(type) {
	case time.Time:
		*nt = val
	case string:
		t, err := time.Parse("2006-01-02 15:04:05", val)
		if err != nil {
			return err
		}
		*nt = t
	}
	*n.p = nt
	return nil
}

func scanNullTime(p **time.Time) sql.Scanner {
	return nullTimeDest{p}
}
