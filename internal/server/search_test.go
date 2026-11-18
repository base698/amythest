package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestSearchScopePrecedesLimitAndValidatesInput(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		writeSearchFixture(t, root, fmt.Sprintf("Other/%02d.md", i), "---\ntitle: Beacon\n---\nbeacon\n")
	}
	writeSearchFixture(t, root, "Garden/Plan.md", "---\ntitle: Garden plan\n---\nPlant a beacon beside the seedlings.\n- [ ] Check the beacon\n")
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
	s := &Server{db: db}
	for _, tc := range []struct {
		scope         string
		status, count int
	}{
		{`["Garden/Plan"]`, http.StatusOK, 2},
		{`[]`, http.StatusOK, 0},
		{`["missing"]`, http.StatusOK, 0},
		{`null`, http.StatusBadRequest, 0},
		{`"Garden/Plan"`, http.StatusBadRequest, 0},
		{`[`, http.StatusBadRequest, 0},
	} {
		r := httptest.NewRecorder()
		s.handleSearch(r, httptest.NewRequest(http.MethodGet, "/api/search?q=beacon&scope_slugs="+url.QueryEscape(tc.scope), nil))
		if r.Code != tc.status {
			t.Fatalf("scope %s: status %d", tc.scope, r.Code)
		}
		if r.Code != http.StatusOK {
			continue
		}
		var results []index.SearchResult
		if err := json.Unmarshal(r.Body.Bytes(), &results); err != nil {
			t.Fatal(err)
		}
		if len(results) != tc.count {
			t.Fatalf("scope %s: %+v", tc.scope, results)
		}
		for _, result := range results {
			if result.Slug != "Garden/Plan" {
				t.Fatalf("outside scope: %+v", result)
			}
		}
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
