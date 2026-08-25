package server

import (
	"errors"
	"fmt"
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

func TestBuildTreeGroupsDailyNotesByMonth(t *testing.T) {
	root := t.TempDir()
	daily := filepath.Join(root, "Daily Notes")
	if err := os.MkdirAll(daily, 0o755); err != nil {
		t.Fatal(err)
	}
	// 15 day-notes across two months plus one non-dated note.
	for day := 1; day <= 10; day++ {
		os.WriteFile(filepath.Join(daily, fmt.Sprintf("2026-08-%02d.md", day)), []byte("x"), 0o644)
	}
	for day := 1; day <= 5; day++ {
		os.WriteFile(filepath.Join(daily, fmt.Sprintf("2026-07-%02d.md", day)), []byte("x"), 0o644)
	}
	os.WriteFile(filepath.Join(daily, "Template.md"), []byte("x"), 0o644)

	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tree := s.tree.Load()
	var dailyNode *treeNode
	for _, c := range tree.Children {
		if c.Name == "Daily Notes" {
			dailyNode = c
		}
	}
	if dailyNode == nil {
		t.Fatal("Daily Notes folder missing")
	}
	// Template stays flat; months are dirs, newest first; days newest first.
	var names []string
	for _, c := range dailyNode.Children {
		names = append(names, c.Name)
	}
	if len(names) != 3 || names[0] != "Template" || names[1] != "2026-08" || names[2] != "2026-07" {
		t.Fatalf("children = %v", names)
	}
	aug := dailyNode.Children[1]
	if !aug.IsDir || len(aug.Children) != 10 || aug.Children[0].Name != "2026-08-10" {
		t.Fatalf("august = %+v first=%v", len(aug.Children), aug.Children[0].Name)
	}
}
