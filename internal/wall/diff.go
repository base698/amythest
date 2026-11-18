package wall

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Fullscreen lets the dashboard lend the diff both panels until Escape.
func (m *model) Fullscreen() bool { return m.view == "diff" }

type diffLine struct {
	text, file, oldFile string
	kind                byte
	old, new            int
	start, deleted      bool
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// Keep one display row per patch line so paging, file jumps and opening a
// file all refer to the same position, even with lines scrolled sideways.
func parseDiff(lines []string) []diffLine {
	rows := make([]diffLine, len(lines))
	start, old, next := -1, 0, 0
	file, oldFile := "", ""
	deleted, inHunk := false, false
	finish := func(end int) {
		for i := max(0, start); start >= 0 && i < end; i++ {
			rows[i].file, rows[i].oldFile, rows[i].deleted = file, oldFile, deleted
		}
	}
	for i, line := range lines {
		row := diffLine{text: line}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			finish(i)
			start, inHunk, deleted = i, false, false
			oldFile, file = diffPaths(strings.TrimPrefix(line, "diff --git "))
			row.start, row.kind = true, 'f'
		case !inHunk && strings.HasPrefix(line, "--- "):
			if start < 0 {
				start, row.start = i, true
			}
			oldFile = patchPath(strings.TrimPrefix(line, "--- "))
			row.kind = 'm'
		case !inHunk && strings.HasPrefix(line, "+++ "):
			file = patchPath(strings.TrimPrefix(line, "+++ "))
			deleted = file == "/dev/null"
			if deleted {
				file = oldFile
			}
			row.kind = 'm'
		case strings.HasPrefix(line, "deleted file mode "):
			deleted, file, row.kind = true, oldFile, 'm'
		case hunkHeader.MatchString(line):
			match := hunkHeader.FindStringSubmatch(line)
			old, _ = strconv.Atoi(match[1])
			next, _ = strconv.Atoi(match[2])
			inHunk, row.kind = true, '@'
		case inHunk && strings.HasPrefix(line, "+"):
			row.kind, row.new = '+', next
			next++
		case inHunk && strings.HasPrefix(line, "-"):
			row.kind, row.old = '-', old
			old++
		case inHunk && strings.HasPrefix(line, " "):
			row.kind, row.old, row.new = ' ', old, next
			old++
			next++
		default:
			row.kind = 'm'
		}
		rows[i] = row
	}
	finish(len(rows))
	return rows
}

// Git quotes paths with spaces/non-ASCII bytes when needed. +++/--- supply
// the definitive paths; diff --git also covers binary-only patches.
func patchPath(s string) string {
	if strings.HasPrefix(s, `"`) {
		if end := quotedEnd(s); end > 0 {
			if unquoted, err := strconv.Unquote(s[:end]); err == nil {
				s = unquoted
			}
		}
	} else {
		s, _, _ = strings.Cut(s, "\t")
	}
	if strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/") {
		return s[2:]
	}
	return s
}

func quotedEnd(s string) int {
	escaped := false
	for i := 1; i < len(s); i++ {
		if s[i] == '"' && !escaped {
			return i + 1
		}
		if s[i] == '\\' && !escaped {
			escaped = true
		} else {
			escaped = false
		}
	}
	return 0
}

func diffPaths(s string) (string, string) {
	if strings.HasPrefix(s, `"`) {
		if end := quotedEnd(s); end > 0 {
			return patchPath(s[:end]), patchPath(strings.TrimSpace(s[end:]))
		}
	}
	for _, sep := range []string{` "b/`, " b/"} {
		if i := strings.Index(s, sep); i >= 0 {
			return patchPath(s[:i]), patchPath(s[i+1:])
		}
	}
	return "", ""
}

func (r diffLine) render(width, left int) string {
	if r.start {
		return " " + strong.Render(clip("▸ "+r.file, width-1))
	}
	if r.kind == '+' || r.kind == '-' || r.kind == ' ' {
		number := func(n int) string {
			if n == 0 {
				return "     "
			}
			return fmt.Sprintf("%5d", n)
		}
		gutter := " " + number(r.old) + " " + number(r.new) + " │ "
		text := r.text
		if len(text) > 0 {
			text = text[1:]
		}
		avail := max(0, width-lipgloss.Width(gutter)-2)
		text = ansi.Cut(text, left, left+avail)
		line := string(r.kind) + text
		switch r.kind {
		case '+':
			line = added.Render(line)
		case '-':
			line = bad.Render(line)
		}
		return ansi.Truncate(dim.Render(gutter)+line, width, "")
	}
	text := " " + clip(r.text, width-1)
	if r.kind == '@' {
		return accent.Render(text)
	}
	return dim.Render(text)
}

func (m *model) diffTarget() *diffLine {
	// The first visible file owns the open action; the header names it.
	for i := m.diffTop; i < min(len(m.patch), m.diffTop+m.body()); i++ {
		if m.patch[i].file != "" {
			return &m.patch[i]
		}
	}
	return nil
}

func (m *model) jumpFile(direction int) {
	for i := m.diffTop + direction; i >= 0 && i < len(m.patch); i += direction {
		if m.patch[i].start {
			m.diffTop, m.diffLeft = i, 0
			return
		}
	}
}

func (m *model) openDiffFile() tea.Cmd {
	row := m.diffTarget()
	if row == nil {
		return m.say("Scroll to a file or press ] for the next file")
	}
	repo := githubRepo(m.diffItem.Art.Repo)
	ref := m.diffItem.Art.Ref
	if repo == "" || ref == "" {
		return m.say("No GitHub address recorded for this repository")
	}
	file, line := row.file, row.new
	if row.deleted || row.kind == '-' {
		file, line, ref = row.oldFile, row.old, ref+"^"
	}
	parts := strings.Split(file, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	address := "https://github.com/" + repo + "/blob/" + url.PathEscape(ref) + "/" + strings.Join(parts, "/")
	if line > 0 {
		address += fmt.Sprintf("#L%d", line)
	}
	if m.o.Open == nil {
		return m.say(address)
	}
	if err := m.o.Open(address); err != nil {
		return m.say("Could not open file: " + err.Error())
	}
	return m.say("Opened " + file + " in browser")
}
