package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/tasks"
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

func TestHandleNoteWriteReplacesBodyKeepsFrontmatter(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("---\ntags: [log]\n---\n# Old body\n")
	if err := os.WriteFile(filepath.Join(root, "Note.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	auth := withSession(t, s)

	// GET now exposes the whole-file version.
	rec := httptest.NewRecorder()
	s.handleNoteRaw(rec, httptest.NewRequest(http.MethodGet, "/api/note?slug=Note", nil))
	var got struct{ Markdown, Version string }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != tasks.FileVersion(content) || got.Markdown != "# Old body\n" {
		t.Fatalf("get = %+v", got)
	}

	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/note", strings.NewReader(body))
		s.handleNoteWrite(rec, auth(req))
		return rec
	}
	payload := fmt.Sprintf(`{"slug":"Note","markdown":"# New body\n\n- [ ] fresh task\n","expectedVersion":%q}`, got.Version)
	if rec := put(payload); rec.Code != http.StatusOK {
		t.Fatalf("put status = %d body=%s", rec.Code, rec.Body.String())
	}
	after, _ := os.ReadFile(filepath.Join(root, "Note.md"))
	if string(after) != "---\ntags: [log]\n---\n# New body\n\n- [ ] fresh task\n" {
		t.Fatalf("file = %q", after)
	}
	// Stale version conflicts.
	if rec := put(payload); rec.Code != http.StatusConflict {
		t.Fatalf("stale status = %d", rec.Code)
	}
}
