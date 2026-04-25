package api

import (
	"context"
	"net/http"
	"strconv"

	"nstream/internal/db"
	"nstream/internal/tmdb"
)

// ---- TMDB search/fetch --------------------------------------------------

func (a *API) handleTMDBSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if a.tmdb == nil || !a.tmdb.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "TMDB API key not configured (set NSTREAM_TMDB_KEY)")
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	mediaType := r.URL.Query().Get("type") // "movie" | "tv" | "" = multi
	year := r.URL.Query().Get("year")
	lang := r.URL.Query().Get("lang") // e.g. "de-DE"

	var results []tmdb.SearchResult
	var err error
	switch mediaType {
	case "movie":
		results, err = a.tmdb.SearchMovie(r.Context(), q, year, lang)
	case "tv":
		results, err = a.tmdb.SearchTV(r.Context(), q, year, lang)
	default:
		results, err = a.tmdb.SearchMulti(r.Context(), q, lang)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "tmdb: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (a *API) handleTMDBGetMovie(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if a.tmdb == nil || !a.tmdb.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "TMDB API key not configured")
		return
	}
	idStr := pathSegment(r.URL.Path, "/api/v1/tmdb/movie/")
	var id int64
	if _, err := parseID(idStr, &id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid tmdb id")
		return
	}
	lang := r.URL.Query().Get("lang")
	m, err := a.tmdb.GetMovie(r.Context(), int(id), lang)
	if err != nil {
		writeError(w, http.StatusBadGateway, "tmdb: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (a *API) handleTMDBGetTV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if a.tmdb == nil || !a.tmdb.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "TMDB API key not configured")
		return
	}
	idStr := pathSegment(r.URL.Path, "/api/v1/tmdb/tv/")
	var id int64
	if _, err := parseID(idStr, &id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid tmdb id")
		return
	}
	lang := r.URL.Query().Get("lang")
	tv, err := a.tmdb.GetTV(r.Context(), int(id), lang)
	if err != nil {
		writeError(w, http.StatusBadGateway, "tmdb: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tv)
}

// ---- Media items --------------------------------------------------------

// handleImportMedia imports a movie or TV show from TMDB into the DB, storing
// all metadata (genres, seasons, episodes for TV).
// POST /api/v1/media/import  body: { tmdb_id, media_type }
func (a *API) handleImportMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if a.tmdb == nil || !a.tmdb.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "TMDB API key not configured")
		return
	}
	var req struct {
		TmdbID    int    `json:"tmdb_id"`
		MediaType string `json:"media_type"`
		Language  string `json:"language"` // e.g. "de-DE", defaults to "en-US"
	}
	if err := readJSON(r, &req); err != nil || req.TmdbID == 0 {
		writeError(w, http.StatusBadRequest, "tmdb_id and media_type required")
		return
	}
	if req.MediaType != "movie" && req.MediaType != "tv" {
		writeError(w, http.StatusBadRequest, "media_type must be 'movie' or 'tv'")
		return
	}
	if req.Language == "" {
		req.Language = "en-US"
	}

	item, err := a.importFromTMDB(r.Context(), req.TmdbID, req.MediaType, req.Language)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toMediaJSON(*item))
}

// handleReimportMedia re-fetches all metadata from TMDB in the specified language.
// POST /api/v1/media/{id}/reimport   body: { language }
func (a *API) handleReimportMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if a.tmdb == nil || !a.tmdb.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "TMDB API key not configured")
		return
	}
	idStr := pathSegment(r.URL.Path, "/api/v1/media/")
	if len(idStr) > 9 && idStr[len(idStr)-9:] == "/reimport" {
		idStr = idStr[:len(idStr)-9]
	}
	var id int64
	if _, err := parseID(idStr, &id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := a.db.GetMediaItem(r.Context(), id)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if existing.TmdbID == nil {
		writeError(w, http.StatusBadRequest, "media item has no TMDB id (manually created)")
		return
	}
	var req struct {
		Language string `json:"language"`
	}
	_ = readJSON(r, &req)
	if req.Language == "" {
		req.Language = existing.MetadataLanguage
	}
	if req.Language == "" {
		req.Language = "en-US"
	}

	item, err := a.importFromTMDB(r.Context(), *existing.TmdbID, existing.MediaType, req.Language)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toMediaJSON(*item))
}

// handleAutoMatchVideos tries to link unlinked video files to episodes of a TV show
// based on SxxExx patterns in their filenames.
// POST /api/v1/media/{id}/automatch
func (a *API) handleAutoMatchVideos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	idStr := pathSegment(r.URL.Path, "/api/v1/media/")
	if len(idStr) > 10 && idStr[len(idStr)-10:] == "/automatch" {
		idStr = idStr[:len(idStr)-10]
	}
	var mediaID int64
	if _, err := parseID(idStr, &mediaID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	item, err := a.db.GetMediaItemDetail(r.Context(), mediaID)
	if err != nil || item == nil || item.MediaType != "tv" {
		writeError(w, http.StatusBadRequest, "tv media item required")
		return
	}

	// Get unlinked videos.
	unlinked, _, err := a.db.UnlinkedVideos(r.Context(), 500, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Build episode lookup: (season, episode) → episode DB id.
	type epKey struct{ S, E int }
	epMap := map[epKey]int64{}
	for _, season := range item.Seasons {
		for _, ep := range season.Episodes {
			epMap[epKey{season.SeasonNumber, ep.EpisodeNumber}] = ep.ID
		}
	}

	type matchResult struct {
		VideoID   int64  `json:"video_id"`
		Filename  string `json:"filename"`
		Season    int    `json:"season"`
		Episode   int    `json:"episode"`
		EpisodeID int64  `json:"episode_id"`
	}
	var matched []matchResult
	var skipped []string

	for _, v := range unlinked {
		parsed := parseTVFilename(v.Filename)
		if !parsed.IsTV {
			skipped = append(skipped, v.Filename)
			continue
		}
		epID, ok := epMap[epKey{parsed.Season, parsed.Episode}]
		if !ok {
			skipped = append(skipped, v.Filename)
			continue
		}
		if err := a.db.LinkVideoToMedia(r.Context(), v.ID, mediaID, &epID); err == nil {
			matched = append(matched, matchResult{
				VideoID:   v.ID,
				Filename:  v.Filename,
				Season:    parsed.Season,
				Episode:   parsed.Episode,
				EpisodeID: epID,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"matched": matched,
		"skipped": skipped,
	})
}

// handleListLanguages returns the list of supported TMDB metadata languages.
// GET /api/v1/tmdb/languages
func (a *API) handleListLanguages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, tmdb.SupportedLanguages)
}

// importFromTMDB fetches full details from TMDB and stores them in the DB.
func (a *API) importFromTMDB(ctx context.Context, tmdbID int, mediaType, lang string) (*db.MediaItem, error) {
	if lang == "" {
		lang = "en-US"
	}
	var item db.MediaItem
	item.MediaType = mediaType
	item.MetadataLanguage = lang

	if mediaType == "movie" {
		m, err := a.tmdb.GetMovie(ctx, tmdbID, lang)
		if err != nil {
			return nil, err
		}
		item.TmdbID = &tmdbID
		item.ImdbID = m.IMDBId
		item.Title = m.Title
		item.OriginalTitle = m.OriginalTitle
		item.Overview = m.Overview
		item.Tagline = m.Tagline
		item.ReleaseDate = m.ReleaseDate
		item.Status = m.Status
		item.PosterURL = tmdb.PosterURL(m.PosterPath)
		item.BackdropURL = tmdb.BackdropURL(m.BackdropPath)
		item.VoteAverage = &m.VoteAverage
		item.VoteCount = &m.VoteCount
		item.Runtime = &m.Runtime
		item.Language = m.OriginalLanguage

		saved, err := a.db.UpsertMediaItem(ctx, item)
		if err != nil {
			return nil, err
		}
		// Genres.
		genreIDs := make([]int64, 0, len(m.Genres))
		for _, g := range m.Genres {
			tid := g.ID
			gid, err := a.db.UpsertGenre(ctx, &tid, g.Name)
			if err == nil {
				genreIDs = append(genreIDs, gid)
			}
		}
		_ = a.db.SetMediaGenres(ctx, saved.ID, genreIDs)
		return a.db.GetMediaItemDetail(ctx, saved.ID)

	} else { // tv
		tv, err := a.tmdb.GetTV(ctx, tmdbID, lang)
		if err != nil {
			return nil, err
		}
		imdbID := ""
		if tv.ExternalIDs != nil {
			imdbID = tv.ExternalIDs.IMDBId
		}
		rt := tv.AvgRuntime()
		item.TmdbID = &tmdbID
		item.ImdbID = imdbID
		item.Title = tv.Name
		item.OriginalTitle = tv.OriginalName
		item.Overview = tv.Overview
		item.Tagline = tv.Tagline
		item.ReleaseDate = tv.FirstAirDate
		item.Status = tv.Status
		item.PosterURL = tmdb.PosterURL(tv.PosterPath)
		item.BackdropURL = tmdb.BackdropURL(tv.BackdropPath)
		item.VoteAverage = &tv.VoteAverage
		item.VoteCount = &tv.VoteCount
		item.Runtime = &rt
		item.Language = tv.OriginalLanguage

		saved, err := a.db.UpsertMediaItem(ctx, item)
		if err != nil {
			return nil, err
		}
		// Genres.
		genreIDs := make([]int64, 0, len(tv.Genres))
		for _, g := range tv.Genres {
			tid := g.ID
			gid, err := a.db.UpsertGenre(ctx, &tid, g.Name)
			if err == nil {
				genreIDs = append(genreIDs, gid)
			}
		}
		_ = a.db.SetMediaGenres(ctx, saved.ID, genreIDs)

		// Seasons + episodes (skip season 0 = specials unless it has episodes).
		for _, s := range tv.Seasons {
			if s.SeasonNumber == 0 {
				continue
			}
			seasonID, err := a.db.UpsertSeason(ctx, db.Season{
				MediaID:      saved.ID,
				SeasonNumber: s.SeasonNumber,
				Name:         s.Name,
				Overview:     s.Overview,
				PosterURL:    tmdb.PosterURL(s.PosterPath),
				AirDate:      s.AirDate,
				EpisodeCount: s.EpisodeCount,
			})
			if err != nil {
				continue
			}
			// Fetch full season to get episodes.
			full, err := a.tmdb.GetTVSeason(ctx, tmdbID, s.SeasonNumber, lang)
			if err != nil {
				continue
			}
			for _, ep := range full.Episodes {
				rt := ep.Runtime
				va := ep.VoteAverage
				_, _ = a.db.UpsertEpisode(ctx, db.Episode{
					SeasonID:      seasonID,
					EpisodeNumber: ep.EpisodeNumber,
					Name:          ep.Name,
					Overview:      ep.Overview,
					StillURL:      tmdb.StillURL(ep.StillPath),
					AirDate:       ep.AirDate,
					Runtime:       &rt,
					VoteAverage:   &va,
				})
			}
		}
		return a.db.GetMediaItemDetail(ctx, saved.ID)
	}
}

// handleListMedia returns paginated media items.
// GET /api/v1/media?type=movie|tv|all&q=...&genre=123&page=1&limit=40
func (a *API) handleListMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	mediaType := r.URL.Query().Get("type")
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

	var genreID *int64
	if gs := r.URL.Query().Get("genre"); gs != "" {
		var gid int64
		if n, err := strconv.ParseInt(gs, 10, 64); err == nil {
			gid = n
			genreID = &gid
		}
	}

	items, total, err := a.db.ListMediaItems(r.Context(), mediaType, q, genreID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]mediaJSON, len(items))
	for i, m := range items {
		out[i] = toMediaJSON(m)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": total, "page": page, "limit": limit, "items": out,
	})
}

// handleGetMedia returns a single media item with full details.
// GET /api/v1/media/{id}
func (a *API) handleGetMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	idStr := pathSegment(r.URL.Path, "/api/v1/media/")
	var id int64
	if _, err := parseID(idStr, &id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	item, err := a.db.GetMediaItemDetail(r.Context(), id)
	if err != nil || item == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// Also load linked videos.
	videos, _ := a.db.VideosForMedia(r.Context(), id)
	type detail struct {
		mediaJSON
		Videos []videoJSON `json:"videos"`
	}
	writeJSON(w, http.StatusOK, detail{
		mediaJSON: toMediaJSON(*item),
		Videos:    toVideoJSONSlice(videos),
	})
}

// handleDeleteMedia removes a media item.
// DELETE /api/v1/media/{id}
func (a *API) handleDeleteMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "DELETE required")
		return
	}
	idStr := pathSegment(r.URL.Path, "/api/v1/media/")
	var id int64
	if _, err := parseID(idStr, &id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := a.db.DeleteMediaItem(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleLinkVideo links a video to a media item (and optionally a specific episode).
// POST /api/v1/videos/{id}/link  body: { media_id, episode_id? }
func (a *API) handleLinkVideo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	idStr := pathSegment(r.URL.Path, "/api/v1/videos/")
	// Strip /link suffix.
	if len(idStr) > 5 && idStr[len(idStr)-5:] == "/link" {
		idStr = idStr[:len(idStr)-5]
	}
	var videoID int64
	if _, err := parseID(idStr, &videoID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid video id")
		return
	}
	var req struct {
		MediaID   int64  `json:"media_id"`
		EpisodeID *int64 `json:"episode_id,omitempty"`
	}
	if err := readJSON(r, &req); err != nil || req.MediaID == 0 {
		writeError(w, http.StatusBadRequest, "media_id required")
		return
	}
	if err := a.db.LinkVideoToMedia(r.Context(), videoID, req.MediaID, req.EpisodeID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v, _ := a.db.GetVideoByID(r.Context(), videoID)
	if v == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, toVideoJSON(*v))
}

// handleUnlinkVideo clears the media link from a video.
// POST /api/v1/videos/{id}/unlink
func (a *API) handleUnlinkVideo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	idStr := pathSegment(r.URL.Path, "/api/v1/videos/")
	if len(idStr) > 8 && idStr[len(idStr)-8:] == "/unlink" {
		idStr = idStr[:len(idStr)-8]
	}
	var videoID int64
	if _, err := parseID(idStr, &videoID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid video id")
		return
	}
	if err := a.db.UnlinkVideoFromMedia(r.Context(), videoID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListGenres returns all genres that have at least one media item.
// GET /api/v1/genres
func (a *API) handleListGenres(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	genres, err := a.db.ListGenres(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if genres == nil {
		genres = []db.Genre{}
	}
	writeJSON(w, http.StatusOK, genres)
}

// handleUnlinkedVideos returns videos without a media item link.
// GET /api/v1/videos/unlinked
func (a *API) handleUnlinkedVideos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	limit := queryInt(r, "limit", 40)
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	vs, total, err := a.db.UnlinkedVideos(r.Context(), limit, (page-1)*limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": total, "page": page, "limit": limit,
		"videos": toVideoJSONSlice(vs),
	})
}

// parseTVFilename is a thin wrapper around tmdb.ParseFilename used by auto-match.
func parseTVFilename(filename string) tmdb.ParsedFilename {
	return tmdb.ParseFilename(filename)
}

// ---- JSON serialisation -------------------------------------------------

type mediaJSON struct {
	ID               int64        `json:"id"`
	TmdbID           *int         `json:"tmdb_id,omitempty"`
	ImdbID           string       `json:"imdb_id,omitempty"`
	MediaType        string       `json:"media_type"`
	Title            string       `json:"title"`
	OriginalTitle    string       `json:"original_title,omitempty"`
	Overview         string       `json:"overview,omitempty"`
	Tagline          string       `json:"tagline,omitempty"`
	ReleaseDate      string       `json:"release_date,omitempty"`
	Year             string       `json:"year,omitempty"`
	Status           string       `json:"status,omitempty"`
	PosterURL        string       `json:"poster_url,omitempty"`
	BackdropURL      string       `json:"backdrop_url,omitempty"`
	VoteAverage      *float64     `json:"vote_average,omitempty"`
	VoteCount        *int         `json:"vote_count,omitempty"`
	Runtime          *int         `json:"runtime,omitempty"`
	Language         string       `json:"language,omitempty"`
	MetadataLanguage string       `json:"metadata_language"`
	Genres           []db.Genre   `json:"genres"`
	Seasons          []seasonJSON `json:"seasons,omitempty"`
	IMDBUrl          string       `json:"imdb_url,omitempty"`
}

type seasonJSON struct {
	ID           int64         `json:"id"`
	SeasonNumber int           `json:"season_number"`
	Name         string        `json:"name"`
	Overview     string        `json:"overview,omitempty"`
	PosterURL    string        `json:"poster_url,omitempty"`
	AirDate      string        `json:"air_date,omitempty"`
	EpisodeCount int           `json:"episode_count"`
	Episodes     []episodeJSON `json:"episodes,omitempty"`
}

type episodeJSON struct {
	ID            int64    `json:"id"`
	EpisodeNumber int      `json:"episode_number"`
	Name          string   `json:"name"`
	Overview      string   `json:"overview,omitempty"`
	StillURL      string   `json:"still_url,omitempty"`
	AirDate       string   `json:"air_date,omitempty"`
	Runtime       *int     `json:"runtime,omitempty"`
	VoteAverage   *float64 `json:"vote_average,omitempty"`
}

func toMediaJSON(m db.MediaItem) mediaJSON {
	j := mediaJSON{
		ID:            m.ID,
		TmdbID:        m.TmdbID,
		ImdbID:        m.ImdbID,
		MediaType:     m.MediaType,
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		Overview:      m.Overview,
		Tagline:       m.Tagline,
		ReleaseDate:   m.ReleaseDate,
		Status:        m.Status,
		PosterURL:     m.PosterURL,
		BackdropURL:   m.BackdropURL,
		VoteAverage:      m.VoteAverage,
		VoteCount:        m.VoteCount,
		Runtime:          m.Runtime,
		Language:         m.Language,
		MetadataLanguage: m.MetadataLanguage,
		Genres:           m.Genres,
	}
	if j.Genres == nil {
		j.Genres = []db.Genre{}
	}
	if len(m.ReleaseDate) >= 4 {
		j.Year = m.ReleaseDate[:4]
	}
	if m.ImdbID != "" {
		j.IMDBUrl = "https://www.imdb.com/title/" + m.ImdbID
	}
	for _, s := range m.Seasons {
		sj := seasonJSON{
			ID:           s.ID,
			SeasonNumber: s.SeasonNumber,
			Name:         s.Name,
			Overview:     s.Overview,
			PosterURL:    s.PosterURL,
			AirDate:      s.AirDate,
			EpisodeCount: s.EpisodeCount,
		}
		for _, e := range s.Episodes {
			sj.Episodes = append(sj.Episodes, episodeJSON{
				ID:            e.ID,
				EpisodeNumber: e.EpisodeNumber,
				Name:          e.Name,
				Overview:      e.Overview,
				StillURL:      e.StillURL,
				AirDate:       e.AirDate,
				Runtime:       e.Runtime,
				VoteAverage:   e.VoteAverage,
			})
		}
		j.Seasons = append(j.Seasons, sj)
	}
	return j
}

func toVideoJSONSlice(vs []db.Video) []videoJSON {
	out := make([]videoJSON, len(vs))
	for i, v := range vs {
		out[i] = toVideoJSON(v)
	}
	return out
}
