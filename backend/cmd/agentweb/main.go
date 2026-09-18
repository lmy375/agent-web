// Command agentweb serves the chat API and the built frontend from one process.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/lmy375/agent-web/backend/internal/agents/claudecode"
	"github.com/lmy375/agent-web/backend/internal/agents/codex"
	"github.com/lmy375/agent-web/backend/internal/agents/opencode"
	"github.com/lmy375/agent-web/backend/internal/agents/pi"
	"github.com/lmy375/agent-web/backend/internal/chat"
	"github.com/lmy375/agent-web/backend/internal/config"
	"github.com/lmy375/agent-web/backend/internal/protocol"
	"github.com/lmy375/agent-web/backend/internal/server"
)

func main() {
	cfg := config.Load()
	if err := os.MkdirAll(cfg.RootDir, 0o755); err != nil {
		log.Fatalf("cannot create root dir %s: %v", cfg.RootDir, err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		log.Fatalf("cannot create data dir %s: %v", cfg.DataDir, err)
	}
	// Every thread runs with this user's full shell and filesystem access, so
	// an address anyone else can reach has to be behind a password.
	if cfg.Password == "" && !cfg.IsLoopback() && !cfg.AllowNoAuth {
		log.Fatalf("refusing to bind %s with no AGENT_WEB_PASSWORD set; use a loopback host or AGENT_WEB_ALLOW_NO_AUTH=true", cfg.Host)
	}

	registry, err := chat.NewRegistry(filepath.Join(cfg.DataDir, "threads.db"))
	if err != nil {
		log.Fatalf("cannot open thread database: %v", err)
	}
	// Declared before the service's own defer so it closes last: stopping a
	// backend still touches the directory.
	defer func() { _ = registry.Close() }()
	hub := chat.NewHub()
	deps := chat.Deps{Publish: hub.Publish, Registry: registry}
	svc := chat.NewService(registry, hub, buildBackends(cfg, deps)...)
	defer svc.Close()

	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	httpServer := &http.Server{
		Addr:              address,
		Handler:           server.New(cfg, svc),
		ReadHeaderTimeout: 10 * time.Second,
		// Streams are open for as long as a browser tab is, so no write deadline.
	}

	go func() {
		log.Printf("agent-web listening on http://%s (agents: %s)", address, cfg.Agents)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

// buildBackends constructs the enabled kinds in the configured order; an empty
// AGENT_WEB_AGENTS means all of them.
func buildBackends(cfg config.Config, deps chat.Deps) []chat.Backend {
	wanted := cfg.Agents
	if len(wanted) == 0 {
		wanted = []string{string(protocol.KindClaudeCode), string(protocol.KindCodex), string(protocol.KindOpenCode), string(protocol.KindPi)}
	}
	idle := time.Duration(cfg.IdleTimeoutS) * time.Second
	backends := []chat.Backend{}
	for _, kind := range wanted {
		switch protocol.AgentKind(kind) {
		case protocol.KindClaudeCode:
			backends = append(backends, claudecode.New(claudecode.Options{
				Bin: cfg.ClaudePath, DefaultCwd: cfg.RootDir,
				OAuthToken: cfg.ClaudeOAuthToken, IdleTimeout: idle,
			}, deps))
		case protocol.KindCodex:
			backends = append(backends, codex.New(codex.Options{
				Bin: cfg.CodexPath, DefaultCwd: cfg.RootDir, IdleTimeout: idle,
			}, deps))
		case protocol.KindOpenCode:
			backends = append(backends, opencode.New(opencode.Options{
				Bin: cfg.OpenCodePath, DefaultCwd: cfg.RootDir, IdleTimeout: idle,
			}, deps))
		case protocol.KindPi:
			backends = append(backends, pi.New(pi.Options{
				Bin: cfg.PiPath, DefaultCwd: cfg.RootDir, IdleTimeout: idle,
			}, deps))
		default:
			log.Fatalf("unknown agent kind %q in AGENT_WEB_AGENTS", kind)
		}
	}
	return backends
}
