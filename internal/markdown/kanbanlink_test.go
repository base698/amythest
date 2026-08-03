package markdown_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
)

// A task moved to a board leaves [[kanban/<board>/board#^card-<id>]] behind.
// That slug's plain URL (/kanban/<board>/board) lands in the board UI, which
// treats every segment after the board name as a label filter — so the link
// showed an empty board. It must point at the board itself instead.
func TestKanbanBoardWikilinkPointsAtTheBoardUINotTheMarkdownPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "kanban", "operations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kanban", "operations", "board.md"), []byte("# operations\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Src.md"),
		[]byte("- Moved [[kanban/operations/board#^card-k_19fc6d904dc_f479e594cc|k_19fc6d904dc_f479e594cc]]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	var src *vault.Note
	for _, n := range v.Notes {
		if n.Slug == "Src" {
			src = n
		}
	}
	if src == nil {
		t.Fatal("source note not scanned")
	}
	res, err := markdown.New("/notes/").Render(v, src)
	if err != nil {
		t.Fatal(err)
	}
	html := string(res.HTML)
	want := `href="/notes/kanban/operations#card-k_19fc6d904dc_f479e594cc"`
	if !strings.Contains(html, want) {
		t.Fatalf("want %s in:\n%s", want, html)
	}
	if strings.Contains(html, "/notes/kanban/operations/board") {
		t.Fatalf("link still targets the markdown path, which filters the board to nothing:\n%s", html)
	}
	// The visible text is just the id.
	if !strings.Contains(html, ">k_19fc6d904dc_f479e594cc</a>") {
		t.Fatalf("link text should be the card id:\n%s", html)
	}
}

// The board UI owns the whole /kanban/* URL space, so no note inside the
// kanban tree can render as a note page — and any extra path segment is read
// as a label filter. Every such link therefore resolves to the board itself.
func TestEveryKanbanTreeNoteLinksToItsBoard(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "kanban", "operations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kanban", "operations", "done.md"), []byte("# done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Src.md"), []byte("[[kanban/operations/done]]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	var src *vault.Note
	for _, n := range v.Notes {
		if n.Slug == "Src" {
			src = n
		}
	}
	res, err := markdown.New("/notes/").Render(v, src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.HTML), `href="/notes/kanban/operations"`) {
		t.Fatalf("kanban note should link to its board:\n%s", res.HTML)
	}
	if strings.Contains(string(res.HTML), `href="/notes/kanban/operations/done"`) {
		t.Fatalf("trailing segment would be read as a label filter:\n%s", res.HTML)
	}
}

// A note outside the kanban tree is untouched, however its path looks.
func TestNonKanbanNotesKeepTheirNormalURL(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Projects", "kanban-notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Projects", "kanban-notes", "plan.md"), []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Src.md"), []byte("[[Projects/kanban-notes/plan]]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	var src *vault.Note
	for _, n := range v.Notes {
		if n.Slug == "Src" {
			src = n
		}
	}
	res, err := markdown.New("/notes/").Render(v, src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.HTML), `href="/notes/Projects/kanban-notes/plan"`) {
		t.Fatalf("ordinary note rewritten:\n%s", res.HTML)
	}
}
