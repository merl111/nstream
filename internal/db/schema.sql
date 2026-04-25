PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY,
  username      TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'viewer',
  created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
  token      TEXT PRIMARY KEY,
  user_id    INTEGER REFERENCES users(id) ON DELETE CASCADE,
  expires_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS containers (
  id              INTEGER PRIMARY KEY,
  cid             TEXT UNIQUE NOT NULL,
  name            TEXT NOT NULL,
  scan_enabled    BOOLEAN DEFAULT TRUE,
  last_scanned_at DATETIME,
  created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS videos (
  id               INTEGER PRIMARY KEY,
  container_id     INTEGER REFERENCES containers(id) ON DELETE CASCADE,
  object_id        TEXT NOT NULL,
  filename         TEXT NOT NULL,
  title            TEXT,
  duration_sec     REAL,
  size_bytes       INTEGER,
  content_type     TEXT,
  thumbnail_oid    TEXT,
  transcode_status TEXT NOT NULL DEFAULT 'none',
  transcode_oid    TEXT,
  media_id         INTEGER REFERENCES media_items(id),
  episode_id       INTEGER REFERENCES tv_episodes(id),
  indexed_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(container_id, object_id)
);

CREATE TABLE IF NOT EXISTS transcode_jobs (
  id          INTEGER PRIMARY KEY,
  video_id    INTEGER REFERENCES videos(id) ON DELETE CASCADE,
  status      TEXT NOT NULL DEFAULT 'pending',
  profile     TEXT NOT NULL DEFAULT 'hls-h264',
  error       TEXT,
  started_at  DATETIME,
  finished_at DATETIME,
  created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Media metadata (movies + TV shows) fetched from TMDB or entered manually.
CREATE TABLE IF NOT EXISTS media_items (
  id             INTEGER PRIMARY KEY,
  tmdb_id        INTEGER,
  imdb_id        TEXT,
  media_type     TEXT NOT NULL DEFAULT 'movie',  -- 'movie' | 'tv'
  title          TEXT NOT NULL,
  original_title TEXT,
  overview       TEXT,
  tagline        TEXT,
  release_date   TEXT,  -- YYYY-MM-DD or YYYY
  status         TEXT,
  poster_url     TEXT,  -- full TMDB poster URL
  backdrop_url   TEXT,
  vote_average   REAL,
  vote_count     INTEGER,
  runtime        INTEGER,  -- minutes (movies) or avg episode runtime (tv)
  language          TEXT,
  metadata_language TEXT NOT NULL DEFAULT 'en-US',
  created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(tmdb_id, media_type)
);

CREATE TABLE IF NOT EXISTS genres (
  id      INTEGER PRIMARY KEY,
  tmdb_id INTEGER UNIQUE,
  name    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS media_genres (
  media_id INTEGER NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
  genre_id INTEGER NOT NULL REFERENCES genres(id) ON DELETE CASCADE,
  PRIMARY KEY (media_id, genre_id)
);

CREATE TABLE IF NOT EXISTS tv_seasons (
  id             INTEGER PRIMARY KEY,
  media_id       INTEGER NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
  season_number  INTEGER NOT NULL,
  name           TEXT,
  overview       TEXT,
  poster_url     TEXT,
  air_date       TEXT,
  episode_count  INTEGER,
  UNIQUE(media_id, season_number)
);

CREATE TABLE IF NOT EXISTS tv_episodes (
  id             INTEGER PRIMARY KEY,
  season_id      INTEGER NOT NULL REFERENCES tv_seasons(id) ON DELETE CASCADE,
  episode_number INTEGER NOT NULL,
  name           TEXT,
  overview       TEXT,
  still_url      TEXT,
  air_date       TEXT,
  runtime        INTEGER,
  vote_average   REAL,
  UNIQUE(season_id, episode_number)
);

CREATE INDEX IF NOT EXISTS idx_sessions_user    ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_videos_container ON videos(container_id);
CREATE INDEX IF NOT EXISTS idx_videos_filename  ON videos(filename);
CREATE INDEX IF NOT EXISTS idx_jobs_video       ON transcode_jobs(video_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status      ON transcode_jobs(status);
CREATE INDEX IF NOT EXISTS idx_media_type       ON media_items(media_type);
CREATE INDEX IF NOT EXISTS idx_media_tmdb       ON media_items(tmdb_id);
