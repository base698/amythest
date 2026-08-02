package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/base698/amythest/internal/config"
)

func TestSnippetHTMLEscapesContentAndKeepsHighlights(t *testing.T) {
	in := "code \x02<img src=x onerror=alert(1)>\x03 rest"
	got := snippetHTML(in)
	want := "code <b>&lt;img src=x onerror=alert(1)&gt;</b> rest"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestHandleDBRejectsUnknownTableNames(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Note.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/", SiteName: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, target := range []string{
		`/db/nosuchtable`,
		`/db/x%22%3B%20ATTACH%20DATABASE%20--`, // quote/injection attempt
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status=%d want 404", target, rec.Code)
		}
	}
	// Arbitrary SQL on a GET needs a session even for a known table shape.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/db/nosuchtable?sql=SELECT+1", nil))
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Errorf("sql without session: status=%d want 404/403", rec.Code)
	}
}
