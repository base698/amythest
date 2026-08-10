package tui

import (
	"context"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
)

// notesView is the note-finding screen: type to search the vault (server FTS),
// enter opens the selected note.
type notesView struct {
	client  *apiclient.Client
	input   textinput.Model
	typing  bool
	results []apiclient.SearchResult
	cursor  int
	busy    bool
}

type notesFoundMsg struct {
	query   string
	results []apiclient.SearchResult
}

func newNotesView(client *apiclient.Client) *notesView {
	ti := textinput.New()
	ti.Prompt = "search notes: "
	ti.CharLimit = 200
	return &notesView{client: client, input: ti, typing: true}
}

func (v *notesView) Title() string   { return "notes" }
func (v *notesView) Busy() bool      { return v.busy }
func (v *notesView) Capturing() bool { return v.typing }

func (v *notesView) Init() tea.Cmd { return v.input.Focus() }

func (v *notesView) searchCmd(query string) tea.Cmd {
	client := v.client
	return func() tea.Msg {
		results, err := client.SearchNotes(context.Background(), query)
		if err != nil {
			return fail(err)
		}
		return notesFoundMsg{query: query, results: results}
	}
}

func (v *notesView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case notesFoundMsg:
		v.busy = false
		v.results = msg.results
		v.cursor = 0
		return v, nil

	case openNoteMsg:
		v.busy = false
		return v, pushView(newNoteView(v.client, msg.note))

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.typing {
			switch msg.Type {
			case tea.KeyEsc:
				v.typing = false
				v.input.Blur()
				return v, nil
			case tea.KeyEnter:
				query := strings.TrimSpace(v.input.Value())
				if query == "" {
					return v, nil
				}
				v.typing = false
				v.input.Blur()
				v.busy = true
				return v, v.searchCmd(query)
			}
			var cmd tea.Cmd
			v.input, cmd = v.input.Update(msg)
			return v, cmd
		}
		switch msg.String() {
		case "j", "down":
			if v.cursor < len(v.results)-1 {
				v.cursor++
			}
		case "k", "up":
			if v.cursor > 0 {
				v.cursor--
			}
		case "/", "i":
			v.typing = true
			return v, v.input.Focus()
		case "enter":
			if v.cursor < len(v.results) {
				v.busy = true
				return v, openNoteCmd(v.client, v.results[v.cursor].Slug)
			}
		}
	}
	return v, nil
}

var tagRe = regexp.MustCompile(`</?b>`)

func (v *notesView) View(width, height int) string {
	var b strings.Builder
	b.WriteString(" " + v.input.View() + "\n\n")
	if v.busy {
		b.WriteString("  searching…\n")
	}
	if !v.typing && !v.busy && len(v.results) == 0 && v.input.Value() != "" {
		b.WriteString("  no notes matched\n")
	}
	for i, result := range v.results {
		prefix := "   "
		title := result.Title
		if i == v.cursor && !v.typing {
			prefix = cursorStyle.Render(" ▸ ")
			title = cursorStyle.Render(title)
		}
		if result.Archived {
			title += dimStyle.Render(" (archived)")
		}
		b.WriteString(prefix + title + "  " + dimStyle.Render(result.Slug) + "\n")
		excerpt := htmlUnescape(tagRe.ReplaceAllString(result.Excerpt, ""))
		for _, row := range wrapLine("     "+excerpt, max(30, width-4)) {
			b.WriteString(dimStyle.Render(row) + "\n")
		}
	}
	if !v.typing {
		b.WriteString("\n" + dimStyle.Render(" enter open · / new search · esc back"))
	}
	return b.String()
}

func htmlUnescape(s string) string {
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'", "&quot;", `"`)
	return replacer.Replace(s)
}

func openNoteCmd(client *apiclient.Client, ref string) tea.Cmd {
	return func() tea.Msg {
		note, err := client.GetNote(context.Background(), ref)
		if err != nil {
			return fail(err)
		}
		return openNoteMsg{note}
	}
}

type openNoteMsg struct{ note *apiclient.Note }
