package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MediaItem is a movie or TV show entry in the library.
type MediaItem struct {
	ID            int64
	TmdbID        *int
	ImdbID        string
	MediaType     string // "movie" | "tv"
	Title         string
	OriginalTitle string
	Overview      string
	Tagline       string
	ReleaseDate   string
	Status        string
	PosterURL     string
	BackdropURL   string
	VoteAverage   *float64
	VoteCount     *int
	Runtime       *int
	Language         string
	MetadataLanguage string // e.g. "en-US", "de-DE"
	Genres           []Genre
	Seasons          []Season // TV only, populated by GetMediaItemDetail
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Genre is a content classification tag.
type Genre struct {
	ID     int64  `json:"id"`
	TmdbID *int   `json:"tmdb_id"`
	Name   string `json:"name"`
}

// Season is a TV season.
type Season struct {
	ID           int64
	MediaID      int64
	SeasonNumber int
	Name         string
	Overview     string
	PosterURL    string
	AirDate      string
	EpisodeCount int
	Episodes     []Episode // populated by GetSeasonDetail
}

// Episode is a TV episode.
type Episode struct {
	ID            int64
	SeasonID      int64
	EpisodeNumber int
	Name          string
	Overview      string
	StillURL      string
	AirDate       string
	Runtime       *int
	VoteAverage   *float64
}

// -- Media items ----------------------------------------------------------

// UpsertMediaItem inserts or updates a media item by TMDB id + type.
// If TmdbID is nil (manual entry), always inserts.
func (d *DB) UpsertMediaItem(ctx context.Context, m MediaItem) (*MediaItem, error) {
	var id int64
	var updatedAt sql.NullTime

	lang := m.MetadataLanguage
	if lang == "" {
		lang = "en-US"
	}

	if m.TmdbID != nil {
		err := d.QueryRowContext(ctx, `
			INSERT INTO media_items
			  (tmdb_id, imdb_id, media_type, title, original_title, overview, tagline,
			   release_date, status, poster_url, backdrop_url, vote_average, vote_count,
			   runtime, language, metadata_language, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?, datetime('now'))
			ON CONFLICT(tmdb_id, media_type) DO UPDATE SET
			  imdb_id           = excluded.imdb_id,
			  title             = excluded.title,
			  original_title    = excluded.original_title,
			  overview          = excluded.overview,
			  tagline           = excluded.tagline,
			  release_date      = excluded.release_date,
			  status            = excluded.status,
			  poster_url        = excluded.poster_url,
			  backdrop_url      = excluded.backdrop_url,
			  vote_average      = excluded.vote_average,
			  vote_count        = excluded.vote_count,
			  runtime           = excluded.runtime,
			  language          = excluded.language,
			  metadata_language = excluded.metadata_language,
			  updated_at        = datetime('now')
			RETURNING id, updated_at`,
			m.TmdbID, nullString(m.ImdbID), m.MediaType,
			m.Title, nullString(m.OriginalTitle), nullString(m.Overview), nullString(m.Tagline),
			nullString(m.ReleaseDate), nullString(m.Status), nullString(m.PosterURL), nullString(m.BackdropURL),
			m.VoteAverage, m.VoteCount, m.Runtime, nullString(m.Language), lang,
		).Scan(&id, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("upsert media item: %w", err)
		}
	} else {
		err := d.QueryRowContext(ctx, `
			INSERT INTO media_items
			  (imdb_id, media_type, title, original_title, overview, tagline,
			   release_date, status, poster_url, backdrop_url, vote_average, vote_count,
			   runtime, language, metadata_language)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			RETURNING id, updated_at`,
			nullString(m.ImdbID), m.MediaType,
			m.Title, nullString(m.OriginalTitle), nullString(m.Overview), nullString(m.Tagline),
			nullString(m.ReleaseDate), nullString(m.Status), nullString(m.PosterURL), nullString(m.BackdropURL),
			m.VoteAverage, m.VoteCount, m.Runtime, nullString(m.Language), lang,
		).Scan(&id, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("insert media item: %w", err)
		}
	}

	m.ID = id
	if updatedAt.Valid {
		m.UpdatedAt = updatedAt.Time
	}
	return &m, nil
}

// GetMediaItem fetches a media item by primary key (no genres/seasons loaded).
func (d *DB) GetMediaItem(ctx context.Context, id int64) (*MediaItem, error) {
	items, err := d.queryMediaItems(ctx,
		`WHERE m.id=?`, id)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

// GetMediaItemDetail fetches a media item with genres and (for TV) seasons+episodes.
func (d *DB) GetMediaItemDetail(ctx context.Context, id int64) (*MediaItem, error) {
	item, err := d.GetMediaItem(ctx, id)
	if err != nil || item == nil {
		return nil, err
	}
	if err := d.loadGenres(ctx, item); err != nil {
		return nil, err
	}
	if item.MediaType == "tv" {
		if err := d.loadSeasons(ctx, item, true); err != nil {
			return nil, err
		}
	}
	return item, nil
}

// ListMediaItems returns paginated media items, optionally filtered.
func (d *DB) ListMediaItems(ctx context.Context, mediaType, query string, genreID *int64, limit, offset int) ([]MediaItem, int, error) {
	where := "WHERE 1=1"
	args := []any{}

	if mediaType != "" && mediaType != "all" {
		where += " AND m.media_type=?"
		args = append(args, mediaType)
	}
	if query != "" {
		where += " AND (m.title LIKE ? OR m.original_title LIKE ?)"
		like := "%" + query + "%"
		args = append(args, like, like)
	}
	if genreID != nil {
		where += " AND EXISTS (SELECT 1 FROM media_genres mg WHERE mg.media_id=m.id AND mg.genre_id=?)"
		args = append(args, *genreID)
	}

	var total int
	countArgs := append([]any{}, args...)
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_items m `+where, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	listArgs := append(args, limit, offset)
	items, err := d.queryMediaItems(ctx,
		where+` ORDER BY m.title LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, 0, err
	}

	// Load genres for all items.
	for i := range items {
		_ = d.loadGenres(ctx, &items[i])
	}
	return items, total, nil
}

// DeleteMediaItem removes a media item (cascade clears genres, seasons, episodes).
func (d *DB) DeleteMediaItem(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM media_items WHERE id=?`, id)
	return err
}

// LinkVideoToMedia sets the media_id (and optionally episode_id) on a video.
func (d *DB) LinkVideoToMedia(ctx context.Context, videoID, mediaID int64, episodeID *int64) error {
	_, err := d.ExecContext(ctx,
		`UPDATE videos SET media_id=?, episode_id=? WHERE id=?`,
		mediaID, episodeID, videoID)
	return err
}

// UnlinkVideoFromMedia clears the media_id and episode_id on a video.
func (d *DB) UnlinkVideoFromMedia(ctx context.Context, videoID int64) error {
	_, err := d.ExecContext(ctx,
		`UPDATE videos SET media_id=NULL, episode_id=NULL WHERE id=?`, videoID)
	return err
}

// VideosForMedia returns all videos linked to a media item, optionally filtered by episode.
func (d *DB) VideosForMedia(ctx context.Context, mediaID int64) ([]Video, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT v.id, v.container_id, c.cid, v.object_id, v.filename, COALESCE(v.title,''),
		       v.duration_sec, v.size_bytes, COALESCE(v.content_type,''), COALESCE(v.thumbnail_oid,''),
		       v.transcode_status, COALESCE(v.transcode_oid,''), v.media_id, v.episode_id, v.indexed_at
		FROM videos v JOIN containers c ON c.id=v.container_id
		WHERE v.media_id=?
		ORDER BY v.episode_id, v.indexed_at`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVideos(rows)
}

// UnlinkedVideos returns videos not yet linked to any media item.
func (d *DB) UnlinkedVideos(ctx context.Context, limit, offset int) ([]Video, int, error) {
	var total int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM videos WHERE media_id IS NULL`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := d.QueryContext(ctx, `
		SELECT v.id, v.container_id, c.cid, v.object_id, v.filename, COALESCE(v.title,''),
		       v.duration_sec, v.size_bytes, COALESCE(v.content_type,''), COALESCE(v.thumbnail_oid,''),
		       v.transcode_status, COALESCE(v.transcode_oid,''), v.media_id, v.episode_id, v.indexed_at
		FROM videos v JOIN containers c ON c.id=v.container_id
		WHERE v.media_id IS NULL
		ORDER BY v.indexed_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	vs, err := scanVideos(rows)
	return vs, total, err
}

// -- Genres ---------------------------------------------------------------

// UpsertGenre inserts or returns an existing genre.
func (d *DB) UpsertGenre(ctx context.Context, tmdbID *int, name string) (int64, error) {
	var id int64
	if tmdbID != nil {
		err := d.QueryRowContext(ctx,
			`INSERT INTO genres(tmdb_id, name) VALUES(?,?)
			 ON CONFLICT(tmdb_id) DO UPDATE SET name=excluded.name
			 RETURNING id`, tmdbID, name).Scan(&id)
		return id, err
	}
	err := d.QueryRowContext(ctx,
		`INSERT INTO genres(name) VALUES(?) RETURNING id`, name).Scan(&id)
	return id, err
}

// SetMediaGenres replaces all genres for a media item.
func (d *DB) SetMediaGenres(ctx context.Context, mediaID int64, genreIDs []int64) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM media_genres WHERE media_id=?`, mediaID); err != nil {
		return err
	}
	for _, gid := range genreIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO media_genres(media_id, genre_id) VALUES(?,?)`,
			mediaID, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListGenres returns all genres that have at least one media item.
func (d *DB) ListGenres(ctx context.Context) ([]Genre, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT g.id, g.tmdb_id, g.name
		FROM genres g
		WHERE EXISTS (SELECT 1 FROM media_genres mg WHERE mg.genre_id=g.id)
		ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGenres(rows)
}

// -- Seasons & Episodes ---------------------------------------------------

// UpsertSeason inserts or updates a TV season.
func (d *DB) UpsertSeason(ctx context.Context, s Season) (int64, error) {
	var id int64
	err := d.QueryRowContext(ctx, `
		INSERT INTO tv_seasons(media_id, season_number, name, overview, poster_url, air_date, episode_count)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(media_id, season_number) DO UPDATE SET
		  name=excluded.name, overview=excluded.overview, poster_url=excluded.poster_url,
		  air_date=excluded.air_date, episode_count=excluded.episode_count
		RETURNING id`,
		s.MediaID, s.SeasonNumber, nullString(s.Name), nullString(s.Overview),
		nullString(s.PosterURL), nullString(s.AirDate), s.EpisodeCount,
	).Scan(&id)
	return id, err
}

// UpsertEpisode inserts or updates a TV episode.
func (d *DB) UpsertEpisode(ctx context.Context, e Episode) (int64, error) {
	var id int64
	err := d.QueryRowContext(ctx, `
		INSERT INTO tv_episodes(season_id, episode_number, name, overview, still_url, air_date, runtime, vote_average)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(season_id, episode_number) DO UPDATE SET
		  name=excluded.name, overview=excluded.overview, still_url=excluded.still_url,
		  air_date=excluded.air_date, runtime=excluded.runtime, vote_average=excluded.vote_average
		RETURNING id`,
		e.SeasonID, e.EpisodeNumber, nullString(e.Name), nullString(e.Overview),
		nullString(e.StillURL), nullString(e.AirDate), e.Runtime, e.VoteAverage,
	).Scan(&id)
	return id, err
}

// GetEpisode fetches a single episode by season+number.
func (d *DB) GetEpisode(ctx context.Context, seasonID int64, episodeNumber int) (*Episode, error) {
	var e Episode
	var rt sql.NullInt64
	var va sql.NullFloat64
	err := d.QueryRowContext(ctx, `
		SELECT id, season_id, episode_number, COALESCE(name,''), COALESCE(overview,''),
		       COALESCE(still_url,''), COALESCE(air_date,''), runtime, vote_average
		FROM tv_episodes WHERE season_id=? AND episode_number=?`,
		seasonID, episodeNumber).Scan(
		&e.ID, &e.SeasonID, &e.EpisodeNumber, &e.Name, &e.Overview,
		&e.StillURL, &e.AirDate, &rt, &va)
	if err != nil {
		return nil, err
	}
	if rt.Valid {
		v := int(rt.Int64)
		e.Runtime = &v
	}
	if va.Valid {
		e.VoteAverage = &va.Float64
	}
	return &e, nil
}

// GetEpisodeByNumber fetches an episode by media ID + season number + episode number.
func (d *DB) GetEpisodeByNumber(ctx context.Context, mediaID int64, seasonNumber, episodeNumber int) (*Episode, error) {
	season, err := d.GetSeasonByNumber(ctx, mediaID, seasonNumber)
	if err != nil {
		return nil, err
	}
	return d.GetEpisode(ctx, season.ID, episodeNumber)
}

// GetSeasonByNumber fetches a season by media ID + number.
func (d *DB) GetSeasonByNumber(ctx context.Context, mediaID int64, seasonNumber int) (*Season, error) {
	var s Season
	err := d.QueryRowContext(ctx, `
		SELECT id, media_id, season_number, COALESCE(name,''), COALESCE(overview,''),
		       COALESCE(poster_url,''), COALESCE(air_date,''), episode_count
		FROM tv_seasons WHERE media_id=? AND season_number=?`,
		mediaID, seasonNumber).Scan(
		&s.ID, &s.MediaID, &s.SeasonNumber, &s.Name, &s.Overview,
		&s.PosterURL, &s.AirDate, &s.EpisodeCount)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// -- Internal helpers -----------------------------------------------------

func (d *DB) queryMediaItems(ctx context.Context, suffix string, args ...any) ([]MediaItem, error) {
	q := `SELECT m.id, m.tmdb_id, COALESCE(m.imdb_id,''), m.media_type,
		         m.title, COALESCE(m.original_title,''), COALESCE(m.overview,''), COALESCE(m.tagline,''),
		         COALESCE(m.release_date,''), COALESCE(m.status,''),
		         COALESCE(m.poster_url,''), COALESCE(m.backdrop_url,''),
		         m.vote_average, m.vote_count, m.runtime, COALESCE(m.language,''),
		         COALESCE(m.metadata_language,'en-US')
		  FROM media_items m ` + suffix
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MediaItem
	for rows.Next() {
		var m MediaItem
		var tmdbID sql.NullInt64
		var va sql.NullFloat64
		var vc, rt sql.NullInt64
		if err := rows.Scan(
			&m.ID, &tmdbID, &m.ImdbID, &m.MediaType,
			&m.Title, &m.OriginalTitle, &m.Overview, &m.Tagline,
			&m.ReleaseDate, &m.Status,
			&m.PosterURL, &m.BackdropURL,
			&va, &vc, &rt, &m.Language,
			&m.MetadataLanguage,
		); err != nil {
			return nil, err
		}
		if tmdbID.Valid {
			v := int(tmdbID.Int64)
			m.TmdbID = &v
		}
		if va.Valid {
			m.VoteAverage = &va.Float64
		}
		if vc.Valid {
			v := int(vc.Int64)
			m.VoteCount = &v
		}
		if rt.Valid {
			v := int(rt.Int64)
			m.Runtime = &v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) loadGenres(ctx context.Context, m *MediaItem) error {
	rows, err := d.QueryContext(ctx, `
		SELECT g.id, g.tmdb_id, g.name
		FROM genres g JOIN media_genres mg ON mg.genre_id=g.id
		WHERE mg.media_id=? ORDER BY g.name`, m.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	gs, err := scanGenres(rows)
	if err != nil {
		return err
	}
	m.Genres = gs
	return nil
}

func (d *DB) loadSeasons(ctx context.Context, m *MediaItem, withEpisodes bool) error {
	rows, err := d.QueryContext(ctx, `
		SELECT id, media_id, season_number, COALESCE(name,''), COALESCE(overview,''),
		       COALESCE(poster_url,''), COALESCE(air_date,''), episode_count
		FROM tv_seasons WHERE media_id=? ORDER BY season_number`, m.ID)
	if err != nil {
		return err
	}

	// Collect all seasons into memory before closing the cursor.
	// This is critical because SQLite uses a single connection (SetMaxOpenConns(1)),
	// and nested QueryContext calls while a rows cursor is open cause a deadlock.
	var seasons []Season
	for rows.Next() {
		var s Season
		if err := rows.Scan(&s.ID, &s.MediaID, &s.SeasonNumber, &s.Name, &s.Overview,
			&s.PosterURL, &s.AirDate, &s.EpisodeCount); err != nil {
			rows.Close()
			return err
		}
		seasons = append(seasons, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close() // release the connection before issuing episode queries

	for i := range seasons {
		if withEpisodes {
			eps, err := d.loadEpisodes(ctx, seasons[i].ID)
			if err != nil {
				return err
			}
			seasons[i].Episodes = eps
		}
		m.Seasons = append(m.Seasons, seasons[i])
	}
	return nil
}

func (d *DB) loadEpisodes(ctx context.Context, seasonID int64) ([]Episode, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT id, season_id, episode_number, COALESCE(name,''), COALESCE(overview,''),
		       COALESCE(still_url,''), COALESCE(air_date,''), runtime, vote_average
		FROM tv_episodes WHERE season_id=? ORDER BY episode_number`, seasonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Episode
	for rows.Next() {
		var e Episode
		var rt sql.NullInt64
		var va sql.NullFloat64
		if err := rows.Scan(&e.ID, &e.SeasonID, &e.EpisodeNumber, &e.Name, &e.Overview,
			&e.StillURL, &e.AirDate, &rt, &va); err != nil {
			return nil, err
		}
		if rt.Valid {
			v := int(rt.Int64)
			e.Runtime = &v
		}
		if va.Valid {
			e.VoteAverage = &va.Float64
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanGenres(rows *sql.Rows) ([]Genre, error) {
	var out []Genre
	for rows.Next() {
		var g Genre
		var tmdbID sql.NullInt64
		if err := rows.Scan(&g.ID, &tmdbID, &g.Name); err != nil {
			return nil, err
		}
		if tmdbID.Valid {
			v := int(tmdbID.Int64)
			g.TmdbID = &v
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
