package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// finder gives a view vim/less-style search: "/" opens the prompt, enter
// commits the query, n/N jump to the next/previous match (wrapping), esc
// clears. Views own their list of searchable strings and cursor movement;
// finder owns the prompt state and match arithmetic.
type finder struct {
	input  textinput.Model
	typing bool
	query  string
}

func newFinder() finder {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 128
	return finder{input: ti}
}

// active reports whether the prompt is open and should capture all keys.
func (f *finder) active() bool { return f.typing }

func (f *finder) start() tea.Cmd {
	f.typing = true
	f.input.SetValue("")
	return f.input.Focus()
}

// handleKey consumes a key while the prompt is open. committed is true when
// the user pressed enter with a non-empty query.
func (f *finder) handleKey(msg tea.KeyMsg) (committed bool, cmd tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		f.typing = false
		f.query = strings.TrimSpace(f.input.Value())
		f.input.Blur()
		return f.query != "", nil
	case tea.KeyEsc:
		f.typing = false
		f.query = ""
		f.input.Blur()
		return false, nil
	}
	f.input, cmd = f.input.Update(msg)
	return false, cmd
}

// bar renders the prompt line, or the committed query as a reminder.
func (f *finder) bar() string {
	if f.typing {
		return f.input.View()
	}
	if f.query != "" {
		return dimStyle.Render("/" + f.query + "  (n next · N prev · / again)")
	}
	return ""
}

func matches(text, query string) bool {
	return query != "" && strings.Contains(strings.ToLower(text), strings.ToLower(query))
}

// findMatch returns the index of the next item matching query, scanning from
// (from+dir) with wraparound, or -1 when nothing matches.
func findMatch(items []string, from, dir int, query string) int {
	if query == "" || len(items) == 0 {
		return -1
	}
	n := len(items)
	for step := 1; step <= n; step++ {
		i := ((from+dir*step)%n + n) % n
		if matches(items[i], query) {
			return i
		}
	}
	return -1
}

// highlight wraps the first match of query inside line with the search style.
func highlight(line, query string) string {
	if query == "" {
		return line
	}
	lower := strings.ToLower(line)
	idx := strings.Index(lower, strings.ToLower(query))
	if idx < 0 {
		return line
	}
	end := idx + len(query)
	return line[:idx] + searchHitStyle.Render(line[idx:end]) + line[end:]
}
