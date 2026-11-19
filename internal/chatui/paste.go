package chatui

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Files can be attached without typing /attach:
//
//   - Pasting or dropping file paths (as copypasta's ⇧⌘⏎ or a Finder drag
//     does, backslash-escaped or quoted, or as file:// links) stages those
//     files instead of inserting the text.
//   - Ctrl+V stages the picture on the clipboard (a screenshot, "Copy
//     Image"), or the files copied in Finder. A terminal cannot paste an
//     image itself, so this reads the clipboard directly.
//
// Staged files show above the input; Enter sends them with whatever was
// typed, as a reply like plain text. Ctrl+X clears them.

type clipMsg struct {
	win   *window
	paths []string
	err   error
}

// pastedFiles reads a paste as file paths, or returns nil when it is not
// entirely existing files.
func pastedFiles(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" || strings.Count(text, "\n") > maxFiles {
		return nil
	}
	var words []string
	for _, l := range strings.Split(text, "\n") {
		words = append(words, splitArgs(strings.TrimSpace(l))...)
	}
	if len(words) == 0 || len(words) > maxFiles {
		return nil
	}
	var out []string
	for _, w := range words {
		if strings.HasPrefix(w, "file://") {
			u, err := url.Parse(w)
			if err != nil {
				return nil
			}
			w = u.Path
		}
		w = expandHome(w)
		if !filepath.IsAbs(w) || !isFile(w) {
			return nil
		}
		out = append(out, w)
	}
	return out
}

func (w *window) stage(paths []string) (added int, full bool) {
	for _, p := range paths {
		dup := false
		for _, s := range w.staged {
			dup = dup || s == p
		}
		if dup {
			continue
		}
		if len(w.staged) >= maxFiles {
			return added, true
		}
		w.staged = append(w.staged, p)
		added++
	}
	return added, false
}

func (m *Model) stageFiles(w *window, paths []string) {
	if w.kind != kindConv {
		m.status("-!- Open a chat to attach files.")
		return
	}
	if _, full := w.stage(paths); full {
		w.note(m.o.Now(), "-!- A message can carry up to %d files.", maxFiles)
	}
}

// pasteKey takes a paste of file paths in a chat window.
func (m *Model) pasteKey(k tea.KeyMsg) bool {
	w := m.cur()
	if !k.Paste || w.kind != kindConv || m.br.open {
		return false
	}
	paths := pastedFiles(string(k.Runes))
	if paths == nil {
		return false
	}
	m.stageFiles(w, paths)
	return true
}

// clipboardScript saves the clipboard's picture as PNG at the given path,
// or prints the POSIX paths of copied files. It prints "none" otherwise.
const clipboardScript = `on run argv
	try
		set d to the clipboard as «class PNGf»
		set f to open for access (POSIX file (item 1 of argv)) with write permission
		set eof f to 0
		write d to f
		close access f
		return "png"
	end try
	try
		set out to ""
		repeat with x in (the clipboard as list)
			try
				set out to out & POSIX path of (x as alias) & linefeed
			end try
		end repeat
		if out is not "" then return out
	end try
	return "none"
end run`

// pasteClipboard stages what the clipboard holds: a picture, or files.
func (m *Model) pasteClipboard() tea.Cmd {
	w := m.cur()
	if w.kind != kindConv {
		m.status("-!- Open a chat to attach files.")
		return nil
	}
	dir := m.o.PasteDir
	return func() tea.Msg {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return clipMsg{win: w, err: err}
		}
		png := filepath.Join(dir, "pasted-"+time.Now().Format("20060102-150405")+".png")
		out, err := exec.Command("osascript", "-e", clipboardScript, png).Output()
		if err != nil {
			return clipMsg{win: w, err: fmt.Errorf("could not read the clipboard")}
		}
		switch r := strings.TrimSpace(string(out)); r {
		case "png":
			return clipMsg{win: w, paths: []string{png}}
		case "none", "":
			return clipMsg{win: w, err: fmt.Errorf("no picture or file on the clipboard")}
		default:
			var paths []string
			for _, p := range strings.Split(r, "\n") {
				if p = strings.TrimSpace(p); isFile(p) {
					paths = append(paths, p)
				}
			}
			if len(paths) == 0 {
				return clipMsg{win: w, err: fmt.Errorf("no picture or file on the clipboard")}
			}
			return clipMsg{win: w, paths: paths}
		}
	}
}

func (m *Model) onClip(x clipMsg) {
	if x.err != nil {
		x.win.note(m.o.Now(), "-!- %v", x.err)
		return
	}
	m.stageFiles(x.win, x.paths)
}

// sendStaged sends the staged files with the typed text, as plain text
// would be sent.
func (m *Model) sendStaged(w *window, body string) tea.Cmd {
	target, ok := w.replyTarget()
	if !ok {
		w.note(m.o.Now(), "-!- No messages here yet to answer with a file.")
		return nil
	}
	paths := w.staged
	w.staged = nil
	return m.sendFiles(w, target, paths, body)
}

func (m *Model) stagedLine(w *window) string {
	names := make([]string, len(w.staged))
	for i, p := range w.staged {
		names[i] = filepath.Base(p)
	}
	return dim.Render("📎 "+strings.Join(names, ", ")) + dim.Render("  · Enter sends · Ctrl+X clears")
}
