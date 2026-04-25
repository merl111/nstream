package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UpdateVideoDuration stores the probed duration for a video.
// It is a targeted UPDATE used to backfill duration for videos that were
// indexed before automatic duration probing was added.
func (d *DB) UpdateVideoDuration(ctx context.Context, videoID int64, durationSec float64) error {
	_, err := d.ExecContext(ctx,
		`UPDATE videos SET duration_sec=? WHERE id=?`,
		durationSec, videoID)
	return err
}

// Container is a NeoFS container registered in the library.
type Container struct {
	ID            int64
	CID           string
	Name          string
	ScanEnabled   bool
	LastScannedAt *time.Time
	CreatedAt     time.Time
}

// Video is a media object stored in NeoFS and indexed in the library.
type Video struct {
	ID              int64
	ContainerID     int64
	ContainerCID    string
	ObjectID        string
	Filename        string
	Title           string
	DurationSec     *float64
	SizeBytes       *int64
	ContentType     string
	ThumbnailOID    string
	TranscodeStatus string
	TranscodeOID    string
	MediaID         *int64
	EpisodeID       *int64
	IndexedAt       time.Time
}

// -- Containers -----------------------------------------------------------

// AddContainer inserts a new container record.
func (d *DB) AddContainer(ctx context.Context, cid, name string) (*Container, error) {
	var c Container
	var last sql.NullTime
	err := d.QueryRowContext(ctx,
		`INSERT INTO containers(cid, name) VALUES(?,?) RETURNING id, cid, name, scan_enabled, last_scanned_at, created_at`,
		cid, name,
	).Scan(&c.ID, &c.CID, &c.Name, &c.ScanEnabled, &last, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("add container: %w", err)
	}
	if last.Valid {
		c.LastScannedAt = &last.Time
	}
	return &c, nil
}

// ListContainers returns all containers.
func (d *DB) ListContainers(ctx context.Context) ([]Container, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, cid, name, scan_enabled, last_scanned_at, created_at FROM containers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanContainers(rows)
}

// GetContainerByID fetches a single container.
func (d *DB) GetContainerByID(ctx context.Context, id int64) (*Container, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, cid, name, scan_enabled, last_scanned_at, created_at FROM containers WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cs, err := scanContainers(rows)
	if err != nil || len(cs) == 0 {
		return nil, err
	}
	return &cs[0], nil
}

// GetContainerByCID fetches a container by its NeoFS CID string.
func (d *DB) GetContainerByCID(ctx context.Context, cid string) (*Container, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, cid, name, scan_enabled, last_scanned_at, created_at FROM containers WHERE cid=?`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cs, err := scanContainers(rows)
	if err != nil || len(cs) == 0 {
		return nil, err
	}
	return &cs[0], nil
}

// DeleteContainer removes a container (cascade deletes its videos).
func (d *DB) DeleteContainer(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM containers WHERE id=?`, id)
	return err
}

// TouchContainerScan updates last_scanned_at to now.
func (d *DB) TouchContainerScan(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `UPDATE containers SET last_scanned_at=datetime('now') WHERE id=?`, id)
	return err
}

func scanContainers(rows *sql.Rows) ([]Container, error) {
	var out []Container
	for rows.Next() {
		var c Container
		var last sql.NullTime
		if err := rows.Scan(&c.ID, &c.CID, &c.Name, &c.ScanEnabled, &last, &c.CreatedAt); err != nil {
			return nil, err
		}
		if last.Valid {
			c.LastScannedAt = &last.Time
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// -- Videos ---------------------------------------------------------------

// UpsertVideo inserts or ignores a video entry (idempotent by container+object).
func (d *DB) UpsertVideo(ctx context.Context, v Video) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO videos(container_id, object_id, filename, title, duration_sec, size_bytes, content_type, thumbnail_oid)
		 VALUES(?,?,?,?,?,?,?,?)
		 ON CONFLICT(container_id, object_id) DO UPDATE SET
		   filename=excluded.filename,
		   title=COALESCE(excluded.title, title),
		   duration_sec=COALESCE(excluded.duration_sec, duration_sec),
		   size_bytes=COALESCE(excluded.size_bytes, size_bytes),
		   content_type=COALESCE(excluded.content_type, content_type)`,
		v.ContainerID, v.ObjectID, v.Filename,
		nullString(v.Title), v.DurationSec, v.SizeBytes,
		nullString(v.ContentType), nullString(v.ThumbnailOID),
	)
	return err
}

// GetVideoByID fetches a video by primary key, joining the container CID.
func (d *DB) GetVideoByID(ctx context.Context, id int64) (*Video, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT v.id, v.container_id, c.cid, v.object_id, v.filename, COALESCE(v.title,''),
		        v.duration_sec, v.size_bytes, COALESCE(v.content_type,''), COALESCE(v.thumbnail_oid,''),
		        v.transcode_status, COALESCE(v.transcode_oid,''), v.media_id, v.episode_id, v.indexed_at
		 FROM videos v JOIN containers c ON c.id=v.container_id WHERE v.id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	vs, err := scanVideos(rows)
	if err != nil || len(vs) == 0 {
		return nil, err
	}
	return &vs[0], nil
}

// GetVideoByObjectID finds a video by container ID and NeoFS object ID.
func (d *DB) GetVideoByObjectID(ctx context.Context, containerID int64, objectID string) (*Video, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT v.id, v.container_id, c.cid, v.object_id, v.filename, COALESCE(v.title,''),
		        v.duration_sec, v.size_bytes, COALESCE(v.content_type,''), COALESCE(v.thumbnail_oid,''),
		        v.transcode_status, COALESCE(v.transcode_oid,''), v.media_id, v.episode_id, v.indexed_at
		 FROM videos v JOIN containers c ON c.id=v.container_id
		 WHERE v.container_id=? AND v.object_id=?`, containerID, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	vs, err := scanVideos(rows)
	if err != nil || len(vs) == 0 {
		return nil, err
	}
	return &vs[0], nil
}

// ListVideos returns paginated videos, optionally filtered by search query.
func (d *DB) ListVideos(ctx context.Context, query string, limit, offset int) ([]Video, int, error) {
	var (
		rows *sql.Rows
		err  error
		total int
	)
	if query != "" {
		like := "%" + query + "%"
		err = d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM videos v JOIN containers c ON c.id=v.container_id
			 WHERE v.filename LIKE ? OR v.title LIKE ?`, like, like).Scan(&total)
		if err != nil {
			return nil, 0, err
		}
		rows, err = d.QueryContext(ctx,
			`SELECT v.id, v.container_id, c.cid, v.object_id, v.filename, COALESCE(v.title,''),
			        v.duration_sec, v.size_bytes, COALESCE(v.content_type,''), COALESCE(v.thumbnail_oid,''),
			        v.transcode_status, COALESCE(v.transcode_oid,''), v.media_id, v.episode_id, v.indexed_at
			 FROM videos v JOIN containers c ON c.id=v.container_id
			 WHERE v.filename LIKE ? OR v.title LIKE ?
			 ORDER BY v.indexed_at DESC LIMIT ? OFFSET ?`, like, like, limit, offset)
	} else {
		err = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM videos`).Scan(&total)
		if err != nil {
			return nil, 0, err
		}
		rows, err = d.QueryContext(ctx,
			`SELECT v.id, v.container_id, c.cid, v.object_id, v.filename, COALESCE(v.title,''),
			        v.duration_sec, v.size_bytes, COALESCE(v.content_type,''), COALESCE(v.thumbnail_oid,''),
			        v.transcode_status, COALESCE(v.transcode_oid,''), v.media_id, v.episode_id, v.indexed_at
			 FROM videos v JOIN containers c ON c.id=v.container_id
			 ORDER BY v.indexed_at DESC LIMIT ? OFFSET ?`, limit, offset)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	vs, err := scanVideos(rows)
	return vs, total, err
}

// SetTranscodeStatus updates a video's transcode status and optional OID.
func (d *DB) SetTranscodeStatus(ctx context.Context, id int64, status, oid string) error {
	_, err := d.ExecContext(ctx,
		`UPDATE videos SET transcode_status=?, transcode_oid=? WHERE id=?`,
		status, nullString(oid), id)
	return err
}

// DeleteVideo removes a video record.
func (d *DB) DeleteVideo(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM videos WHERE id=?`, id)
	return err
}

func scanVideos(rows *sql.Rows) ([]Video, error) {
	var out []Video
	for rows.Next() {
		var v Video
		var dur sql.NullFloat64
		var size, mediaID, episodeID sql.NullInt64
		if err := rows.Scan(
			&v.ID, &v.ContainerID, &v.ContainerCID, &v.ObjectID,
			&v.Filename, &v.Title, &dur, &size,
			&v.ContentType, &v.ThumbnailOID,
			&v.TranscodeStatus, &v.TranscodeOID,
			&mediaID, &episodeID, &v.IndexedAt,
		); err != nil {
			return nil, err
		}
		if dur.Valid {
			v.DurationSec = &dur.Float64
		}
		if size.Valid {
			v.SizeBytes = &size.Int64
		}
		if mediaID.Valid {
			v.MediaID = &mediaID.Int64
		}
		if episodeID.Valid {
			v.EpisodeID = &episodeID.Int64
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ScanEnabledContainers returns all containers eligible for scanning.
func (d *DB) ScanEnabledContainers(ctx context.Context) ([]Container, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, cid, name, scan_enabled, last_scanned_at, created_at
		 FROM containers WHERE scan_enabled=TRUE ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanContainers(rows)
}

// VideoExists reports whether a video with the given container+object already exists.
func (d *DB) VideoExists(ctx context.Context, containerID int64, objectID string) (bool, error) {
	var n int
	err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM videos WHERE container_id=? AND object_id=?`, containerID, objectID).Scan(&n)
	return n > 0, err
}

// ErrNotFound is returned when a record doesn't exist.
var ErrNotFound = errors.New("db: not found")
