package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
)

func writeSearchFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHandleSearchDefaultsToActiveOnlyAndHonorsIncludeArchived(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, root, "Notes/Active.md", "---\ntitle: Active\n---\nA synthetic beacon note.\n")
	writeSearchFixture(t, root, "Notes/Retired.md", "---\ntitle: Retired\nstatus: archived\n---\nA synthetic beacon note, retired.\n")

	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Reconcile(v, markdown.New("/")); err != nil {
		t.Fatal(err)
	}

	s := &Server{mux: http.NewServeMux(), db: db}
	s.mux.HandleFunc("GET /api/search", s.handleSearch)

	doSearch := func(q string) []index.SearchResult {
		t.Helper()
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search?"+q, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var results []index.SearchResult
		if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return results
	}

	defaultResults := doSearch("q=beacon+note")
	if len(defaultResults) != 1 || defaultResults[0].Slug != "Notes/Active" {
		t.Fatalf("default search = %+v, want only Notes/Active", defaultResults)
	}

	includeArchived := doSearch("q=beacon+note&include_archived=1")
	if len(includeArchived) != 2 {
		t.Fatalf("include_archived=1 search = %+v, want 2 results", includeArchived)
	}
	var sawArchived bool
	for _, r := range includeArchived {
		if r.Slug == "Notes/Retired" {
			sawArchived = true
			if !r.Archived || r.ArchivedReason != "frontmatter:status" {
				t.Fatalf("Retired result = %+v, want archived via frontmatter:status", r)
			}
		}
	}
	if !sawArchived {
		t.Fatalf("include_archived=1 search missing Notes/Retired: %+v", includeArchived)
	}
}
