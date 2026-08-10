package server

import (
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/tasks"
)

func TestRenderPageStatusBuffersTemplateBeforeWriting(t *testing.T) {
	tmpl := template.Must(template.New("base.html").Parse(`{{define "base.html"}}{{.Title}}{{end}}`))
	s := &Server{tmpl: tmpl}
	rec := httptest.NewRecorder()

	s.renderPageStatus(rec, http.StatusNotFound, pageData{Title: "missing"})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Body.String(); got != "missing" {
		t.Fatalf("body = %q, want %q", got, "missing")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
}

func TestHandleNoteRefreshesAfterOutOfBandEdit(t *testing.T) {
	root := t.TempDir()
	notePath := filepath.Join(root, "Project.md")
	orig := []byte("# Project\n\n- [ ] Ship release\n")
	if err := os.WriteFile(notePath, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/notes/"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleNote(rec, httptest.NewRequest(http.MethodGet, "/Project", nil))
		return rec
	}
	if rec := get(); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tasks.FileVersion(orig)) {
		t.Fatalf("initial page: status=%d, missing version %s", rec.Code, tasks.FileVersion(orig))
	}

	// An out-of-band edit (git pull, iCloud sync) must be visible on the next
	// request — the embedded task version is what mutations validate against.
	edited := []byte("# Project\n\n- [ ] Ship release now\n")
	if err := os.WriteFile(notePath, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := get(); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tasks.FileVersion(edited)) {
		t.Fatalf("page after out-of-band edit: status=%d, still serving stale task version", rec.Code)
	}

	if err := os.Remove(notePath); err != nil {
		t.Fatal(err)
	}
	if rec := get(); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted note: status=%d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRenderPageStatusReturnsCleanTemplateError(t *testing.T) {
	tmpl := template.Must(template.New("base.html").Funcs(template.FuncMap{
		"fail": func() (string, error) { return "", errors.New("boom") },
	}).Parse(`{{define "base.html"}}prefix{{fail}}{{end}}`))
	s := &Server{tmpl: tmpl}
	rec := httptest.NewRecorder()

	s.renderPageStatus(rec, http.StatusNotFound, pageData{})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := rec.Body.String(); got == "prefix" {
		t.Fatalf("partial template output leaked: %q", got)
	}
}
