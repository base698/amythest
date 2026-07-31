package index_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
)

// writeNote writes a fixture note under root, creating parent dirs as
// needed. Fixtures use only generic, synthetic content.
func writeNote(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func slugsOf(results []index.SearchResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Slug)
	}
	sort.Strings(out)
	return out
}

func TestSearchExcludesArchivedByDefaultAndIncludesOnRequest(t *testing.T) {
	root := t.TempDir()
	writeNote(t, root, "Active/Widget.md", "---\ntitle: Widget\n---\nA gizmo widget for testing.\n")
	writeNote(t, root, "Archived/OldWidget.md", "---\ntitle: Old Widget\narchived: true\n---\nA retired gizmo widget.\n")
	writeNote(t, root, "Archived/StatusWidget.md", "---\ntitle: Status Widget\nstatus: archived\n---\nAnother gizmo widget, archived via status.\n")
	writeNote(t, root, "Archived/TaggedWidget.md", "---\ntitle: Tagged Widget\ntags: [archive]\n---\nA gizmo widget archived via tag.\n")

	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := markdown.New("/")
	if err := db.Reconcile(v, e); err != nil {
		t.Fatal(err)
	}

	activeOnly, err := db.Search("gizmo widget", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := slugsOf(activeOnly); len(got) != 1 || got[0] != "Active/Widget" {
		t.Fatalf("active-only search = %v, want only [Active/Widget]", got)
	}
	for _, r := range activeOnly {
		if r.Archived {
			t.Fatalf("active-only result %q reported archived=true", r.Slug)
		}
	}

	withArchived, err := db.Search("gizmo widget", 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := slugsOf(withArchived); len(got) != 4 {
		t.Fatalf("include-archived search = %v, want 4 results", got)
	}

	cases := map[string]struct {
		archived bool
		reason   string
	}{
		"Active/Widget":         {false, ""},
		"Archived/OldWidget":    {true, "frontmatter:archived"},
		"Archived/StatusWidget": {true, "frontmatter:status"},
		"Archived/TaggedWidget": {true, "tag:archive"},
	}
	for _, r := range withArchived {
		want, ok := cases[r.Slug]
		if !ok {
			t.Fatalf("unexpected slug %q in results", r.Slug)
		}
		if r.Archived != want.archived || r.ArchivedReason != want.reason {
			t.Errorf("slug %q: archived=%v reason=%q, want archived=%v reason=%q",
				r.Slug, r.Archived, r.ArchivedReason, want.archived, want.reason)
		}
	}
}

func TestReconcileRefreshesArchivedStatusOnFrontmatterChange(t *testing.T) {
	root := t.TempDir()
	writeNote(t, root, "Notes/Report.md", "---\ntitle: Report\n---\nA quarterly synthetic report.\n")

	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := markdown.New("/")
	if err := db.Reconcile(v, e); err != nil {
		t.Fatal(err)
	}

	before, err := db.Search("quarterly report", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 {
		t.Fatalf("before archiving: got %d results, want 1", len(before))
	}

	// Mark the note archived and rescan — the refresh must pick up the
	// frontmatter change without any manual index reset.
	writeNote(t, root, "Notes/Report.md", "---\ntitle: Report\nstatus: archived\n---\nA quarterly synthetic report.\n")
	v2, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Reconcile(v2, e); err != nil {
		t.Fatal(err)
	}

	afterActiveOnly, err := db.Search("quarterly report", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterActiveOnly) != 0 {
		t.Fatalf("after archiving, active-only search = %d results, want 0", len(afterActiveOnly))
	}

	afterIncluded, err := db.Search("quarterly report", 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterIncluded) != 1 || !afterIncluded[0].Archived || afterIncluded[0].ArchivedReason != "frontmatter:status" {
		t.Fatalf("after archiving, include-archived result = %#v, want archived via frontmatter:status", afterIncluded)
	}
}
