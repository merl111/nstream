// Package api implements the REST API and SPA serving for nstream.
package api

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"nstream/internal/db"
	"nstream/internal/neofs"
	"nstream/internal/tmdb"
)

// Scanner is the interface the API uses to trigger on-demand container scans.
type Scanner interface {
	ScanContainer(ctx interface{ Deadline() (interface{}, bool); Done() <-chan struct{}; Err() error; Value(any) any }, c db.Container)
}

// JobRunner is the interface the API uses to kick the background job runner.
type JobRunner interface {
	Kick()
}

// HLSManager is the interface the API uses to serve on-the-fly HLS.
type HLSManager interface {
	ServeMasterPlaylist(w http.ResponseWriter, r *http.Request, videoID int64)
	ServeSegment(w http.ResponseWriter, r *http.Request, videoID int64, session, file string)
}

// API wires together all REST handlers.
type API struct {
	db         *db.DB
	nfs        *neofs.Client
	tmdb       *tmdb.Client
	log        *slog.Logger
	scanner    ScannerI
	jobRunner  JobRunnerI
	hls        HLSManagerI
	webFS      fs.FS
	ffprobe    string
	listenAddr string
	uploads    *uploadManager
	// serverCtx is the server's lifecycle context.  Long-running NeoFS
	// operations (uploads) use this instead of the HTTP request context so
	// they are never killed by a browser timeout or disconnect.
	serverCtx context.Context
}

// ScannerI is the concrete interface used internally.
type ScannerI interface {
	ScanContainer(c db.Container)
}

// JobRunnerI is the concrete interface used internally.
type JobRunnerI interface {
	Kick()
}

// HLSManagerI is the concrete interface used internally.
type HLSManagerI interface {
	ServeMasterPlaylist(w http.ResponseWriter, r *http.Request, videoID int64)
	ServeSegment(w http.ResponseWriter, r *http.Request, videoID int64, session, file string)
	ServeKeepalive(w http.ResponseWriter, r *http.Request, videoID int64)
}

// Config holds dependencies for building the API.
type Config struct {
	DB        *db.DB
	NFS       *neofs.Client
	TMDB      *tmdb.Client
	Log       *slog.Logger
	Scanner   ScannerI
	JobRunner JobRunnerI
	HLS       HLSManagerI
	WebFS     fs.FS
	FFprobe   string // path to ffprobe binary; defaults to "ffprobe"
	// ListenAddr is the address the HTTP server listens on (e.g. ":8080").
	// Used to construct stream URLs for self-probing after upload.
	ListenAddr string
	// ServerCtx is the server's lifecycle context.  Long-running NeoFS
	// operations (uploads) should derive from this rather than the HTTP
	// request context so they survive browser disconnects and timeouts.
	ServerCtx context.Context
}

// New constructs an API and returns its HTTP handler.
func New(cfg Config) *API {
	ffprobe := cfg.FFprobe
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	listenAddr := cfg.ListenAddr
	if listenAddr == "" {
		listenAddr = ":8080"
	}
	serverCtx := cfg.ServerCtx
	if serverCtx == nil {
		serverCtx = context.Background()
	}
	return &API{
		db:         cfg.DB,
		nfs:        cfg.NFS,
		tmdb:       cfg.TMDB,
		log:        cfg.Log,
		scanner:    cfg.Scanner,
		jobRunner:  cfg.JobRunner,
		hls:        cfg.HLS,
		webFS:      cfg.WebFS,
		ffprobe:    ffprobe,
		listenAddr: listenAddr,
		uploads:    newUploadManager(),
		serverCtx:  serverCtx,
	}
}

// Handler returns the root HTTP mux.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()

	// Auth (public)
	mux.HandleFunc("/api/v1/auth/login", a.handleLogin)
	mux.HandleFunc("/api/v1/auth/logout", a.handleLogout)
	mux.Handle("/api/v1/auth/me", a.requireAuth(http.HandlerFunc(a.handleMe)))

	// Users (admin)
	mux.Handle("/api/v1/users", a.requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			a.handleListUsers(w, r)
		case http.MethodPost:
			a.handleCreateUser(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "GET or POST required")
		}
	})))
	mux.Handle("/api/v1/users/", a.requireAdmin(http.HandlerFunc(a.handleDeleteUser)))

	// Videos list (viewer+)
	mux.Handle("/api/v1/videos", a.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			a.handleListVideos(w, r)
		} else {
			writeError(w, http.StatusMethodNotAllowed, "GET required")
		}
	})))

	// Containers (admin)
	mux.Handle("/api/v1/containers", a.requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			a.handleListContainers(w, r)
		case http.MethodPost:
			a.handleAddContainer(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "GET or POST required")
		}
	})))
	// Exact match for /create takes priority over the wildcard below.
	mux.Handle("/api/v1/containers/create", a.requireAdmin(http.HandlerFunc(a.handleCreateNeoFSContainer)))
	mux.Handle("/api/v1/containers/", a.requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/scan") {
			a.handleScanContainer(w, r)
		} else if r.Method == http.MethodDelete {
			a.handleDeleteContainer(w, r)
		} else {
			writeError(w, http.StatusMethodNotAllowed, "DELETE or POST /scan required")
		}
	})))

	// Jobs (admin)
	mux.Handle("/api/v1/jobs", a.requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			a.handleListJobs(w, r)
		case http.MethodPost:
			a.handleCreateJob(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "GET or POST required")
		}
	})))
	mux.Handle("/api/v1/jobs/", a.requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			a.handleGetJob(w, r)
		} else {
			writeError(w, http.StatusMethodNotAllowed, "GET required")
		}
	})))

	// Upload (admin — only admins may add content to NeoFS)
	mux.Handle("/api/v1/upload", a.requireAdmin(http.HandlerFunc(a.handleUpload)))
	mux.Handle("/api/v1/upload/jobs", a.requireAuth(http.HandlerFunc(a.handleListUploadJobs)))

	// TMDB (admin — requires API key)
	mux.Handle("/api/v1/tmdb/search", a.requireAdmin(http.HandlerFunc(a.handleTMDBSearch)))
	mux.Handle("/api/v1/tmdb/languages", a.requireAuth(http.HandlerFunc(a.handleListLanguages)))
	mux.Handle("/api/v1/tmdb/movie/", a.requireAdmin(http.HandlerFunc(a.handleTMDBGetMovie)))
	mux.Handle("/api/v1/tmdb/tv/", a.requireAdmin(http.HandlerFunc(a.handleTMDBGetTV)))

	// Media library (viewer: list/get; admin: import/delete/link)
	mux.Handle("/api/v1/media/import", a.requireAdmin(http.HandlerFunc(a.handleImportMedia)))
	mux.Handle("/api/v1/media", a.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			a.handleListMedia(w, r)
		} else {
			writeError(w, http.StatusMethodNotAllowed, "GET required")
		}
	})))
	mux.Handle("/api/v1/media/", a.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/reimport"):
			a.requireAdmin(http.HandlerFunc(a.handleReimportMedia)).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/automatch"):
			a.requireAdmin(http.HandlerFunc(a.handleAutoMatchVideos)).ServeHTTP(w, r)
		default:
			switch r.Method {
			case http.MethodGet:
				a.handleGetMedia(w, r)
			case http.MethodDelete:
				a.requireAdmin(http.HandlerFunc(a.handleDeleteMedia)).ServeHTTP(w, r)
			default:
				writeError(w, http.StatusMethodNotAllowed, "GET or DELETE required")
			}
		}
	})))

	// Genres
	mux.Handle("/api/v1/genres", a.requireAuth(http.HandlerFunc(a.handleListGenres)))

	// Video link/unlink/match (admin)
	mux.Handle("/api/v1/videos/unlinked", a.requireAdmin(http.HandlerFunc(a.handleUnlinkedVideos)))
	mux.Handle("/api/v1/videos/", a.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/link"):
			a.requireAdmin(http.HandlerFunc(a.handleLinkVideo)).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/unlink"):
			a.requireAdmin(http.HandlerFunc(a.handleUnlinkVideo)).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/match"):
			a.requireAdmin(http.HandlerFunc(a.handleMatchVideo)).ServeHTTP(w, r)
		default:
			if r.Method == http.MethodGet {
				a.handleGetVideo(w, r)
			} else {
				writeError(w, http.StatusMethodNotAllowed, "GET required")
			}
		}
	})))

	// HLS (viewer+)
	mux.Handle("/hls/", a.requireAuth(http.HandlerFunc(a.handleHLS)))

	// Frontend SPA
	if a.webFS != nil {
		mux.Handle("/", a.spaHandler())
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, "nstream API")
		})
	}

	return mux
}

func (a *API) handleHLS(w http.ResponseWriter, r *http.Request) {
	if a.hls == nil {
		writeError(w, http.StatusServiceUnavailable, "HLS transcoding not configured")
		return
	}
	// /hls/{videoID}/master.m3u8
	// /hls/{videoID}/{session}/{file}
	rest := strings.TrimPrefix(r.URL.Path, "/hls/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "malformed HLS path")
		return
	}
	var videoID int64
	if _, err := parseID(parts[0], &videoID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid video id")
		return
	}
	if parts[1] == "master.m3u8" {
		a.hls.ServeMasterPlaylist(w, r, videoID)
		return
	}
	if parts[1] == "keepalive" {
		a.hls.ServeKeepalive(w, r, videoID)
		return
	}
	if len(parts) == 3 {
		a.hls.ServeSegment(w, r, videoID, parts[1], parts[2])
		return
	}
	writeError(w, http.StatusBadRequest, "malformed HLS path")
}

// spaHandler serves the embedded React SPA, returning index.html for all
// non-asset paths so client-side routing works.
func (a *API) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(a.webFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try serving the file; if it doesn't exist fall back to index.html
		_, err := a.webFS.Open(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			// Serve index.html for all unknown paths (SPA routes)
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/index.html"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func parseID(s string, dst *int64) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	if dst != nil {
		*dst = n
	}
	return n, nil
}
