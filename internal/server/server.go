package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"simpleGithubSync/internal/docker"
	"simpleGithubSync/internal/state"
	"simpleGithubSync/internal/ui"
)

// Server is the dashboard HTTP server.
type Server struct {
	store       *state.Store
	docker      *docker.Client // may be nil if Docker is unavailable
	syncTrigger chan<- string
	addr        string
	mux         *http.ServeMux
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
	return s
}

// Start binds and serves. Blocks until the server exits.
func (s *Server) Start() {
	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 0 = no timeout; needed for SSE streams
		IdleTimeout:  60 * time.Second,
	}
	log.Printf("Dashboard listening on %s", s.addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("Dashboard server error: %v", err)
	}
}

func (s *Server) registerRoutes() {
	staticFS, err := fs.Sub(ui.StaticFiles, "dist")
	if err != nil {
		// Should never happen given the embed directive.
		log.Printf("Warning: could not sub static FS: %v", err)
	}

	// GET / — serve the SPA shell
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			// Try to serve from static assets first (for CSS, JS, etc)
			if staticFS != nil {
				assetPath := r.URL.Path[1:] // Remove leading slash
				if f, err := staticFS.Open(assetPath); err == nil {
					defer f.Close()
					http.FileServer(http.FS(staticFS)).ServeHTTP(w, r)
					return
				}
			}
			// 404 for not found
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(ui.IndexHTML)
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
