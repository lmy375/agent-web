package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const cookieName = "agent_web_auth"

// Authenticator is the single-user password gate. The credential is a signed
// token in an httpOnly cookie rather than a header: an EventSource cannot
// attach headers, so a cookie is the only thing that covers REST calls and the
// SSE streams alike.
//
// Tokens are stateless -- payload.signature, signed with a key derived from a
// per-install random file *and* the current password, so changing the password
// invalidates every cookie already handed out while a restart does not.
type Authenticator struct {
	Enabled  bool
	ttl      time.Duration
	password string
	secret   []byte
	throttle *throttle
}

func NewAuthenticator(password, dataDir string, ttlSeconds int) *Authenticator {
	a := &Authenticator{
		Enabled:  password != "",
		ttl:      time.Duration(ttlSeconds) * time.Second,
		password: password,
		throttle: newThrottle(),
	}
	if a.Enabled {
		mac := hmac.New(sha256.New, machineKey(dataDir))
		mac.Write([]byte(password))
		a.secret = mac.Sum(nil)
	}
	return a
}

// machineKey is a random per-install signing key, created once with 0600.
func machineKey(dataDir string) []byte {
	path := filepath.Join(dataDir, "auth_secret")
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		return existing
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	_ = os.MkdirAll(dataDir, 0o700)
	// Create with the mode up front so the key is never briefly world-readable.
	if file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600); err == nil {
		_, _ = file.Write(key)
		_ = file.Close()
	}
	return key
}

func (a *Authenticator) VerifyPassword(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(a.password)) == 1
}

func (a *Authenticator) issue() string {
	now := time.Now()
	payload, _ := json.Marshal(map[string]int64{"iat": now.Unix(), "exp": now.Add(a.ttl).Unix()})
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + a.sign(encoded)
}

func (a *Authenticator) verify(token string) bool {
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(signature), []byte(a.sign(payload))) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return false
	}
	return time.Now().Unix() < claims.Exp
}

func (a *Authenticator) sign(payload string) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SignedIn reports whether this request carries a valid cookie. With no
// password configured every request is signed in.
func (a *Authenticator) SignedIn(r *http.Request) bool {
	if !a.Enabled {
		return true
	}
	cookie, err := r.Cookie(cookieName)
	return err == nil && a.verify(cookie.Value)
}

func (a *Authenticator) SetCookie(w http.ResponseWriter, r *http.Request, secure *bool) {
	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	if secure != nil {
		isSecure = *secure
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: a.issue(), Path: "/", HttpOnly: true,
		Secure: isSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(a.ttl.Seconds()),
	})
}

func (a *Authenticator) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

// OriginAllowed compares Origin ourselves. CORS does not stop a cross-site page
// from firing requests at a server on this machine, and every one of them would
// carry the ambient cookie -- so a page on the public web must not be able to
// start a thread here.
//
// A loopback origin is allowed when this server is itself bound to loopback:
// that is the dev proxy and any other local tool, never a page from the web,
// whose origin is its own domain.
func OriginAllowed(r *http.Request, allowed []string, loopback bool) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Not a browser; there is no ambient cookie to abuse.
	}
	if len(allowed) > 0 {
		for _, candidate := range allowed {
			if candidate == origin {
				return true
			}
		}
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsed.Host == r.Host {
		return true
	}
	return loopback && isLoopbackHost(parsed.Hostname())
}

func isLoopbackHost(hostname string) bool {
	if hostname == "localhost" {
		return true
	}
	address := net.ParseIP(hostname)
	return address != nil && address.IsLoopback()
}

// ClientIP is best effort. X-Forwarded-For is only trusted for the throttle,
// where the worst case is locking out the wrong key for fifteen minutes.
func ClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	host, _, _ := strings.Cut(r.RemoteAddr, ":")
	return host
}

// throttle counts login failures per client IP plus a global one, so an
// attacker rotating source addresses still hits a ceiling. A single user means
// an in-memory map is enough; nothing needs to survive a restart.
type throttle struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	failures    int
	firstAt     time.Time
	lockedUntil time.Time
}

const (
	throttleWindow = 15 * time.Minute
	throttleLock   = 15 * time.Minute
	maxPerIP       = 5
	maxGlobal      = 20
	maxBuckets     = 1024
	globalKey      = "*"
)

func newThrottle() *throttle { return &throttle{buckets: map[string]*bucket{}} }

// RetryAfter is how many seconds the caller must wait, or 0 when a login
// attempt is allowed.
func (a *Authenticator) RetryAfter(ip string) int {
	t := a.throttle
	t.mu.Lock()
	defer t.mu.Unlock()
	longest := time.Time{}
	for _, key := range []string{ip, globalKey} {
		if b, ok := t.buckets[key]; ok && b.lockedUntil.After(longest) {
			longest = b.lockedUntil
		}
	}
	if remaining := time.Until(longest); remaining > 0 {
		return int(remaining.Seconds()) + 1
	}
	return 0
}

func (a *Authenticator) RecordFailure(ip string) {
	t := a.throttle
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordLocked(ip, maxPerIP)
	t.recordLocked(globalKey, maxGlobal)
	t.evictLocked()
}

func (a *Authenticator) ResetFailures(ip string) {
	t := a.throttle
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.buckets, ip)
	delete(t.buckets, globalKey)
}

func (t *throttle) recordLocked(key string, limit int) {
	now := time.Now()
	b, ok := t.buckets[key]
	if !ok || now.Sub(b.firstAt) > throttleWindow {
		b = &bucket{firstAt: now}
		t.buckets[key] = b
	}
	b.failures++
	if b.failures >= limit {
		b.lockedUntil = now.Add(throttleLock)
		b.failures, b.firstAt = 0, now
	}
}

// evictLocked bounds memory against an attacker cycling through addresses.
func (t *throttle) evictLocked() {
	if len(t.buckets) <= maxBuckets {
		return
	}
	now := time.Now()
	for key, b := range t.buckets {
		if b.lockedUntil.Before(now) && now.Sub(b.firstAt) > throttleWindow {
			delete(t.buckets, key)
		}
	}
	if overflow := len(t.buckets) - maxBuckets; overflow > 0 {
		keys := make([]string, 0, len(t.buckets))
		for key := range t.buckets {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return t.buckets[keys[i]].firstAt.Before(t.buckets[keys[j]].firstAt) })
		for _, key := range keys[:overflow] {
			delete(t.buckets, key)
		}
	}
}
