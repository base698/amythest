package wall

import (
	"errors"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/apiclient"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const testPatch = `diff --git a/first.go b/first.go
index 123..456 100644
--- a/first.go
+++ b/first.go
@@ -10,2 +20,2 @@ func greeting()
 unchanged
-old
+new
diff --git "a/a space #.go" "b/a space #.go"
deleted file mode 100644
--- a/a space #.go
+++ /dev/null
@@ -1 +0,0 @@
-removed
diff --git a/old.png b/new.png
similarity index 100%
rename from old.png
rename to new.png
`

func TestDiffLineNumbersFilesAndOpen(t *testing.T) {
	var opened FileTarget
	m := New(&fakeAPI{}, Options{OpenFile: func(file FileTarget) tea.Cmd { opened = file; return nil }}).(*model)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	it := Item{Art: apiclient.WorkArtifact{Repo: "git@github.com:example/demo.git", Ref: "abc123"}}
	m.Update(diffMsg{item: it, title: "abc123  Example change", text: testPatch})
	if !m.Fullscreen() || !strings.Contains(ansi.Strip(m.View()), "10    20 │  unchanged") {
		t.Fatalf("missing numbered context:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "first.go") {
		t.Fatal("missing current file header")
	}
	for _, row := range strings.Split(m.View(), "\n") {
		if ansi.StringWidth(row) > m.w {
			t.Fatalf("diff overflow: %q", row)
		}
	}
	// Removed lines open the working copy at the next surviving line.
	m.diffTop = 8
	m.openDiffFile()
	if opened.Path != "first.go" || opened.Line != 21 || opened.Repo != it.Art.Repo {
		t.Fatalf("removed line target: %+v", opened)
	}
	m.diffTop = 9
	m.openDiffFile()
	if opened.Path != "first.go" || opened.Line != 21 {
		t.Fatalf("new line target: %+v", opened)
	}
	m.jumpFile(1)
	m.openDiffFile()
	if opened.Path != "a space #.go" || opened.Line != 1 {
		t.Fatalf("deleted file target: %+v", opened)
	}
	m.jumpFile(1)
	m.openDiffFile()
	if opened.Path != "new.png" {
		t.Fatalf("renamed binary target: %+v", opened)
	}
	last := m.diffTop
	m.Update(FileOpened{})
	if m.diffTop != last || !m.Fullscreen() {
		t.Fatal("editor return lost the diff")
	}
	m.Update(FileOpened{Err: errors.New("no local checkout")})
	if m.diffTop != last || !m.Fullscreen() || !strings.Contains(m.View(), "no local checkout") {
		t.Fatal("opening error did not stay in the diff")
	}
	m.jumpFile(-1)
	if m.diffTop >= last {
		t.Fatal("previous file did not move back")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.Fullscreen() || m.view != "feed" {
		t.Fatal("Escape did not leave the diff")
	}
}

func TestDiffQuotedPathsAndHorizontalScroll(t *testing.T) {
	rows := parseDiff(strings.Split("diff --git \"a/caf\\303\\251.go\" \"b/caf\\303\\251.go\"\n--- \"a/caf\\303\\251.go\"\n+++ \"b/caf\\303\\251.go\"\n@@ -0,0 +1 @@\n+abcdefghijklmnopqrstuv", "\n"))
	if rows[0].file != "café.go" || rows[4].new != 1 {
		t.Fatalf("quoted filename / new file: %+v", rows)
	}
	got := ansi.Strip(rows[4].render(25, 8))
	if strings.Contains(got, "abcdefgh") || !strings.Contains(got, "ijklm") || ansi.StringWidth(got) > 25 {
		t.Fatalf("sideways scroll lost gutter or overflowed: %q", got)
	}
	rows = parseDiff(strings.Split("--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-before\n+after", "\n"))
	if rows[0].file != "file.txt" || !rows[0].start {
		t.Fatal("plain unified diff has no file target")
	}
}

func TestDiffResizePreservesWallSelectionAndScroll(t *testing.T) {
	s := snapshot()
	s.Artifacts = nil
	for i := 0; i < 30; i++ {
		s.Artifacts = append(s.Artifacts, apiclient.WorkArtifact{ID: strings.Repeat("x", i+1), Kind: "commit", Title: strings.Repeat("A long title with context ", 4)})
	}
	m, _ := newWall(t, &fakeAPI{overview: s, project: s}, Options{Project: "Robot", Graphics: "off"})
	m.summary = false
	m.relayout()
	m.move(25)
	cursor, top := m.cursor, m.top
	m.Update(diffMsg{item: m.items[cursor], title: "Change", text: testPatch})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	_, redraw := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	// The dashboard resizes synchronously, then delivers the wall's redraw.
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 28})
	if redraw != nil {
		m.Update(redraw())
	}
	if m.cursor != cursor || m.top != top {
		t.Fatalf("return position changed: cursor %d/%d, top %d/%d", m.cursor, cursor, m.top, top)
	}
}
