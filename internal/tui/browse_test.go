package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
)

func browseFixtures() ([]apiclient.NoteEntry, map[string]apiclient.ContentEntry, time.Time) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	day := func(offset int) int64 { return now.AddDate(0, 0, offset).Unix() }
	notes := []apiclient.NoteEntry{
		{Slug: "Home", Title: "Home", Path: "Home.md", MTime: day(-1), Size: 10},
		{Slug: "Projects/Garden", Title: "Garden", Path: "Projects/Garden.md", MTime: day(-2), Size: 20},
		{Slug: "Projects/Shed", Title: "Shed", Path: "Projects/Shed.md", MTime: day(-30), Size: 30},
		{Slug: "Archive/Old", Title: "Old", Path: "Archive/Old.md", MTime: day(-90), Size: 40},
	}
	index := map[string]apiclient.ContentEntry{
		"Home":            {Title: "Home"},
		"Projects/Garden": {Title: "Garden", Tags: []string{"garden", "home"}},
		"Projects/Shed":   {Title: "Shed", Tags: []string{"home"}},
		"Archive/Old":     {Title: "Old"},
	}
	return notes, index, now
}

func TestBuildBrowseRowsVirtualFoldersAndTree(t *testing.T) {
	notes, index, now := browseFixtures()
	expanded := map[string]bool{}
	rows := buildBrowseRows(notes, index, expanded, false, now)

	labels := make([]string, len(rows))
	for i, r := range rows {
		labels[i] = r.label
	}
	// Collapsed: virtuals, then root folders (sorted), then root notes.
	want := []string{"Recent", "Untagged", "Tags", "Archive/", "Projects/", "Home"}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v", labels)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("labels = %v, want %v", labels, want)
		}
	}
	if rows[0].count != 2 { // Recent: Home (-1d), Garden (-2d); Shed -30d excluded
		t.Fatalf("recent count = %d", rows[0].count)
	}
	if rows[1].count != 2 { // Untagged: Home, Old
		t.Fatalf("untagged count = %d", rows[1].count)
	}
	if rows[4].count != 2 { // Projects folder holds 2 notes
		t.Fatalf("projects count = %d", rows[4].count)
	}

	// Expanding Projects lists its notes, modified-desc by default.
	expanded["Projects"] = true
	rows = buildBrowseRows(notes, index, expanded, false, now)
	var projectNotes []string
	for _, r := range rows {
		if r.kind == "note" && strings.HasPrefix(r.slug, "Projects/") {
			projectNotes = append(projectNotes, r.label)
		}
	}
	if len(projectNotes) != 2 || projectNotes[0] != "Garden" || projectNotes[1] != "Shed" {
		t.Fatalf("project notes = %v", projectNotes)
	}

	// Tags node expands to per-tag folders ordered by use.
	expanded["@tags"] = true
	rows = buildBrowseRows(notes, index, expanded, false, now)
	var tagLabels []string
	for _, r := range rows {
		if r.kind == "tag" {
			tagLabels = append(tagLabels, r.label+":"+itoa(r.count))
		}
	}
	if len(tagLabels) != 2 || tagLabels[0] != "#home:2" || tagLabels[1] != "#garden:1" {
		t.Fatalf("tags = %v", tagLabels)
	}
}

func TestFilterBrowseRowsDSL(t *testing.T) {
	notes, index, _ := browseFixtures()
	slugsOf := func(filter string) []string {
		rows := filterBrowseRows(notes, index, filter, true)
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.slug
		}
		return out
	}
	if got := slugsOf("t:garden"); len(got) != 1 || got[0] != "Projects/Garden" {
		t.Fatalf("t:garden = %v", got)
	}
	if got := slugsOf("f:projects"); len(got) != 2 {
		t.Fatalf("f:projects = %v", got)
	}
	if got := slugsOf("f:projects t:home shed"); len(got) != 1 || got[0] != "Projects/Shed" {
		t.Fatalf("combined = %v", got)
	}
	if got := slugsOf("zzz"); len(got) != 0 {
		t.Fatalf("no-match = %v", got)
	}
}

func TestNotesViewTabTogglesBrowseAndPreviewDebounces(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	v := newNotesView(client)
	v.Init()
	v.Update(tea.KeyMsg{Type: tea.KeyEsc}) // leave the search input
	if v.Capturing() {
		t.Fatal("should not capture after esc")
	}

	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !v.browsing || cmd == nil {
		t.Fatal("tab must enter browse and load the vault")
	}
	notes, index, _ := browseFixtures()
	v.Update(browseLoadedMsg{notes: notes, index: index})
	if !v.browse.loaded || len(v.browse.rows) == 0 {
		t.Fatal("browse rows not built")
	}
	out := v.browseView(80, 30)
	for _, want := range []string{"Recent", "Tags", "Projects/", "Home"} {
		if !strings.Contains(out, want) {
			t.Fatalf("browse render missing %q:\n%s", want, out)
		}
	}

	// Expanding a folder adds its notes.
	for i, r := range v.browse.rows {
		if r.folder == "Projects" {
			v.browse.cursor = i
			break
		}
	}
	v.Update(enterMsg())
	found := false
	for _, r := range v.browse.rows {
		if r.slug == "Projects/Garden" {
			found = true
		}
	}
	if !found {
		t.Fatal("expanding Projects should reveal Garden")
	}

	// Preview debounce: moving onto a note arms a tick; a stale tick is a
	// no-op; the current tick fetches.
	for i, r := range v.browse.rows {
		if r.slug == "Projects/Garden" {
			v.browse.cursor = i
			break
		}
	}
	cmd = v.schedulePreview()
	if cmd == nil {
		t.Fatal("expected preview tick")
	}
	staleGen := v.preview.gen
	v.schedulePreview() // supersede
	_, cmd = v.Update(previewTickMsg{gen: staleGen})
	if cmd != nil {
		t.Fatal("stale tick must not fetch")
	}
	_, cmd = v.Update(previewTickMsg{gen: v.preview.gen})
	if cmd == nil {
		t.Fatal("current tick must fetch the preview")
	}
	// Loaded preview caches and displays for the selected slug.
	v.Update(previewLoadedMsg{slug: "Projects/Garden", note: &apiclient.Note{Slug: "Projects/Garden", Title: "Garden", Markdown: "# Garden\nbeds"}})
	if v.preview.note == nil || v.preview.slug != "Projects/Garden" {
		t.Fatalf("preview state = %+v", v.preview)
	}
	pane := v.previewPane(50, 20)
	if !strings.Contains(pane, "Garden") || !strings.Contains(pane, "beds") {
		t.Fatalf("preview pane:\n%s", pane)
	}
	// Wide view renders list + preview side by side; narrow hides preview.
	wide := v.View(140, 30)
	if !strings.Contains(wide, "│") {
		t.Fatal("wide view should show the divider")
	}
	narrow := v.View(90, 30)
	if strings.Contains(narrow, "beds") {
		t.Fatal("narrow view must hide the preview")
	}
}

func TestTabEntersBrowseDirectlyFromSearchInput(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	v := newNotesView(client)
	v.Init()
	if !v.Capturing() {
		t.Fatal("search input should be focused on open")
	}
	// Tab straight from the focused input — no esc needed.
	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !v.browsing || cmd == nil {
		t.Fatal("tab from the search input must enter browse")
	}
	notes, index, _ := browseFixtures()
	v.Update(browseLoadedMsg{notes: notes, index: index})

	// And tab from the browse filter input flips back to search.
	v.Update(keyMsg("/"))
	if !v.bTyping {
		t.Fatal("filter input should capture")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v.browsing || v.bTyping {
		t.Fatal("tab from the filter must return to search mode")
	}
}
