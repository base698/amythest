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

func TestNotesListBasesListAndBaseData(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("Projects/Garden.md", "---\nstatus: active\n---\n# Garden\n")
	writeFile("Projects/Shed.md", "---\nstatus: done\n---\n# Shed\n")
	writeFile("Projects.base", "source: notes\nfilters:\n  and:\n    - file.folder == \"Projects\"\nviews:\n  - type: table\n    name: All\n    order: [file.name, note.status]\n")

	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// /api/notes: listing with paths, titles, mtimes.
	rec := httptest.NewRecorder()
	s.handleNotesList(rec, httptest.NewRequest(http.MethodGet, "/api/notes", nil))
	var notes []struct {
		Slug, Title, Path string
		MTime, Size       int64
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &notes); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 || notes[0].Path != "Projects/Garden.md" || notes[0].MTime == 0 || notes[0].Size == 0 {
		t.Fatalf("notes = %+v", notes)
	}

	// /api/bases: names.
	rec = httptest.NewRecorder()
	s.handleBasesList(rec, httptest.NewRequest(http.MethodGet, "/api/bases", nil))
	var names []string
	if err := json.Unmarshal(rec.Body.Bytes(), &names); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "Projects" {
		t.Fatalf("bases = %v", names)
	}

	// /api/base: evaluated view with row-parallel slugs.
	rec = httptest.NewRecorder()
	s.handleBaseData(rec, httptest.NewRequest(http.MethodGet, "/api/base?name=Projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("base status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Name  string   `json:"name"`
		Views []string `json:"views"`
		Data  struct {
			Columns []string `json:"columns"`
			Groups  []struct {
				Rows  [][]string `json:"rows"`
				Slugs []string   `json:"slugs"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Name != "Projects" || len(payload.Views) != 1 || payload.Views[0] != "All" {
		t.Fatalf("payload meta = %+v", payload)
	}
	g := payload.Data.Groups
	if len(g) != 1 || len(g[0].Rows) != 2 || len(g[0].Slugs) != 2 {
		t.Fatalf("groups = %+v", g)
	}
	if g[0].Slugs[0] != "Projects/Garden" && g[0].Slugs[1] != "Projects/Garden" {
		t.Fatalf("slugs = %v", g[0].Slugs)
	}

	// Errors: unknown base 404, bad view index 400.
	rec = httptest.NewRecorder()
	s.handleBaseData(rec, httptest.NewRequest(http.MethodGet, "/api/base?name=Nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown base = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.handleBaseData(rec, httptest.NewRequest(http.MethodGet, "/api/base?name=Projects&view=-1", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad view = %d", rec.Code)
	}
}
