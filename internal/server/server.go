package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"stackd/internal/docker"
	"stackd/internal/state"
	"stackd/internal/ui"
)

// Server is the dashboard HTTP server.
type Server struct {
	store       *state.Store
	docker      *docker.Client // may be nil if Docker is unavailable
	syncTrigger chan<- string
	addr        string
	mux         *http.ServeMux
	handler     http.Handler // final handler with all middlewares applied
	syncLimiter *rateLimiter
}

// New creates a Server. syncTrigger receives repo names for on-demand syncs.
// dockerClient may be nil; log endpoints will return 503 in that case.
func New(store *state.Store, dockerClient *docker.Client, syncTrigger chan<- string, port int) *Server {
	s := &Server{
		store:       store,
		docker:      dockerClient,
		syncTrigger: syncTrigger,
		addr:        fmt.Sprintf(":%d", port),
		mux:         http.NewServeMux(),
	}
	s.registerRoutes()

	window := 5 * time.Second
	if v, err := strconv.Atoi(os.Getenv("SYNC_RATE_LIMIT_SECONDS")); err == nil && v > 0 {
		window = time.Duration(v) * time.Second
	}
	s.syncLimiter = newRateLimiter(window)

	token := os.Getenv("DASHBOARD_TOKEN")
	s.handler = securityHeaders(authMiddleware(token, s.mux))
	return s
}

// Start binds and serves. Blocks until the server exits.
func (s *Server) Start(ctx context.Context) {
	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 0 = no timeout; needed for SSE streams
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Dashboard server shutdown error: %v", err)
		}
	}()

	log.Printf("Dashboard listening on %s", s.addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("Dashboard server error: %v", err)
	}
}

func (s *Server) registerRoutes() {
	// Explicitly register MIME types so assets are served correctly on systems
	// without a complete MIME database (e.g. Alpine Linux containers).
	mime.AddExtensionType(".css", "text/css; charset=utf-8")
	mime.AddExtensionType(".js", "application/javascript")
	mime.AddExtensionType(".svg", "image/svg+xml")
	mime.AddExtensionType(".ico", "image/x-icon")
	mime.AddExtensionType(".png", "image/png")
	mime.AddExtensionType(".woff2", "font/woff2")

	staticFS, err := fs.Sub(ui.StaticFiles, "dist")
	if err != nil {
		// Should never happen given the embed directive.
		log.Printf("Warning: could not sub static FS: %v", err)
	}

	// GET /assets/ — serve hashed JS/CSS bundles produced by Vite.
	// A dedicated route avoids the catch-all handler misrouting these requests.
	if staticFS != nil {
		s.mux.Handle("GET /assets/", http.FileServer(http.FS(staticFS)))
	}

	// GET / — serve index.html for the root; for all other paths try the
	// embedded dist FS (covers favicon.svg, icons.svg, etc. from public/).
	fileServer := http.FileServer(http.FS(staticFS))
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(ui.IndexHTML)
			return
		}
		if staticFS != nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// GET /api/status — full state snapshot
	s.mux.HandleFunc("GET /api/status", s.handleStatus)

	// POST /api/sync/{repo} — trigger an immediate sync
	s.mux.HandleFunc("POST /api/sync/{repo}", s.handleSync)

	// GET /api/logs/{container} — SSE stream of Docker logs
	s.mux.HandleFunc("GET /api/logs/{container}", s.handleLogs)
}

// repoView is the per-repo shape returned by /api/status.
// Stacks are nested inside their repo so the frontend can render them directly.
type repoView struct {
	Name      string             `json:"name"`
	LastSync  time.Time          `json:"lastSync"`
	LastSHA   string             `json:"lastSha"`
	Status    state.SyncStatus   `json:"status"`
	LastError string             `json:"lastError,omitempty"`
	Infisical state.InfisicalState `json:"infisical"`
	Stacks    []state.StackState `json:"stacks"`
}

// handleStatus returns the full state as JSON with stacks nested inside repos.
// Note: InfisicalState only exposes Enabled and Env (the environment name), never the token value.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	full := s.store.GetAll()

	// Group stacks by repoName.
	byRepo := make(map[string][]state.StackState, len(full.Repos))
	for _, st := range full.Stacks {
		byRepo[st.RepoName] = append(byRepo[st.RepoName], st)
	}

	repos := make([]repoView, 0, len(full.Repos))
	for _, r := range full.Repos {
		stacks := byRepo[r.Name]
		if stacks == nil {
			stacks = []state.StackState{}
		}
		repos = append(repos, repoView{
			Name:      r.Name,
			LastSync:  r.LastSync,
			LastSHA:   r.LastSHA,
			Status:    r.Status,
			LastError: r.LastError,
			Infisical: full.Infisical,
			Stacks:    stacks,
		})
	}

	resp := struct {
		Repos     []repoView          `json:"repos"`
		Infisical state.InfisicalState `json:"infisical"`
	}{
		Repos:     repos,
		Infisical: full.Infisical,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("status encode error: %v", err)
	}
}

// handleSync enqueues an on-demand sync for the named repo.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("repo")
	if repo == "" {
		http.Error(w, "missing repo name", http.StatusBadRequest)
		return
	}

	if !s.syncLimiter.Allow(repo) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "rate_limited",
			"repo":   repo,
		})
		return
	}

	select {
	case s.syncTrigger <- repo:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "queued", "repo": repo})
	default:
		// Channel full — a sync is already pending.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "already_queued", "repo": repo})
	}
}

// handleLogs streams container logs as Server-Sent Events.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	containerName := r.PathValue("container")
	if containerName == "" {
		http.Error(w, "missing container name", http.StatusBadRequest)
		return
	}

	if s.docker == nil {
		http.Error(w, "Docker client unavailable", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	flusher.Flush()

	sw := &sseWriter{w: w, flusher: flusher}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if err := s.docker.StreamLogs(ctx, containerName, sw); err != nil {
		fmt.Fprintf(sw, "error: %v", err)
	}
}

// authMiddleware protects API endpoints with bearer token authentication.
// If DASHBOARD_TOKEN is empty, all requests pass through (auth disabled).
func authMiddleware(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always allow: health probes, metrics, static assets, dashboard HTML
		path := r.URL.Path
		if path == "/healthz" || path == "/readyz" || path == "/metrics" ||
			path == "/" || strings.HasPrefix(path, "/assets/") ||
			!strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		// Validate Bearer token
		auth := r.Header.Get("Authorization")
		expected := "Bearer " + token
		if auth != expected {
			w.Header().Set("WWW-Authenticate", `Bearer realm="stackd"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders adds standard security response headers to all responses.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// rateLimiter enforces a minimum time window between requests per key.
type rateLimiter struct {
	mu       sync.Mutex
	lastTime map[string]time.Time
	window   time.Duration
}

func newRateLimiter(window time.Duration) *rateLimiter {
	return &rateLimiter{
		lastTime: make(map[string]time.Time),
		window:   window,
	}
}

func (rl *rateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	last, ok := rl.lastTime[key]
	if ok && time.Since(last) < rl.window {
		return false
	}
	rl.lastTime[key] = time.Now()
	return true
}

// sseWriter formats each Write call as one or more SSE data events.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *sseWriter) Write(p []byte) (int, error) {
	text := string(p)
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		if _, err := fmt.Fprintf(s.w, "data: %s\n\n", line); err != nil {
			return 0, err
		}
	}
	s.flusher.Flush()
	return len(p), nil
}
