// Package server is the HTTP shell: routing, the single-user gate, the SSE
// streams, and the static frontend. It knows the protocol and the chat service,
// and nothing at all about any agent harness.
package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/config"
	"github.com/lmy375/agent-web/backend/internal/fsx"
	"github.com/lmy375/agent-web/backend/internal/protocol"
)

type Server struct {
	cfg     config.Config
	svc     *chat.Service
	auth    *Authenticator
	handler http.Handler
}

func New(cfg config.Config, svc *chat.Service) *Server {
	s := &Server{cfg: cfg, svc: svc, auth: NewAuthenticator(cfg.Password, cfg.DataDir, cfg.AuthTTLS)}
	s.handler = s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) routes() http.Handler {
	api := http.NewServeMux()
	// Public: the login page has to be able to load and submit.
	api.HandleFunc("GET /api/auth/status", s.authStatus)
	api.HandleFunc("POST /api/auth/login", s.login)
	api.HandleFunc("POST /api/auth/logout", s.logout)

	api.HandleFunc("GET /api/config", s.getConfig)
	api.HandleFunc("GET /api/agents", s.listAgents)
	api.HandleFunc("GET /api/fs/dirs", s.listDirs)
	api.HandleFunc("GET /api/events", s.directoryEvents)

	api.HandleFunc("GET /api/threads", s.listThreads)
	api.HandleFunc("POST /api/threads", s.createThread)
	api.HandleFunc("GET /api/threads/{id}", s.getThread)
	api.HandleFunc("PATCH /api/threads/{id}", s.updateThread)
	api.HandleFunc("DELETE /api/threads/{id}", s.deleteThread)
	api.HandleFunc("GET /api/threads/{id}/messages", s.transcript)
	api.HandleFunc("GET /api/threads/{id}/events", s.threadEvents)
	api.HandleFunc("POST /api/threads/{id}/input", s.postInput)
	api.HandleFunc("GET /api/threads/{id}/files/search", s.searchFiles)

	root := http.NewServeMux()
	root.Handle("/api/", s.guard(api))
	root.Handle("/", s.static())
	return root
}

// guard applies the cookie gate and the Origin check to everything under /api
// except the auth endpoints themselves.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !OriginAllowed(r, s.cfg.AllowedOrigins, s.cfg.IsLoopback()) {
			protocol.WriteError(w, protocol.Errorf(protocol.CodeUnauthorized, "origin not allowed"))
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/auth/") && !s.auth.SignedIn(r) {
			protocol.WriteError(w, protocol.Errorf(protocol.CodeUnauthorized, "sign in first"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// static serves the built frontend, falling back to index.html so the client
// router owns every non-/api path. It stays public: the login page loads from it.
func (s *Server) static() http.Handler {
	if s.cfg.StaticDir == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "frontend not built; run `pnpm build` in frontend/", http.StatusNotFound)
		})
	}
	files := http.FileServer(http.Dir(s.cfg.StaticDir))
	index := filepath.Join(s.cfg.StaticDir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		candidate := filepath.Join(s.cfg.StaticDir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index)
	})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func readJSON(r *http.Request, into any) error {
	// A prompt carries inline base64 images, so the cap is generous but finite.
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return protocol.Errorf(protocol.CodeBadRequest, "malformed body: %v", err)
	}
	return nil
}

func intParam(r *http.Request, name string, fallback, min, max int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return fallback
	}
	return clamp(value, min, max)
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// --- auth ---

type authStatusResponse struct {
	PasswordRequired bool `json:"password_required"`
	SignedIn         bool `json:"signed_in"`
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, authStatusResponse{s.auth.Enabled, s.auth.SignedIn(r)})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		protocol.WriteError(w, err)
		return
	}
	if !s.auth.Enabled {
		writeJSON(w, http.StatusOK, authStatusResponse{false, true})
		return
	}
	ip := ClientIP(r)
	if wait := s.auth.RetryAfter(ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		protocol.WriteError(w, protocol.Errorf(protocol.CodeUnauthorized, "too many attempts; retry in %ds", wait))
		return
	}
	if !s.auth.VerifyPassword(body.Password) {
		s.auth.RecordFailure(ip)
		protocol.WriteError(w, protocol.Errorf(protocol.CodeUnauthorized, "wrong password"))
		return
	}
	s.auth.ResetFailures(ip)
	s.auth.SetCookie(w, r, s.cfg.CookieSecure)
	writeJSON(w, http.StatusOK, authStatusResponse{true, true})
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	s.auth.ClearCookie(w)
	writeJSON(w, http.StatusOK, authStatusResponse{s.auth.Enabled, false})
}

// --- config, agents, filesystem ---

type configResponse struct {
	RootDir string `json:"root_dir"`
	HomeDir string `json:"home_dir"`
}

func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request) {
	home, _ := os.UserHomeDir()
	writeJSON(w, http.StatusOK, configResponse{RootDir: s.cfg.RootDir, HomeDir: home})
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.DescribeAgents(r.Context()))
}

func (s *Server) listDirs(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = s.cfg.RootDir
	}
	resolved, err := chat.ValidateCwd(path)
	if err != nil {
		protocol.WriteError(w, err)
		return
	}
	dirs, err := fsx.ListDirs(resolved)
	if err != nil {
		protocol.WriteError(w, protocol.Errorf(protocol.CodeCwdInvalid, "cannot read %s", resolved))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": resolved, "parent": filepath.Dir(resolved), "dirs": dirs})
}

// --- threads ---

func (s *Server) listThreads(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListThreads(r.URL.Query().Get("cursor"), intParam(r, "limit", 50, 1, 200))
	if err != nil {
		protocol.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createThread(w http.ResponseWriter, r *http.Request) {
	var body protocol.CreateThreadRequest
	if err := readJSON(r, &body); err != nil {
		protocol.WriteError(w, err)
		return
	}
	summary, err := s.svc.CreateThread(r.Context(), body)
	if err != nil {
		protocol.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, summary)
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	detail, err := s.svc.GetThread(r.PathValue("id"))
	if err != nil {
		protocol.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) updateThread(w http.ResponseWriter, r *http.Request) {
	var body protocol.UpdateThreadRequest
	if err := readJSON(r, &body); err != nil {
		protocol.WriteError(w, err)
		return
	}
	summary, err := s.svc.UpdateThread(r.PathValue("id"), body)
	if err != nil {
		protocol.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) deleteThread(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteThread(r.Context(), r.PathValue("id")); err != nil {
		protocol.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) transcript(w http.ResponseWriter, r *http.Request) {
	page, err := s.svc.Transcript(r.Context(), r.PathValue("id"),
		r.URL.Query().Get("before"), intParam(r, "limit", 200, 1, 1000))
	if err != nil {
		protocol.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) postInput(w http.ResponseWriter, r *http.Request) {
	var cmd protocol.ClientCommand
	if err := readJSON(r, &cmd); err != nil {
		protocol.WriteError(w, err)
		return
	}
	if err := s.svc.Handle(r.Context(), r.PathValue("id"), cmd); err != nil {
		protocol.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) searchFiles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		protocol.WriteError(w, protocol.Errorf(protocol.CodeBadRequest, "q is required"))
		return
	}
	result, err := s.svc.SearchFiles(r.PathValue("id"), query, intParam(r, "limit", 20, 1, 100))
	if err != nil {
		protocol.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// --- streams ---

func (s *Server) threadEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.svc.GetThread(id); err != nil {
		protocol.WriteError(w, err)
		return
	}
	streamEvents(w, r, s.svc.Hub(), id)
}

// directoryEvents is the sidebar's stream: thread_updated and thread_deleted for
// every thread at once, so a browser does not have to poll the directory.
func (s *Server) directoryEvents(w http.ResponseWriter, r *http.Request) {
	streamEvents(w, r, s.svc.Hub(), chat.GlobalStream)
}
