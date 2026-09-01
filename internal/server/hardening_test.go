package server

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/base698/amythest/internal/config"
)

func hardenedServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Note.md"), []byte("# Note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/"}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProbesLivenessReadinessAndLegacyHealth(t *testing.T) {
	s := hardenedServer(t, nil)

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	// Started and indexed: everything healthy.
	if rec := get("/probes/liveness"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("liveness = %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("/probes/readiness"); rec.Code != 200 {
		t.Fatalf("readiness = %d", rec.Code)
	}
	if rec := get("/health"); rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatalf("legacy health = %d %q", rec.Code, rec.Body.String())
	}

	// Simulate pre-ready state: readiness 503 names the check, liveness 200.
	s.indexReady.Store(false)
	if rec := get("/probes/readiness"); rec.Code != http.StatusServiceUnavailable ||
		!strings.Contains(rec.Body.String(), `"index"`) {
		t.Fatalf("not-ready readiness = %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("/probes/liveness"); rec.Code != 200 {
		t.Fatalf("liveness during unready = %d", rec.Code)
	}
	if rec := get("/health"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("legacy health during unready = %d", rec.Code)
	}
	s.indexReady.Store(true)
}

func TestProbePathsAreConfigurable(t *testing.T) {
	s := hardenedServer(t, func(c *config.Config) {
		c.LivenessPath = "/livez"
		c.ReadinessPath = "/readyz"
	})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rec.Code != 200 {
		t.Fatalf("custom liveness = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != 200 {
		t.Fatalf("custom readiness = %d", rec.Code)
	}
}

// captureLogs redirects slog.Default to a buffer for the test's duration.
func captureLogs(t *testing.T, level slog.Level) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRequestIDEchoAndAccessLogRedaction(t *testing.T) {
	s := hardenedServer(t, nil)
	logs := captureLogs(t, slog.LevelDebug)

	// Inbound id is echoed and appears in the access log.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-Id", "corr-42")
	req.Header.Set("Authorization", "Bearer super-secret-token")
	req.Header.Set("Cookie", "amythest_kanban_session=secret-cookie")
	s.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-Id"); got != "corr-42" {
		t.Fatalf("echoed id = %q", got)
	}
	out := logs.String()
	if !strings.Contains(out, "corr-42") {
		t.Fatalf("access log missing correlation id:\n%s", out)
	}
	if strings.Contains(out, "super-secret-token") || strings.Contains(out, "secret-cookie") {
		t.Fatalf("secret leaked into logs:\n%s", out)
	}

	// Missing id: generated and echoed.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("no generated request id")
	}
}

func TestAccessLogDemotesProbesToDebug(t *testing.T) {
	s := hardenedServer(t, nil)
	logs := captureLogs(t, slog.LevelInfo) // debug suppressed

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probes/readiness", nil))
	if strings.Contains(logs.String(), "/probes/readiness") {
		t.Fatalf("probe request logged at info:\n%s", logs.String())
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil))
	if !strings.Contains(logs.String(), "/api/search") {
		t.Fatalf("real request missing from access log:\n%s", logs.String())
	}
}

// ---- JWT ----

type jwks struct {
	mu   sync.Mutex
	keys []map[string]string
}

func (j *jwks) add(kid string, key *rsa.PublicKey) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.keys = append(j.keys, map[string]string{
		"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	})
}

func (j *jwks) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	j.mu.Lock()
	defer j.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": j.keys})
}

func mintToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestJWTModeMatrix(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keySet := &jwks{}
	keySet.add("kid1", &key.PublicKey)
	jwksSrv := httptest.NewServer(keySet)
	defer jwksSrv.Close()

	s := hardenedServer(t, func(c *config.Config) {
		c.AuthMode = "jwt"
		c.AuthJWKSURI = jwksSrv.URL
		c.AuthIssuer = "https://issuer.test"
		c.AuthAudience = "amythest"
		c.AuthProtect = "ui"
		c.AuthRequiredScope = "notes.read"
	})

	now := time.Now()
	base := func() jwt.MapClaims {
		return jwt.MapClaims{
			"iss": "https://issuer.test", "aud": "amythest", "sub": "tester",
			"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
			"scope": "notes.read other.scope",
		}
	}
	request := func(token string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		s.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := request(mintToken(t, key, "kid1", base())); got != 200 {
		t.Fatalf("valid token = %d", got)
	}
	if got := request(""); got != 401 {
		t.Fatalf("missing token = %d", got)
	}
	expired := base()
	expired["exp"] = now.Add(-time.Hour).Unix()
	if got := request(mintToken(t, key, "kid1", expired)); got != 401 {
		t.Fatalf("expired = %d", got)
	}
	wrongIss := base()
	wrongIss["iss"] = "https://evil.test"
	if got := request(mintToken(t, key, "kid1", wrongIss)); got != 401 {
		t.Fatalf("wrong issuer = %d", got)
	}
	wrongAud := base()
	wrongAud["aud"] = "someone-else"
	if got := request(mintToken(t, key, "kid1", wrongAud)); got != 401 {
		t.Fatalf("wrong audience = %d", got)
	}
	noScope := base()
	noScope["scope"] = "other.scope"
	if got := request(mintToken(t, key, "kid1", noScope)); got != 403 {
		t.Fatalf("missing scope = %d", got)
	}
	// alg=none and disallowed symmetric algs are rejected.
	none := jwt.NewWithClaims(jwt.SigningMethodNone, base())
	none.Header["kid"] = "kid1"
	noneToken, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if got := request(noneToken); got != 401 {
		t.Fatalf("alg none = %d", got)
	}
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, base())
	hsToken, _ := hs.SignedString([]byte("shared"))
	if got := request(hsToken); got != 401 {
		t.Fatalf("HS256 = %d", got)
	}
	// Wrong key entirely (bad signature).
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	if got := request(mintToken(t, otherKey, "kid1", base())); got != 401 {
		t.Fatalf("bad signature = %d", got)
	}

	// Probes stay open in every mode.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probes/readiness", nil))
	if rec.Code != 200 {
		t.Fatalf("probe under auth = %d", rec.Code)
	}
	// MCP is not protected when protect=ui (route absent here → 404, not 401).
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if rec.Code == 401 {
		t.Fatal("protect=ui must not guard /mcp")
	}
}

func TestJWTKeyRotationPicksUpNewKID(t *testing.T) {
	key1, _ := rsa.GenerateKey(rand.Reader, 2048)
	key2, _ := rsa.GenerateKey(rand.Reader, 2048)
	keySet := &jwks{}
	keySet.add("kid1", &key1.PublicKey)
	jwksSrv := httptest.NewServer(keySet)
	defer jwksSrv.Close()

	s := hardenedServer(t, func(c *config.Config) {
		c.AuthMode = "jwt"
		c.AuthJWKSURI = jwksSrv.URL
		c.AuthProtect = "ui"
	})
	claims := jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}

	// kid2 unknown yet: rotate the JWKS, then the unknown-kid refresh path
	// must pick it up without a restart.
	keySet.add("kid2", &key2.PublicKey)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil)
	req.Header.Set("Authorization", "Bearer "+mintToken(t, key2, "kid2", claims))
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("rotated kid = %d", rec.Code)
	}
}

func TestJWTFailsFastOnBadConfig(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Note.md"), []byte("x"), 0o644)
	// Missing JWKS URI.
	_, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/", AuthMode: "jwt"})
	if err == nil || !strings.Contains(err.Error(), "JWKS") {
		t.Fatalf("missing jwks err = %v", err)
	}
	// Unreachable JWKS.
	_, err = New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/",
		AuthMode: "jwt", AuthJWKSURI: "http://127.0.0.1:1/jwks.json"})
	if err == nil {
		t.Fatal("unreachable jwks must fail startup")
	}
	// Unknown mode.
	_, err = New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/", AuthMode: "banana"})
	if err == nil {
		t.Fatal("unknown auth mode must fail startup")
	}
	// Static without token.
	os.Unsetenv("AMYTHEST_MCP_TOKEN")
	_, err = New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/", AuthMode: "static"})
	if err == nil {
		t.Fatal("static mode without token must fail startup")
	}
}

func TestAuthOffLeavesEverythingOpen(t *testing.T) {
	s := hardenedServer(t, nil) // AuthMode unset = off
	for _, path := range []string{"/health", "/api/search?q=x", "/api/tasks?query=not%20done"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == 401 || rec.Code == 403 {
			t.Fatalf("auth off but %s = %d", path, rec.Code)
		}
	}
}

func TestConfigPrecedenceFlagBeatsEnvBeatsDefault(t *testing.T) {
	t.Setenv("AMYTHEST_LOG_FORMAT", "json")
	t.Setenv("AMYTHEST_AUTH_MODE", "static")
	dir := t.TempDir()
	cfg, err := config.Load([]string{"-vault", dir, "-auth-mode", "off"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogFormat != "json" {
		t.Fatalf("env should beat default: %q", cfg.LogFormat)
	}
	if cfg.AuthMode != "off" {
		t.Fatalf("flag should beat env: %q", cfg.AuthMode)
	}
	if cfg.LivenessPath != "/probes/liveness" || cfg.AuthAlgs != "RS256,ES256" {
		t.Fatalf("defaults wrong: %q %q", cfg.LivenessPath, cfg.AuthAlgs)
	}
	_ = fmt.Sprint() // keep fmt imported if assertions change
}
