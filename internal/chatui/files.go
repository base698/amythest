package chatui

import (
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/base698/amythest/internal/messages"
	"github.com/base698/amythest/internal/termimg"
	tea "github.com/charmbracelet/bubbletea"
)

// Files on messages: each has a tag like a message ref. /open TAG saves it
// to Downloads and opens it; /save TAG [PATH] only saves it. Pictures are
// drawn under their message, as the wall draws them: Kitty graphics where
// the terminal has them, coloured half blocks elsewhere.
//
// /attach [REF] PATH... [text] sends files, with optional text, as a reply
// the way plain text would be (or to REF).
//
// /grant [DURATION] [files] lets agents read and send in this conversation
// for a while (30 minutes unless given; up to 24 hours). /revoke ends it.

const (
	maxImageRows = 14
	maxImageCols = 72
	maxFiles     = 8
)

// pic is one picture attachment: loading, failed, or decoded and placed.
type pic struct {
	src     image.Image
	loading bool
	err     error
	placed  map[[2]int]int // cols×rows -> Kitty image id
	blocks  map[[2]int][]string
}

type picMsg struct {
	id  string
	img image.Image
	err error
}

type fileMsg struct {
	conv, text string
	err        error
}

func isPicture(a messages.Attachment) bool {
	switch a.MIME {
	case "image/png", "image/jpeg", "image/gif":
		return a.Size > 0 && a.Size <= messages.MaxAttachment
	}
	return false
}

// fetchPictures starts loading the window's pictures not yet asked for.
func (m *Model) fetchPictures(w *window) tea.Cmd {
	if m.o.Graphics == "off" || w.kind != kindConv {
		return nil
	}
	var cmds []tea.Cmd
	for _, msg := range w.msgs {
		for _, a := range msg.Attachments {
			if !isPicture(a) || m.pics[a.ID] != nil {
				continue
			}
			m.pics[a.ID] = &pic{loading: true, placed: map[[2]int]int{}, blocks: map[[2]int][]string{}}
			id := a.ID
			cmds = append(cmds, func() tea.Msg {
				c, cancel := ctx()
				defer cancel()
				b, e := m.api.Download(c, id)
				if e != nil {
					return picMsg{id: id, err: e}
				}
				img, e := termimg.Decode(b)
				return picMsg{id: id, img: img, err: e}
			})
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) onPic(x picMsg) {
	p := m.pics[x.id]
	if p == nil {
		return
	}
	p.loading, p.src, p.err = false, x.img, x.err
}

// pictureRows draws a loaded picture in at most maxCols×maxRows cells,
// uploading it to the terminal the first time it is shown at that size.
func (m *Model) pictureRows(id string, maxCols, maxRows int) []string {
	p := m.pics[id]
	if p == nil || p.src == nil {
		return nil
	}
	b := p.src.Bounds()
	cols, rows := termimg.BoxFor(b.Dx(), b.Dy(), min(maxCols, termimg.MaxCells), min(maxRows, termimg.MaxCells), m.o.CellAspect)
	if cols == 0 {
		return nil
	}
	box := [2]int{cols, rows}
	if m.o.Graphics != "kitty" {
		if p.blocks[box] == nil {
			clear(p.blocks)
			p.blocks[box] = termimg.BlockRows(p.src, cols, rows)
		}
		return p.blocks[box]
	}
	imgID, ok := p.placed[box]
	if !ok {
		cellW := m.o.CellW
		if cellW <= 0 {
			cellW = 10
		}
		w := min(b.Dx(), cols*cellW)
		data, err := termimg.EncodePNG(termimg.Shrink(p.src, w, max(1, w*b.Dy()/b.Dx())))
		if err != nil {
			return nil
		}
		for old, oldID := range p.placed {
			io.WriteString(m.o.Out, termimg.KittyDelete(oldID))
			delete(p.placed, old)
		}
		imgID = m.nextImage
		m.nextImage++
		io.WriteString(m.o.Out, termimg.KittyTransmit(imgID, cols, rows, data))
		p.placed[box] = imgID
	}
	return termimg.KittyRows(imgID, cols, rows)
}

// Cleanup frees the pictures uploaded to the terminal. Call it on exit.
func (m *Model) Cleanup() {
	if m.o.Graphics != "kitty" {
		return
	}
	for _, p := range m.pics {
		for _, id := range p.placed {
			io.WriteString(m.o.Out, termimg.KittyDelete(id))
		}
	}
}

// attachmentLines are the rows under a message for its files.
func (m *Model) attachmentLines(w *window, msg *messages.Message, pad string, width int) []line {
	var out []line
	room := max(10, width-len([]rune(pad)))
	for _, a := range msg.Attachments {
		tag := w.refFor(a.ID)
		label := fmt.Sprintf("[file %s] %s (%s) · /open %s", tag, messages.Plain(a.Name), size(a.Size), tag)
		out = append(out, line{pad + dim.Render(label), msg.ID})
		if !isPicture(a) || m.o.Graphics == "off" {
			continue
		}
		if p := m.pics[a.ID]; p != nil && p.err != nil {
			out = append(out, line{pad + dim.Render("(picture could not be shown)"), msg.ID})
			continue
		}
		rows := m.pictureRows(a.ID, min(room, maxImageCols), maxImageRows)
		if rows == nil {
			out = append(out, line{pad + dim.Render("loading picture…"), msg.ID})
			continue
		}
		for _, r := range rows {
			out = append(out, line{pad + r, msg.ID})
		}
	}
	return out
}

func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// attachment finds a file in this window by its tag.
func (w *window) attachment(tag string) (messages.Attachment, bool) {
	id, ok := w.byRef[strings.TrimPrefix(strings.ToLower(tag), "#")]
	if !ok {
		return messages.Attachment{}, false
	}
	for _, msg := range w.msgs {
		for _, a := range msg.Attachments {
			if a.ID == id {
				return a, true
			}
		}
	}
	return messages.Attachment{}, false
}

// --- /attach -------------------------------------------------------------------

// splitArgs splits on spaces, keeping "quoted parts" and backslash-escaped
// spaces together, so paths with spaces can be given.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	quote, has := rune(0), false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			has = true
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote, has = r, true
		case quote == 0 && r == ' ':
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, e := os.UserHomeDir(); e == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func isFile(p string) bool {
	st, e := os.Stat(expandHome(p))
	return e == nil && st.Mode().IsRegular()
}

func (m *Model) attach(w *window, rest string) tea.Cmd {
	if w.kind != kindConv {
		m.status("-!- /attach works in a chat window.")
		return nil
	}
	args := splitArgs(rest)
	var target messages.Message
	ok := false
	if len(args) > 0 && !isFile(args[0]) {
		if target, ok = w.message(args[0]); ok {
			args = args[1:]
		}
	}
	if !ok {
		if target, ok = w.replyTarget(); !ok {
			w.note(m.o.Now(), "-!- No messages here yet to answer with a file.")
			return nil
		}
	}
	var paths []string
	for len(args) > 0 && isFile(args[0]) {
		paths = append(paths, expandHome(args[0]))
		args = args[1:]
	}
	if len(paths) == 0 {
		hint := "-!- /attach [REF] PATH... [text] — Tab completes paths"
		if len(args) > 0 {
			hint = fmt.Sprintf("-!- No file at %s. /attach [REF] PATH... [text]", args[0])
		}
		w.note(m.o.Now(), "%s", hint)
		return nil
	}
	if len(paths) > maxFiles {
		w.note(m.o.Now(), "-!- A message can carry up to %d files.", maxFiles)
		return nil
	}
	return m.sendFiles(w, target, paths, strings.Join(args, " "))
}

// sendFiles uploads files and sends them, with body, as a reply to target.
func (m *Model) sendFiles(w *window, target messages.Message, paths []string, body string) tea.Cmd {
	conv := w.conv.ID
	w.note(m.o.Now(), "-!- Sending %d file(s)…", len(paths))
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		var ids []string
		for _, p := range paths {
			b, e := os.ReadFile(p)
			if e == nil && len(b) > messages.MaxAttachment {
				e = fmt.Errorf("%s is over %s", filepath.Base(p), size(messages.MaxAttachment))
			}
			if e != nil {
				return sentMsg{conv: conv, err: e}
			}
			a, e := m.api.Attach(c, filepath.Base(p), b)
			if e != nil {
				return sentMsg{conv: conv, err: e}
			}
			ids = append(ids, a.ID)
		}
		d, e := m.api.Draft(c, messages.DraftInput{RequestID: messages.ID(), ReplyTo: target.ID, Body: body, Attachments: ids})
		if e == nil {
			d, e = m.api.Send(c, d.ID)
		}
		return sentMsg{conv: conv, draft: d, err: e}
	}
}

// completePath completes the last word of an /attach line as a path.
func (m *Model) completePath() bool {
	v := m.input.Value()
	if !strings.HasPrefix(v, "/attach ") {
		return false
	}
	i := strings.LastIndex(v, " ")
	word := v[i+1:]
	matches, _ := filepath.Glob(expandHome(word) + "*")
	if len(matches) == 0 {
		return true
	}
	sort.Strings(matches)
	common := matches[0]
	for _, x := range matches[1:] {
		for !strings.HasPrefix(x, common) {
			common = common[:len(common)-1]
		}
	}
	if len(matches) == 1 {
		if st, e := os.Stat(common); e == nil && st.IsDir() {
			common += "/"
		}
	} else if len(common) <= len(expandHome(word)) {
		names := make([]string, 0, 10)
		for _, x := range matches[:min(10, len(matches))] {
			names = append(names, filepath.Base(x))
		}
		more := ""
		if len(matches) > 10 {
			more = fmt.Sprintf(" (+%d)", len(matches)-10)
		}
		m.cur().note(m.o.Now(), "-!- %s%s", strings.Join(names, "  "), more)
	}
	if h, e := os.UserHomeDir(); e == nil && strings.HasPrefix(word, "~") && strings.HasPrefix(common, h) {
		common = "~" + strings.TrimPrefix(common, h)
	}
	m.input.SetValue(v[:i+1] + strings.ReplaceAll(common, " ", `\ `))
	m.input.CursorEnd()
	return true
}

// --- /open and /save -----------------------------------------------------------

func (m *Model) saveFile(w *window, rest string, open bool) tea.Cmd {
	args := splitArgs(rest)
	usage := "-!- /save TAG [PATH] — TAG is the [file abc] label"
	if open {
		usage = "-!- /open TAG — TAG is the [file abc] label"
	}
	if len(args) == 0 {
		w.note(m.o.Now(), "%s", usage)
		return nil
	}
	a, ok := w.attachment(args[0])
	if !ok {
		w.note(m.o.Now(), "%s", usage)
		return nil
	}
	dest := ""
	if len(args) > 1 {
		dest = expandHome(args[1])
	}
	dir := m.o.DownloadDir
	conv := w.conv.ID
	opener := m.o.Open
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		b, e := m.api.Download(c, a.ID)
		if e != nil {
			return fileMsg{conv: conv, err: e}
		}
		path := dest
		if path == "" {
			path = freeName(dir, a.Name)
		} else if st, e := os.Stat(path); e == nil && st.IsDir() {
			path = freeName(path, a.Name)
		}
		if e = os.WriteFile(path, b, 0o600); e != nil {
			return fileMsg{conv: conv, err: e}
		}
		if open {
			if e = opener(path); e != nil {
				return fileMsg{conv: conv, err: fmt.Errorf("saved %s but could not open it: %v", path, e)}
			}
			return fileMsg{conv: conv, text: "Opened " + path}
		}
		return fileMsg{conv: conv, text: "Saved " + path}
	}
}

// freeName is dir/name, numbered when that file already exists.
func freeName(dir, name string) string {
	name = filepath.Base(name)
	if name == "." || name == "/" || name == "" {
		name = "attachment"
	}
	p := filepath.Join(dir, name)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		if _, e := os.Lstat(p); os.IsNotExist(e) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

func openWithSystem(path string) error {
	return exec.Command("open", path).Run()
}

// --- /grant --------------------------------------------------------------------

func (m *Model) grant(w *window, rest string) tea.Cmd {
	if w.kind != kindConv {
		m.status("-!- /grant works in a chat window.")
		return nil
	}
	d, files := 30*time.Minute, false
	for _, a := range strings.Fields(rest) {
		if a == "files" {
			files = true
			continue
		}
		x, e := time.ParseDuration(a)
		if e != nil || x <= 0 || x > 24*time.Hour {
			w.note(m.o.Now(), "-!- /grant [DURATION up to 24h] [files] — e.g. /grant 1h")
			return nil
		}
		d = x
	}
	g := messages.Grant{Conversation: w.conv.ID, Expires: m.o.Now().Add(d), Send: true, Attachments: files}
	conv := w.conv.ID
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		if e := m.api.Grant(c, g); e != nil {
			return fileMsg{conv: conv, err: e}
		}
		what := "read and send"
		if files {
			what = "read, send and attach files"
		}
		return fileMsg{conv: conv, text: fmt.Sprintf("Agents may %s here until %s. Conversation: %s · /revoke ends it.", what, g.Expires.Local().Format("15:04"), conv)}
	}
}

func (m *Model) revoke(w *window) tea.Cmd {
	if w.kind != kindConv {
		return nil
	}
	conv := w.conv.ID
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		if e := m.api.Revoke(c, conv); e != nil {
			return fileMsg{conv: conv, err: e}
		}
		return fileMsg{conv: conv, text: "Agents can no longer use this conversation."}
	}
}
