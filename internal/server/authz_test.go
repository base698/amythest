package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/kanban/auth"
)

// authorizeTestServer installs a kanban auth manager on s (vault mutations
// fail closed without one) and returns a valid session cookie for requests.
func authorizeTestServer(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	manager, err := auth.NewManager("test", "test-password-123", []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := manager.NewSession(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.kanbanAuth = manager
	return &http.Cookie{Name: kanbanSessionCookie, Value: token}
}

// withSession installs auth on s and returns a wrapper that stamps requests
// with a valid session cookie.
func withSession(t *testing.T, s *Server) func(*http.Request) *http.Request {
	t.Helper()
	cookie := authorizeTestServer(t, s)
	return func(r *http.Request) *http.Request {
		r.AddCookie(cookie)
		return r
	}
}

func TestVaultMutationsFailClosedWithoutKanbanAuth(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Note.md"), []byte("- [ ] Task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/", SiteName: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.kanbanAuth != nil {
		t.Fatal("test server unexpectedly has kanban auth configured")
	}
	for _, target := range []string{
		"/api/tasks/toggle", "/api/tasks/due", "/api/tasks/triage",
		"/api/tasks/file", "/api/tasks/file/hide", "/api/tasks/move-to-board",
		"/api/tasks/cancel", "/api/tasks/purge", "/api/tasks/priority",
		"/api/share/upload", "/api/share/text", "/api/db/query",
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, strings.NewReader("{}")))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without configured auth: got %d, want 403", target, rec.Code)
		}
	}
}
