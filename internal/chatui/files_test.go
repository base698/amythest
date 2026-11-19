package chatui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func pngFile(t *testing.T, dir, name string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := 0; x < 40; x++ {
		for y := 0; y < 20; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 6), 80, uint8(y * 12), 255})
		}
	}
	var b bytes.Buffer
	if e := png.Encode(&b, img); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, name)
	if e := os.WriteFile(p, b.Bytes(), 0o600); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestAttachSendsFilesAndPicturesDrawInline(t *testing.T) {
	m, s, _ := harness(t)
	m.o.Graphics = "blocks"
	dir := t.TempDir()
	pic := pngFile(t, dir, "board photo.png")
	i, w := m.windowNamed(t, "demo-telegram-robot")
	drain(t, m, m.show(i), 0)
	target, _ := w.replyTarget()
	typeLine(t, m, `/attach `+strings.ReplaceAll(pic, " ", `\ `)+` here is the board`)
	drafts, _ := s.Drafts("owner")
	if len(drafts) != 1 || len(drafts[0].Attachments) != 1 || drafts[0].Body != "here is the board" || drafts[0].ReplyTo != target.ID {
		t.Fatalf("draft: %+v", drafts)
	}
	v := screen(m)
	if !strings.Contains(v, "[file ") || !strings.Contains(v, "board photo.png") || !strings.Contains(v, "▀") {
		t.Fatalf("file or picture not shown:\n%s", v)
	}
	// /open saves to Downloads and opens it; /save names the place.
	opened := ""
	m.o.DownloadDir = dir
	m.o.Open = func(p string) error { opened = p; return nil }
	var tag string
	for _, msg := range w.msgs {
		for _, a := range msg.Attachments {
			tag = w.refFor(a.ID)
		}
	}
	typeLine(t, m, "/open "+tag)
	if opened != filepath.Join(dir, "board photo (1).png") {
		t.Fatalf("opened %q", opened)
	}
	out := filepath.Join(dir, "copy.png")
	typeLine(t, m, "/save "+tag+" "+out)
	if b, e := os.ReadFile(out); e != nil || len(b) == 0 {
		t.Fatalf("save: %v", e)
	}
	typeLine(t, m, "/attach /no/such/file.png hi")
	if !strings.Contains(screen(m), "No file at /no/such/file.png") {
		t.Fatalf("missing file not explained:\n%s", screen(m))
	}
}

func TestAttachPathCompletion(t *testing.T) {
	m, _, _ := harness(t)
	dir := t.TempDir()
	pngFile(t, dir, "only-one.png")
	m.input.SetValue("/attach " + dir + "/on")
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.input.Value() != "/attach "+dir+"/only-one.png" {
		t.Fatalf("completed %q", m.input.Value())
	}
}

func TestGrantAndRevoke(t *testing.T) {
	m, s, _ := harness(t)
	i, _ := m.windowNamed(t, "demo-telegram-robot")
	drain(t, m, m.show(i), 0)
	typeLine(t, m, "/grant 1h")
	if items, e := s.Items("agent", true); e != nil || len(items) != 1 || items[0].Conversation.ID != "demo-telegram-robot" {
		t.Fatalf("agent sees %+v %v", items, e)
	}
	if !strings.Contains(screen(m), "Agents may read and send here until") {
		t.Fatalf("no confirmation:\n%s", screen(m))
	}
	typeLine(t, m, "/grant forever")
	if !strings.Contains(screen(m), "/grant [DURATION") {
		t.Fatal("bad duration not explained")
	}
	typeLine(t, m, "/revoke")
	if items, _ := s.Items("agent", true); len(items) != 0 {
		t.Fatal("revoke left the grant")
	}
}

func TestSplitArgsAndFreeName(t *testing.T) {
	got := splitArgs(`a\ b "c d" 'e' f`)
	if strings.Join(got, "|") != "a b|c d|e|f" {
		t.Fatalf("%q", got)
	}
	dir := t.TempDir()
	if p := freeName(dir, "../x.txt"); p != filepath.Join(dir, "x.txt") {
		t.Fatalf("%s", p)
	}
	os.WriteFile(filepath.Join(dir, "x.txt"), nil, 0o600)
	if p := freeName(dir, "x.txt"); p != filepath.Join(dir, "x (1).txt") {
		t.Fatalf("%s", p)
	}
}

func paste(m *Model, text string) tea.Cmd {
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true})
	return cmd
}

func TestPastedPathsStageFilesAndEnterSendsThem(t *testing.T) {
	m, s, _ := harness(t)
	dir := t.TempDir()
	a := pngFile(t, dir, "Screen Shot 1.png")
	b := pngFile(t, dir, "b.png")
	i, w := m.windowNamed(t, "demo-telegram-robot")
	drain(t, m, m.show(i), 0)
	// As copypasta's ⇧⌘⏎ pastes into a terminal: backslash-escaped, spaced.
	paste(m, strings.ReplaceAll(a, " ", `\ `)+" "+b)
	if len(w.staged) != 2 || m.input.Value() != "" {
		t.Fatalf("staged %q, input %q", w.staged, m.input.Value())
	}
	if v := screen(m); !strings.Contains(v, "📎 Screen Shot 1.png, b.png") {
		t.Fatalf("staged files not shown:\n%s", v)
	}
	// Ordinary text still pastes as text; a file:// link also stages.
	paste(m, "not a file at all")
	if m.input.Value() != "not a file at all" {
		t.Fatalf("text paste became %q", m.input.Value())
	}
	m.input.SetValue("")
	paste(m, "file://"+a)
	if len(w.staged) != 2 {
		t.Fatal("the same file staged twice")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(w.staged) != 0 {
		t.Fatal("ctrl+x kept the files")
	}
	paste(m, b)
	m.input.SetValue("the board")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(t, m, cmd, 0)
	drafts, _ := s.Drafts("owner")
	if len(drafts) != 1 || drafts[0].Body != "the board" || len(drafts[0].Attachments) != 1 || len(w.staged) != 0 {
		t.Fatalf("drafts %+v staged %v", drafts, w.staged)
	}
	// The clipboard's picture arrives the same way.
	m.onClip(clipMsg{win: w, paths: []string{a}})
	if len(w.staged) != 1 {
		t.Fatal("clipboard picture not staged")
	}
}

func TestPastedFilesParsing(t *testing.T) {
	dir := t.TempDir()
	a := pngFile(t, dir, "it's here.png")
	for _, in := range []string{`'` + strings.ReplaceAll(a, `'`, `'\''`) + `'`, `"` + a + `"`, strings.NewReplacer(" ", `\ `, "'", `\'`).Replace(a)} {
		if got := pastedFiles(in); len(got) != 1 || got[0] != a {
			t.Errorf("%s -> %q", in, got)
		}
	}
	if pastedFiles("relative.png") != nil || pastedFiles(a+" /no/such") != nil || pastedFiles("") != nil {
		t.Fatal("non-files staged")
	}
}
