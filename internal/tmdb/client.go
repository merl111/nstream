// Package tmdb provides a client for The Movie Database (TMDB) API v3.
// Get a free API key at https://www.themoviedb.org/settings/api
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	baseURL     = "https://api.themoviedb.org/3"
	imageBase   = "https://image.tmdb.org/t/p"
	PosterW500  = imageBase + "/w500"
	PosterW342  = imageBase + "/w342"
	BackdropW1280 = imageBase + "/w1280"
	StillW300   = imageBase + "/w300"
)

// Client talks to the TMDB v3 REST API.
type Client struct {
	apiKey string
	http   *http.Client
}

// New creates a Client. apiKey is your TMDB v3 API key.
func New(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

// SupportedLanguages is a curated list of TMDB locale codes (language-REGION).
// Full list: https://developer.themoviedb.org/docs/languages
var SupportedLanguages = []struct {
	Code    string
	English string
	Native  string
}{
	{"en-US", "English", "English"},
	{"de-DE", "German", "Deutsch"},
	{"fr-FR", "French", "Français"},
	{"es-ES", "Spanish", "Español"},
	{"it-IT", "Italian", "Italiano"},
	{"pt-BR", "Portuguese (Brazil)", "Português"},
	{"ja-JP", "Japanese", "日本語"},
	{"ko-KR", "Korean", "한국어"},
	{"zh-CN", "Chinese (Simplified)", "中文"},
	{"zh-TW", "Chinese (Traditional)", "繁體中文"},
	{"ru-RU", "Russian", "Русский"},
	{"ar-SA", "Arabic", "العربية"},
	{"pl-PL", "Polish", "Polski"},
	{"nl-NL", "Dutch", "Nederlands"},
	{"sv-SE", "Swedish", "Svenska"},
	{"nb-NO", "Norwegian", "Norsk"},
	{"da-DK", "Danish", "Dansk"},
	{"fi-FI", "Finnish", "Suomi"},
	{"tr-TR", "Turkish", "Türkçe"},
	{"uk-UA", "Ukrainian", "Українська"},
	{"cs-CZ", "Czech", "Čeština"},
	{"hu-HU", "Hungarian", "Magyar"},
}

// Enabled reports whether the client has an API key configured.
func (c *Client) Enabled() bool { return c.apiKey != "" }

// PosterURL returns the full URL for a TMDB poster path (e.g. "/abc.jpg").
func PosterURL(path string) string {
	if path == "" {
		return ""
	}
	return PosterW500 + path
}

// BackdropURL returns the full URL for a TMDB backdrop path.
func BackdropURL(path string) string {
	if path == "" {
		return ""
	}
	return BackdropW1280 + path
}

// StillURL returns the full URL for a TMDB episode still path.
func StillURL(path string) string {
	if path == "" {
		return ""
	}
	return StillW300 + path
}

// ---- Search types -------------------------------------------------------

// SearchResult is one item from a multi-media search.
type SearchResult struct {
	ID           int     `json:"id"`
	MediaType    string  `json:"media_type"` // "movie" | "tv" | "person"
	Title        string  `json:"title"`       // movies
	Name         string  `json:"name"`        // tv
	OriginalTitle string `json:"original_title"`
	OriginalName  string `json:"original_name"`
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"`  // movies YYYY-MM-DD
	FirstAirDate string  `json:"first_air_date"` // tv
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	VoteAverage  float64 `json:"vote_average"`
	VoteCount    int     `json:"vote_count"`
	Popularity   float64 `json:"popularity"`
}

// DisplayTitle returns title or name depending on media type.
func (r SearchResult) DisplayTitle() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

// DisplayDate returns release_date or first_air_date.
func (r SearchResult) DisplayDate() string {
	if r.ReleaseDate != "" {
		return r.ReleaseDate
	}
	return r.FirstAirDate
}

// Year returns just the 4-digit year string.
func (r SearchResult) Year() string {
	d := r.DisplayDate()
	if len(d) >= 4 {
		return d[:4]
	}
	return ""
}

// ---- Movie details -------------------------------------------------------

type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ProductionCompany struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Movie struct {
	ID               int       `json:"id"`
	IMDBId           string    `json:"imdb_id"`
	Title            string    `json:"title"`
	OriginalTitle    string    `json:"original_title"`
	Overview         string    `json:"overview"`
	Tagline          string    `json:"tagline"`
	ReleaseDate      string    `json:"release_date"`
	Status           string    `json:"status"`
	PosterPath       string    `json:"poster_path"`
	BackdropPath     string    `json:"backdrop_path"`
	VoteAverage      float64   `json:"vote_average"`
	VoteCount        int       `json:"vote_count"`
	Runtime          int       `json:"runtime"`
	OriginalLanguage string    `json:"original_language"`
	Genres           []Genre   `json:"genres"`
	Homepage         string    `json:"homepage"`
}

// ---- TV details ---------------------------------------------------------

type TVShow struct {
	ID               int      `json:"id"`
	Name             string   `json:"name"`
	OriginalName     string   `json:"original_name"`
	Overview         string   `json:"overview"`
	Tagline          string   `json:"tagline"`
	FirstAirDate     string   `json:"first_air_date"`
	LastAirDate      string   `json:"last_air_date"`
	Status           string   `json:"status"`
	PosterPath       string   `json:"poster_path"`
	BackdropPath     string   `json:"backdrop_path"`
	VoteAverage      float64  `json:"vote_average"`
	VoteCount        int      `json:"vote_count"`
	EpisodeRunTime   []int    `json:"episode_run_time"`
	OriginalLanguage string   `json:"original_language"`
	Genres           []Genre  `json:"genres"`
	NumberOfSeasons  int      `json:"number_of_seasons"`
	NumberOfEpisodes int      `json:"number_of_episodes"`
	Seasons          []Season `json:"seasons"`
	ExternalIDs      *ExternalIDs `json:"-"` // populated by GetTVExternalIDs
}

type Season struct {
	ID           int       `json:"id"`
	SeasonNumber int       `json:"season_number"`
	Name         string    `json:"name"`
	Overview     string    `json:"overview"`
	PosterPath   string    `json:"poster_path"`
	AirDate      string    `json:"air_date"`
	EpisodeCount int       `json:"episode_count"`
	Episodes     []Episode `json:"episodes,omitempty"`
}

type Episode struct {
	ID            int     `json:"id"`
	EpisodeNumber int     `json:"episode_number"`
	SeasonNumber  int     `json:"season_number"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	StillPath     string  `json:"still_path"`
	AirDate       string  `json:"air_date"`
	Runtime       int     `json:"runtime"`
	VoteAverage   float64 `json:"vote_average"`
}

type ExternalIDs struct {
	IMDBId      string `json:"imdb_id"`
	TVDBId      int    `json:"tvdb_id"`
	TwitterId   string `json:"twitter_id"`
	InstagramId string `json:"instagram_id"`
}

// ---- API methods --------------------------------------------------------

// SearchMulti searches movies and TV shows simultaneously.
func (c *Client) SearchMulti(ctx context.Context, query, lang string) ([]SearchResult, error) {
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.get(ctx, lang, "/search/multi", url.Values{"query": {query}}, &resp); err != nil {
		return nil, err
	}
	// Filter to movies and tv only.
	out := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.MediaType == "movie" || r.MediaType == "tv" {
			out = append(out, r)
		}
	}
	return out, nil
}

// SearchMovie searches movies only.
func (c *Client) SearchMovie(ctx context.Context, query, year, lang string) ([]SearchResult, error) {
	params := url.Values{"query": {query}}
	if year != "" {
		params.Set("year", year)
	}
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.get(ctx, lang, "/search/movie", params, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Results {
		resp.Results[i].MediaType = "movie"
	}
	return resp.Results, nil
}

// SearchTV searches TV shows only.
func (c *Client) SearchTV(ctx context.Context, query, year, lang string) ([]SearchResult, error) {
	params := url.Values{"query": {query}}
	if year != "" {
		params.Set("first_air_date_year", year)
	}
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.get(ctx, lang, "/search/tv", params, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Results {
		resp.Results[i].MediaType = "tv"
	}
	return resp.Results, nil
}

// GetMovie fetches full movie details including genres.
func (c *Client) GetMovie(ctx context.Context, tmdbID int, lang string) (*Movie, error) {
	var m Movie
	if err := c.get(ctx, lang, fmt.Sprintf("/movie/%d", tmdbID), nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// GetTV fetches full TV show details including genres and season list.
func (c *Client) GetTV(ctx context.Context, tmdbID int, lang string) (*TVShow, error) {
	var tv TVShow
	if err := c.get(ctx, lang, fmt.Sprintf("/tv/%d", tmdbID), nil, &tv); err != nil {
		return nil, err
	}
	// Fetch external IDs for IMDB link.
	var ext ExternalIDs
	if err := c.get(ctx, "", fmt.Sprintf("/tv/%d/external_ids", tmdbID), nil, &ext); err == nil {
		tv.ExternalIDs = &ext
	}
	return &tv, nil
}

// GetTVSeason fetches a full season including all episodes.
func (c *Client) GetTVSeason(ctx context.Context, tvID, seasonNumber int, lang string) (*Season, error) {
	var s Season
	if err := c.get(ctx, lang, fmt.Sprintf("/tv/%d/season/%d", tvID, seasonNumber), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// AvgRuntime returns the average episode runtime for a TV show (0 if unknown).
func (tv *TVShow) AvgRuntime() int {
	if len(tv.EpisodeRunTime) == 0 {
		return 0
	}
	sum := 0
	for _, r := range tv.EpisodeRunTime {
		sum += r
	}
	return sum / len(tv.EpisodeRunTime)
}

func (c *Client) get(ctx context.Context, lang, path string, params url.Values, dst any) error {
	u, _ := url.Parse(baseURL + path)
	q := u.Query()
	q.Set("api_key", c.apiKey)
	if lang == "" {
		lang = "en-US"
	}
	q.Set("language", lang)
	for k, vs := range params {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("tmdb: invalid API key")
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("tmdb: not found")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb: status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// ---- Filename auto-match ------------------------------------------------

// ParsedFilename holds the title and optional year/episode info extracted
// from a media filename.
type ParsedFilename struct {
	Title         string
	Year          string
	Season        int  // 0 = unknown
	Episode       int  // 0 = unknown
	IsTV          bool // true if SxxExx pattern found
}

// ParseFilename attempts to extract title, year, and episode info from a
// filename like "The.Matrix.1999.1080p.BluRay.mkv" or "Breaking.Bad.S03E04.mkv".
func ParseFilename(filename string) ParsedFilename {
	// Strip extension.
	name := filename
	if idx := strings.LastIndexByte(name, '.'); idx > 0 {
		ext := strings.ToLower(name[idx:])
		switch ext {
		case ".mkv", ".mp4", ".avi", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".ts", ".mpg", ".mpeg":
			name = name[:idx]
		}
	}

	var p ParsedFilename

	// Detect SxxExx pattern → TV episode.
	lower := strings.ToLower(name)
	for i := 0; i < len(lower)-5; i++ {
		if lower[i] == 's' && isByteDigit(lower[i+1]) && isByteDigit(lower[i+2]) &&
			lower[i+3] == 'e' && isByteDigit(lower[i+4]) && isByteDigit(lower[i+5]) {
			p.IsTV = true
			p.Season = int(lower[i+1]-'0')*10 + int(lower[i+2]-'0')
			p.Episode = int(lower[i+4]-'0')*10 + int(lower[i+5]-'0')
			// Title is everything before the SxxExx token.
			raw := cleanTitle(name[:i])
			p.Title = raw
			return p
		}
	}

	// Look for a 4-digit year (1900–2099).
	yearIdx := -1
	for i := 0; i <= len(name)-4; i++ {
		if isDigit(rune(name[i])) && isDigit(rune(name[i+1])) &&
			isDigit(rune(name[i+2])) && isDigit(rune(name[i+3])) {
			y := name[i : i+4]
			if y >= "1900" && y <= "2099" {
				// Accept only if preceded by a separator.
				if i == 0 || name[i-1] == '.' || name[i-1] == ' ' || name[i-1] == '_' || name[i-1] == '(' {
					yearIdx = i
					p.Year = y
					break
				}
			}
		}
	}

	if yearIdx > 0 {
		p.Title = cleanTitle(name[:yearIdx])
	} else {
		p.Title = cleanTitle(name)
	}

	return p
}

func cleanTitle(s string) string {
	// Replace separators with spaces.
	var b strings.Builder
	prev := ' '
	for _, c := range s {
		if c == '.' || c == '_' || c == '-' {
			c = ' '
		}
		if c == ' ' && prev == ' ' {
			continue
		}
		b.WriteRune(c)
		prev = c
	}
	return strings.TrimSpace(b.String())
}

func isDigit(c rune) bool  { return c >= '0' && c <= '9' }
func isByteDigit(c byte) bool { return c >= '0' && c <= '9' }
