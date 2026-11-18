package markdown_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
)

func renderOne(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Note.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := markdown.New("/notes/").Render(v, v.Notes[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(res.HTML)
}

func TestCanvasLinksEmbedTheLiveDrawing(t *testing.T) {
	const img = `src="/notes/api/canvas/abcdefghij23/drawing.png"`
	for name, body := range map[string]string{
		"bare url on its own line": "Design:\n\nhttps://notes.example.com/notes/canvas/abcdefghij23\n",
		"titled link alone":        "[Flow](/notes/canvas/abcdefghij23)\n",
		"image syntax":             "See ![Flow](https://notes.example.com/notes/canvas/abcdefghij23/) here\n",
	} {
		html := renderOne(t, body)
		if !strings.Contains(html, img) || !strings.Contains(html, `class="canvas-embed" data-no-spa href="/notes/canvas/abcdefghij23"`) {
			t.Errorf("%s: no embed in\n%s", name, html)
		}
	}
	if html := renderOne(t, "[Flow](/notes/canvas/abcdefghij23)\n"); !strings.Contains(html, `alt="Flow"`) {
		t.Errorf("label not kept:\n%s", html)
	}
}

func TestCanvasLinksInsideTextStayLinks(t *testing.T) {
	for _, body := range []string{
		"Read [the flow](/notes/canvas/abcdefghij23) before coding.\n",
		"[other](/notes/canvasx/abcdefghij23)\n",
		"[bad id](/notes/canvas/ABC)\n",
		"[elsewhere](/other/canvas/abcdefghij23)\n",
	} {
		if html := renderOne(t, body); strings.Contains(html, "canvas-embed") {
			t.Errorf("embedded %q:\n%s", body, html)
		}
	}
}

func TestCanvasEmbedEscapesLabel(t *testing.T) {
	html := renderOne(t, "[<b>x</b> \"q\"](/notes/canvas/abcdefghij23)\n")
	if strings.Contains(html, "<b>x</b>") {
		t.Fatalf("label not escaped:\n%s", html)
	}
}
