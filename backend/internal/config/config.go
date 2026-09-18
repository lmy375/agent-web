// Package config reads every setting from AGENT_WEB_-prefixed environment
// variables, optionally seeded from a .env file next to the binary's module.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	// Default working directory for new threads; the picker can choose another.
	RootDir string
	// Where this server keeps its thread registry and auth secret.
	DataDir string
	// Explicit paths to the harness binaries; empty means look them up on PATH.
	ClaudePath   string
	CodexPath    string
	OpenCodePath string
	PiPath       string
	// Long-lived token from `claude setup-token`, forwarded to the subprocess.
	ClaudeOAuthToken string
	// Seconds an idle harness process is kept before it is stopped; the next
	// prompt resumes the thread from its transcript.
	IdleTimeoutS int
	// Which kinds to expose, in order. Empty means all of them.
	Agents []string

	Host string
	Port int

	// Single-user gate. Empty disables the login screen entirely.
	Password       string
	AuthTTLS       int
	CookieSecure   *bool
	AllowedOrigins []string
	AllowNoAuth    bool
	StaticDir      string
}

func Load() Config {
	loadDotEnv()
	home, _ := os.UserHomeDir()
	c := Config{
		RootDir:          env("ROOT_DIR", filepath.Join(home, "agent-web-workspace")),
		DataDir:          env("DATA_DIR", filepath.Join(home, ".agent-web")),
		ClaudePath:       env("CLAUDE_PATH", ""),
		CodexPath:        env("CODEX_PATH", ""),
		OpenCodePath:     env("OPENCODE_PATH", ""),
		PiPath:           env("PI_PATH", ""),
		ClaudeOAuthToken: env("CLAUDE_OAUTH_TOKEN", ""),
		IdleTimeoutS:     envInt("IDLE_TIMEOUT_S", 900),
		Agents:           envList("AGENTS", nil),
		Host:             env("HOST", "127.0.0.1"),
		Port:             envInt("PORT", 8000),
		Password:         env("PASSWORD", ""),
		AuthTTLS:         envInt("AUTH_TTL_S", 30*24*3600),
		AllowedOrigins:   envList("ALLOWED_ORIGINS", nil),
		AllowNoAuth:      env("ALLOW_NO_AUTH", "") == "true",
		StaticDir:        env("STATIC_DIR", defaultStaticDir()),
	}
	if raw := env("COOKIE_SECURE", ""); raw != "" {
		secure := raw == "true"
		c.CookieSecure = &secure
	}
	return c
}

func (c Config) IsLoopback() bool {
	switch c.Host {
	case "127.0.0.1", "localhost", "::1", "::ffff:127.0.0.1":
		return true
	}
	return false
}

func env(name, fallback string) string {
	if value, ok := os.LookupEnv("AGENT_WEB_" + name); ok && value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	if value, err := strconv.Atoi(env(name, "")); err == nil {
		return value
	}
	return fallback
}

func envList(name string, fallback []string) []string {
	raw := env(name, "")
	if raw == "" {
		return fallback
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// defaultStaticDir serves the built frontend when it is sitting where the repo
// layout puts it, so `go run ./cmd/agentweb` is a single-process deployment.
func defaultStaticDir() string {
	for _, candidate := range []string{"../frontend/dist", "frontend/dist"} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

// loadDotEnv fills in variables a .env file sets, without overriding the real
// environment. Values are taken literally; only surrounding quotes are dropped.
func loadDotEnv() {
	file, err := os.Open(".env")
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}
