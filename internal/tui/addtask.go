package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
)

// addTaskView is the "+" quick-add: type the task, then pick where it goes.
// Today's daily note is the first option; searching the vault offers any
// other note (a tasks-inbox note, a project file, …) as the destination.
type addTaskView struct {
	client  *apiclient.Client
	text    textinput.Model
	search  textinput.Model
	step    int // 0 = typing the task, 1 = choosing the destination
	typing  bool
	results []apiclient.SearchResult
	cursor  int // 0 = daily note, 1..n = results[cursor-1]
	busy    bool
}

type addTaskResultsMsg struct{ results []apiclient.SearchResult }

// taskAddedMsg announces a successful append so task lists refresh.
type taskAddedMsg struct{ path string }

func newAddTaskView(client *apiclient.Client) *addTaskView {
	text := textinput.New()
	text.Prompt = "new task: "
	text.CharLimit = 2000
	search := textinput.New()
	search.Prompt = "search notes: "
	search.CharLimit = 200
	return &addTaskView{client: client, text: text, search: search}
}

func (v *addTaskView) Title() string   { return "add task" }
func (v *addTaskView) Busy() bool      { return v.busy }
func (v *addTaskView) Capturing() bool { return v.step == 0 || v.typing }

func (v *addTaskView) Init() tea.Cmd { return v.text.Focus() }

func (v *addTaskView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case addTaskResultsMsg:
		v.busy = false
		v.results = msg.results
		if len(v.results) > 0 {
			v.cursor = 1
		}
		return v, nil

	case taskAddedMsg:
		v.busy = false
		return v, popView()

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.step == 0 {
			switch msg.Type {
			case tea.KeyEsc:
				return v, popView()
			case tea.KeyEnter:
				if strings.TrimSpace(v.text.Value()) == "" {
					return v, nil
				}
				v.step = 1
				v.text.Blur()
				return v, nil
			}
			var cmd tea.Cmd
			v.text, cmd = v.text.Update(msg)
			return v, cmd
		}
		if v.typing {
			switch msg.Type {
			case tea.KeyEsc:
				v.typing = false
				v.search.Blur()
				return v, nil
			case tea.KeyEnter:
				query := strings.TrimSpace(v.search.Value())
				v.typing = false
				v.search.Blur()
				if query == "" {
					return v, nil
				}
				v.busy = true
				client := v.client
				return v, func() tea.Msg {
					results, err := client.SearchNotes(context.Background(), query)
					if err != nil {
						return fail(err)
					}
					return addTaskResultsMsg{results}
				}
			}
			var cmd tea.Cmd
			v.search, cmd = v.search.Update(msg)
			return v, cmd
		}
		switch msg.String() {
		case "esc":
			v.step = 0
			return v, v.text.Focus()
		case "j", "down":
			if v.cursor < len(v.results) {
				v.cursor++
			}
		case "k", "up":
			if v.cursor > 0 {
				v.cursor--
			}
		case "/", "i":
			v.typing = true
			return v, v.search.Focus()
		case "enter":
			return v.submit()
		}
	}
	return v, nil
}

func (v *addTaskView) submit() (view, tea.Cmd) {
	if v.busy {
		return v, nil
	}
	text := strings.TrimSpace(v.text.Value())
	daily := v.cursor == 0
	slug := ""
	if !daily {
		slug = v.results[v.cursor-1].Slug
	}
	v.busy = true
	client := v.client
	return v, func() tea.Msg {
		path, err := client.AddTask(context.Background(), slug, daily, text)
		if err != nil {
			return fail(err)
		}
		return taskAddedMsg{path: path}
	}
}

func (v *addTaskView) View(width, height int) string {
	var b strings.Builder
	b.WriteString(" " + v.text.View() + "\n\n")
	if v.step == 0 {
		b.WriteString(dimStyle.Render(" enter continue · esc cancel"))
		return b.String()
	}
	b.WriteString(" " + groupHeaderStyle.Render("add to:") + "\n")
	daily := "Today's daily note"
	if v.cursor == 0 {
		b.WriteString(cursorStyle.Render(" ▸ "+daily) + "\n")
	} else {
		b.WriteString("   " + daily + "\n")
	}
	for i, result := range v.results {
		title := result.Title + "  " + dimStyle.Render(result.Slug)
		if v.cursor == i+1 {
			b.WriteString(cursorStyle.Render(" ▸ "+result.Title) + "  " + dimStyle.Render(result.Slug) + "\n")
		} else {
			b.WriteString("   " + title + "\n")
		}
	}
	b.WriteString("\n " + v.search.View() + "\n")
	hint := " enter add · / search other notes · j/k choose · esc back"
	if v.typing {
		hint = " enter search · esc done typing"
	}
	b.WriteString(dimStyle.Render(hint))
	return b.String()
}
