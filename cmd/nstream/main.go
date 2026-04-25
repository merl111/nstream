// Command nstream is a self-hosted NeoFS media platform.
//
// It exposes:
//   - /stream/*  — raw byte-range streaming directly from NeoFS (VLC/mpv/ffplay)
//   - /hls/*     — on-the-fly HLS/H.264 transcoding via ffmpeg
//   - /api/v1/*  — REST API (auth, library, containers, jobs)
//   - /          — embedded React SPA (Vite build)
//
//go:generate sh -c "cd ../../web && npm run build"
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nspcc-dev/neo-go/pkg/crypto/keys"
	"github.com/nspcc-dev/neo-go/pkg/wallet"
	"github.com/nspcc-dev/neofs-sdk-go/user"

	"nstream/internal/api"
	"nstream/internal/db"
	"nstream/internal/neofs"
	"nstream/internal/scanner"
	"nstream/internal/server"
	"nstream/internal/tmdb"
	"nstream/internal/transcode"
	webui "nstream/web"
)

const (
	envWalletPassword = "NSTREAM_WALLET_PASSWORD"
	envWIF            = "NSTREAM_WIF"
)

func main() {
	// Load .env (if present) before flag parsing so NSTREAM_* variables can
	// serve as defaults for flags. Real env vars take priority over file values.
	envPath := envFilePath(os.Args[1:])
	if loaded, err := loadDotEnv(envPath); err != nil {
		fmt.Fprintf(os.Stderr, "nstream: read %s: %v\n", envPath, err)
		os.Exit(1)
	} else if loaded && envPath != ".env" {
		fmt.Fprintf(os.Stderr, "nstream: loaded env from %s\n", envPath)
	}

	var (
		listenAddr     string
		neofsEndpoint  string
		walletPath     string
		walletPassword string
		walletAddress  string
		wif            string
		dialTimeout   time.Duration
		shutdownGrace time.Duration
		verbose        bool
		envFlag        string

		// New flags
		dbPath        string
		ffmpegBin     string
		ffprobeBin    string
		hlsTemp       string
		scanInterval  time.Duration
		adminUser     string
		adminPassword string
		jobWorkers    int
		tmdbKey       string
	)

	flag.StringVar(&envFlag, "env", ".env", "Path to a .env file to load at startup (empty to disable)")
	flag.StringVar(&listenAddr, "listen", envOr("NSTREAM_LISTEN", ":8080"), "HTTP listen address")
	flag.StringVar(&neofsEndpoint, "neofs-endpoint", os.Getenv("NSTREAM_NEOFS_ENDPOINT"), "NeoFS storage node URI (required)")
	flag.StringVar(&walletPath, "wallet", os.Getenv("NSTREAM_WALLET"), "Path to NEP-6 wallet JSON (mutually exclusive with -wif)")
	flag.StringVar(&walletPassword, "wallet-password", "", "Wallet password (or set "+envWalletPassword+")")
	flag.StringVar(&walletAddress, "wallet-address", os.Getenv("NSTREAM_WALLET_ADDRESS"), "Wallet account address")
	flag.StringVar(&wif, "wif", "", "WIF-encoded private key (or set "+envWIF+"); mutually exclusive with -wallet")
	flag.DurationVar(&dialTimeout, "dial-timeout", envDuration("NSTREAM_DIAL_TIMEOUT", 10*time.Second), "Timeout for the initial NeoFS dial")
	flag.DurationVar(&shutdownGrace, "shutdown-grace", 10*time.Second, "Graceful shutdown timeout")
	flag.BoolVar(&verbose, "v", os.Getenv("NSTREAM_VERBOSE") != "", "Verbose (debug) logging")

	flag.StringVar(&dbPath, "db", envOr("NSTREAM_DB", "./nstream.db"), "SQLite database path")
	flag.StringVar(&ffmpegBin, "ffmpeg", envOr("NSTREAM_FFMPEG", "ffmpeg"), "ffmpeg binary (must be in PATH or full path)")
	flag.StringVar(&ffprobeBin, "ffprobe", envOr("NSTREAM_FFPROBE", "ffprobe"), "ffprobe binary (must be in PATH or full path)")
	flag.StringVar(&hlsTemp, "hls-temp", envOr("NSTREAM_HLS_TEMP", filepath.Join(os.TempDir(), "nstream-hls")), "Scratch directory for HLS segments")
	flag.DurationVar(&scanInterval, "scan-interval", envDuration("NSTREAM_SCAN_INTERVAL", time.Hour), "How often to re-scan enabled NeoFS containers")
	flag.StringVar(&adminUser, "admin-user", os.Getenv("NSTREAM_ADMIN_USER"), "Bootstrap admin username (first run only)")
	flag.StringVar(&adminPassword, "admin-password", os.Getenv("NSTREAM_ADMIN_PASSWORD"), "Bootstrap admin password (first run only)")
	flag.IntVar(&jobWorkers, "job-workers", 2, "Number of parallel background transcode workers")
	flag.StringVar(&tmdbKey, "tmdb-key", envOr("NSTREAM_TMDB_KEY", ""), "TMDB API v3 key (free at themoviedb.org)")

	flag.Parse()

	explicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	logLevel := slog.LevelInfo
	if verbose {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(log)

	if err := run(log, runConfig{
		listenAddr:     listenAddr,
		neofsEndpoint:  neofsEndpoint,
		walletPath:     walletPath,
		walletPassword: walletPassword,
		walletAddress:  walletAddress,
		wif:            wif,
		dialTimeout:    dialTimeout,
		shutdownGrace:  shutdownGrace,
		walletFromFlag: explicit["wallet"],
		wifFromFlag:    explicit["wif"],
		dbPath:         dbPath,
		ffmpegBin:      ffmpegBin,
		ffprobeBin:     ffprobeBin,
		hlsTemp:        hlsTemp,
		scanInterval:   scanInterval,
		adminUser:      adminUser,
		adminPassword:  adminPassword,
		jobWorkers:     jobWorkers,
		tmdbKey:        tmdbKey,
	}); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

type runConfig struct {
	listenAddr     string
	neofsEndpoint  string
	walletPath     string
	walletPassword string
	walletAddress  string
	wif            string
	dialTimeout    time.Duration
	shutdownGrace  time.Duration

	walletFromFlag bool
	wifFromFlag    bool

	dbPath        string
		ffmpegBin     string
		ffprobeBin    string
		hlsTemp       string
		scanInterval  time.Duration
	adminUser     string
	adminPassword string
	jobWorkers    int
	tmdbKey       string
}

func run(log *slog.Logger, cfg runConfig) error {
	if cfg.neofsEndpoint == "" {
		return errors.New("-neofs-endpoint is required")
	}
	if cfg.wif == "" {
		cfg.wif = os.Getenv(envWIF)
	}
	if cfg.walletPassword == "" {
		cfg.walletPassword = os.Getenv(envWalletPassword)
	}

	switch {
	case cfg.walletFromFlag && cfg.wifFromFlag:
		return errors.New("-wallet and -wif are mutually exclusive")
	case cfg.walletPath == "" && cfg.wif == "":
		return errors.New("one of -wallet or -wif (or " + envWIF + ") is required")
	case cfg.wif != "" && cfg.walletPath != "":
		log.Info("both wallet and wif are set; using wif", "wallet", cfg.walletPath)
		cfg.walletPath = ""
		cfg.walletPassword = ""
		cfg.walletAddress = ""
	}

	signer, err := loadSigner(cfg)
	if err != nil {
		return fmt.Errorf("signer: %w", err)
	}

	// -- Database -----------------------------------------------------------
	database, err := db.Open(cfg.dbPath)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer database.Close()
	log.Info("database opened", "path", cfg.dbPath)

	// -- NeoFS client -------------------------------------------------------
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dialCtx, dialCancel := context.WithTimeout(ctx, cfg.dialTimeout+5*time.Second)
	nfs, err := neofs.New(dialCtx, neofs.Config{
		Endpoint:    cfg.neofsEndpoint,
		DialTimeout: cfg.dialTimeout,
		Signer:      signer,
	})
	dialCancel()
	if err != nil {
		return err
	}
	defer func() { _ = nfs.Close() }()

	// -- Bootstrap admin user -----------------------------------------------
	if cfg.adminUser != "" && cfg.adminPassword != "" {
		if n, err := database.CountUsers(ctx); err == nil && n == 0 {
			if _, err := database.CreateUser(ctx, cfg.adminUser, cfg.adminPassword, db.RoleAdmin); err != nil {
				log.Warn("bootstrap admin failed", "err", err)
			} else {
				log.Info("bootstrap admin created", "username", cfg.adminUser)
			}
		}
	}

	// -- HLS temp dir -------------------------------------------------------
	if err := os.MkdirAll(cfg.hlsTemp, 0o700); err != nil {
		return fmt.Errorf("hls-temp: %w", err)
	}

	// -- Subsystems ---------------------------------------------------------
	// Build a loopback stream base URL so ffprobe can fetch /stream/... locally,
	// regardless of listen syntax (":8080", "0.0.0.0:8080", "[::]:8080", etc.).
	streamBase := localStreamBase(cfg.listenAddr)
	hlsMgr := transcode.NewManager(database, nfs, cfg.ffmpegBin, cfg.ffprobeBin, streamBase, cfg.hlsTemp, log)
	jobRunner := transcode.NewRunner(database, nfs, cfg.ffmpegBin, cfg.hlsTemp, cfg.jobWorkers, log)
	containerScanner := scanner.New(database, nfs, cfg.scanInterval, cfg.ffprobeBin, log)

	// -- HTTP router --------------------------------------------------------
	// Extract embedded dist/ subtree so file paths work without the "dist/" prefix.
	webSub, err := fs.Sub(webui.FS, "dist")
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}

	tmdbClient := tmdb.New(cfg.tmdbKey)
	if tmdbClient.Enabled() {
		log.Info("TMDB integration enabled")
	} else {
		log.Info("TMDB integration disabled (set NSTREAM_TMDB_KEY to enable)")
	}

	apiHandler := api.New(api.Config{
		DB:         database,
		NFS:        nfs,
		TMDB:       tmdbClient,
		Log:        log,
		Scanner:    containerScanner,
		JobRunner:  jobRunner,
		HLS:        hlsMgr,
		WebFS:      webSub,
		FFprobe:    "ffprobe",
		ListenAddr: cfg.listenAddr,
		ServerCtx:  ctx, // server lifecycle context — uploads use this, not the HTTP request ctx
	})

	// Raw streaming server (existing /stream/ + /health).
	rawSrv := server.New(nfs, log)

	// Combined root mux: stream/ and health go to the old server; everything
	// else goes to the new API (which also hosts /hls/, /api/, and the SPA).
	root := http.NewServeMux()
	root.Handle("/stream/", rawSrv.Handler())
	root.Handle("/health", rawSrv.Handler())
	root.Handle("/", apiHandler.Handler())

	httpSrv := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           withAccessLog(log, root),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// -- Background goroutines ----------------------------------------------
	go containerScanner.Run(ctx)
	go jobRunner.Run(ctx)

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.listenAddr, "neofs", cfg.neofsEndpoint, "db", cfg.dbPath)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownGrace)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func loadSigner(cfg runConfig) (user.Signer, error) {
	if cfg.wif != "" {
		return loadSignerFromWIF(cfg.wif)
	}
	return loadSignerFromWallet(cfg.walletPath, cfg.walletPassword, cfg.walletAddress)
}

func loadSignerFromWIF(wif string) (user.Signer, error) {
	pk, err := keys.NewPrivateKeyFromWIF(wif)
	if err != nil {
		return nil, fmt.Errorf("decode wif: %w", err)
	}
	return user.NewAutoIDSignerRFC6979(pk.PrivateKey), nil
}

func loadSignerFromWallet(path, password, address string) (user.Signer, error) {
	w, err := wallet.NewWalletFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("open wallet: %w", err)
	}
	var acc *wallet.Account
	if address != "" {
		for _, a := range w.Accounts {
			if a.Address == address {
				acc = a
				break
			}
		}
		if acc == nil {
			return nil, fmt.Errorf("account %q not found in wallet", address)
		}
	} else {
		addr := w.GetChangeAddress()
		acc = w.GetAccount(addr)
		if acc == nil {
			return nil, errors.New("wallet has no default account")
		}
	}
	if err := acc.Decrypt(password, w.Scrypt); err != nil {
		return nil, fmt.Errorf("decrypt account: %w", err)
	}
	return user.NewAutoIDSignerRFC6979(acc.PrivateKey().PrivateKey), nil
}

// withAccessLog wraps h in a tiny access logging middleware.
// For /api/ paths it also pre-sets Cache-Control: no-store so the browser
// never permanently caches any redirect or error response from the API.
func withAccessLog(log *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(sw, r)
		log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(started).String(),
			"remote", r.RemoteAddr,
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := s.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// -- .env helpers -----------------------------------------------------------

func envFilePath(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-env" || a == "--env":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-env=") || strings.HasPrefix(a, "--env="):
			return a[strings.Index(a, "=")+1:]
		}
	}
	return ".env"
}

func loadDotEnv(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return false, fmt.Errorf("%s:%d: missing '=' in %q", path, lineNo, line)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if !isQuoted(val) {
			if hash := strings.IndexByte(val, '#'); hash >= 0 {
				val = strings.TrimSpace(val[:hash])
			}
		}
		if isQuoted(val) {
			val = val[1 : len(val)-1]
		}
		if _, ok := os.LookupEnv(key); ok {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return false, fmt.Errorf("%s:%d: setenv %s: %w", path, lineNo, key, err)
		}
	}
	return true, sc.Err()
}

func isQuoted(s string) bool {
	if len(s) < 2 {
		return false
	}
	return (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nstream: invalid %s=%q: %v\n", key, v, err)
		return fallback
	}
	return d
}

func localStreamBase(listenAddr string) string {
	// Common shorthand ":8080"
	if strings.HasPrefix(listenAddr, ":") {
		return "http://127.0.0.1" + listenAddr
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		// Keep behavior predictable even for unusual input.
		return "http://127.0.0.1:8080"
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
