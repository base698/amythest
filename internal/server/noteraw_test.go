package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/base698/amythest/internal/config"
)

func TestHandleNoteRawReturnsMarkdownAndResolvesSlugs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Daily Notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("---\ntags: [log]\n---\n# Morning\n\nSee [[Project Plan]] for details.\n")
	if err := os.WriteFile(filepath.Join(root, "Daily Notes", "2026-08-10.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rec := httptest.NewRecorder()
	s.handleNoteRaw(rec, httptest.NewRequest(http.MethodGet, "/api/note?slug=Daily-Notes/2026-08-10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Slug     string `json:"slug"`
		Title    string `json:"title"`
		Path     string `json:"path"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Slug != "Daily-Notes/2026-08-10" || payload.Path != "Daily Notes/2026-08-10.md" {
		t.Fatalf("payload = %+v", payload)
	}
	// Frontmatter is stripped; the wikilink body survives verbatim.
	if payload.Markdown != "# Morning\n\nSee [[Project Plan]] for details.\n" {
		t.Fatalf("markdown = %q", payload.Markdown)
	}

	rec = httptest.NewRecorder()
	s.handleNoteRaw(rec, httptest.NewRequest(http.MethodGet, "/api/note?slug=Nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing note status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.handleNoteRaw(rec, httptest.NewRequest(http.MethodGet, "/api/note", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing slug status = %d", rec.Code)
	}
}
