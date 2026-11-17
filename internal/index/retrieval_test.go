package index_test

import (
	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
	"strings"
	"testing"
)

func TestUnifiedRetrievalDatesStemsAliasesAndIncrementalCards(t *testing.T) {
	root := t.TempDir()
	writeNote(t, root, "Robots/2026-07-14.md", "---\ntitle: Motion experiment\naliases: [DemoBot]\n---\nTracing routes and walking with a camera.\n- [ ] Calibrate camera\n")
	writeNote(t, root, "Education/short.md", "---\ntitle: Course A\n---\nAI coursework.\n")
	writeNote(t, root, "Education/long.md", "---\ntitle: Course B\n---\nArtificial Intelligence coursework.\n")
	writeNote(t, root, "kanban/operations/board.md", `---
kanban: 4
name: operations
meta:
  k_test:
    created: 2026-07-01T00:00:00Z
    updated: 2026-07-18T10:00:00Z
---
# Ready
## Walking experiment ^k_test
labels:: #robotics
Trace paths using the camera.
`)
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reconcile := func() {
		v, e := vault.Scan(root)
		if e != nil {
			t.Fatal(e)
		}
		if e = db.Reconcile(v, markdown.New("/")); e != nil {
			t.Fatal(e)
		}
	}
	reconcile()
	for _, q := range []string{"AI type:note", "Artificial Intelligence type:note", `"Artificial Intelligence" type:note`} {
		rows, e := db.Search(q, 50, false)
		if e != nil || len(rows) != 2 {
			t.Fatalf("bidirectional phrase alias %s: %v %v", q, rows, e)
		}
	}
	for _, q := range []string{"trace", "walk", "vision", "DemoBot", "robotics after:2026-07-01 before:2026-07-31"} {
		rows, e := db.Search(q, 50, false)
		if e != nil || len(rows) == 0 {
			t.Fatalf("%s: %v %v", q, rows, e)
		}
	}
	cards, e := db.Search("robotics type:card after:2026-07-01 before:2026-07-31", 10, false)
	if e != nil || len(cards) != 1 {
		t.Fatalf("cards: %v %v", cards, e)
	}
	if cards[0].Date != "2026-07-18" || cards[0].DateSource != "card_updated" || cards[0].URL != "kanban/operations#card-k_test" {
		t.Fatalf("provenance: %#v", cards[0])
	}
	rows, e := db.Search("robotics after:2026-08-01", 50, false)
	if e != nil || len(rows) != 0 {
		t.Fatalf("date exclusion: %v %v", rows, e)
	}
	writeNote(t, root, "kanban/operations/board.md", "---\nkanban: 4\nname: operations\n---\n# Ready\n## New telescope ^k_new\nAstronomy optics.\n")
	reconcile()
	cards, e = db.Search("trace type:card", 10, false)
	if e != nil || len(cards) != 0 {
		t.Fatalf("old card retained: %v %v", cards, e)
	}
	cards, e = db.Search("telescope type:card", 10, false)
	if e != nil || len(cards) != 1 || !strings.Contains(cards[0].URL, "k_new") {
		t.Fatalf("new card: %v %v", cards, e)
	}
}
func TestRetrievalRejectsInvalidFilters(t *testing.T) {
	db, e := index.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, q := range []string{"after:garbage", "after:2026-11-01 before:2026-10-01", "type:secret"} {
		if _, e := db.Search(q, 10, false); e == nil {
			t.Fatal(q)
		}
	}
}

func TestTitleLookupPrefersCurrentLongNoteOverMentionsAndArchive(t *testing.T) {
	root := t.TempDir()
	writeNote(t, root, "Projects/Release Plan.md", "---\ntitle: Release Plan\n---\n"+strings.Repeat("Detailed current project context and history.\n", 1800))
	writeNote(t, root, "Archive/Old/Release Plan.md", "---\ntitle: Release Plan\n---\nOld release plan.\n- [ ] Review release plan\n")
	writeNote(t, root, "Tasks.md", "- [ ] Follow the release plan\n")
	writeNote(t, root, "Journal.md", "The release plan is mentioned here.\n")
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
	for _, q := range []string{"release plan", `"Release Plan"`, "  RELEASE   PLAN  ", "release plan type:note", "release plan after:2000-01-01"} {
		for _, archived := range []bool{false, true} {
			results, err := db.Search(q, 1, archived)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Slug != "Projects/Release-Plan" || results[0].Kind != "note" {
				t.Fatalf("query %q, archived %v: expected current note first, got %+v", q, archived, results)
			}
		}
	}
	results, err := db.Search("release plan", 50, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if strings.HasPrefix(r.Slug, "Archive/") {
			t.Fatalf("archived note or task leaked: %+v", r)
		}
	}
	results, err = db.Search("release plan", 50, true)
	if err != nil {
		t.Fatal(err)
	}
	archivedCount := 0
	for _, r := range results {
		if strings.HasPrefix(r.Slug, "Archive/") {
			archivedCount++
			if !r.Archived || r.ArchivedReason != "folder:archive" {
				t.Fatalf("missing archive provenance: %+v", r)
			}
		}
	}
	if archivedCount != 2 {
		t.Fatalf("expected archived note and task, got %d", archivedCount)
	}
}

func TestArchiveDirectoriesMatchWholeNamesAtAnyDepth(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"Archive/Old.md", "Projects/aRcHiVeD/Old.md", "Projects/Archive/Older/Old.md", "Archive Research/Current.md", "Projects/Archive.md"} {
		writeNote(t, root, rel, "A synthetic beacon note.\n")
	}
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
	results, err := db.Search("beacon", 50, false)
	if err != nil {
		t.Fatal(err)
	}
	got := slugsOf(results)
	if len(got) != 2 || got[0] != "Archive-Research/Current" || got[1] != "Projects/Archive" {
		t.Fatalf("wrong folder archive classification: %v", got)
	}
}
