package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/tasks"
)

type dueSavedMsg struct{}

var isoDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// duePrompt is the inline "edit" for vault tasks. The server only lets a
// client change a task's metadata (its text lives in the note), so editing a
// task means editing its 📅 due date: ISO date, today/tomorrow, +N days, or
// empty to clear.
type duePrompt struct {
	input  textinput.Model
	task   tasks.Task
	typing bool
}

func newDuePrompt() duePrompt {
	ti := textinput.New()
	ti.Prompt = "due: "
	ti.Placeholder = "YYYY-MM-DD · today · tomorrow · +3 · empty clears"
	ti.CharLimit = 32
	return duePrompt{input: ti}
}

func (p *duePrompt) active() bool { return p.typing }

func (p *duePrompt) start(t tasks.Task) tea.Cmd {
	p.typing = true
	p.task = t
	p.input.SetValue(t.Due)
	return p.input.Focus()
}

// handleKey consumes keys while the prompt is open. When the user commits a
// valid value it returns the save command; otherwise cmd drives the input.
func (p *duePrompt) handleKey(msg tea.KeyMsg, client *apiclient.Client, now time.Time) (save tea.Cmd, cmd tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		p.typing = false
		p.input.Blur()
		return nil, nil
	case tea.KeyEnter:
		due, err := parseDueInput(p.input.Value(), now)
		if err != nil {
			return nil, flash(err.Error())
		}
		p.typing = false
		p.input.Blur()
		task := p.task
		return func() tea.Msg {
			if err := client.SetTaskDue(context.Background(), task, due); err != nil {
				return fail(err)
			}
			return dueSavedMsg{}
		}, nil
	}
	p.input, cmd = p.input.Update(msg)
	return nil, cmd
}

func (p *duePrompt) view() string {
	return p.input.View()
}

func parseDueInput(raw string, now time.Time) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "":
		return "", nil
	case "today":
		return now.Format("2006-01-02"), nil
	case "tomorrow":
		return now.AddDate(0, 0, 1).Format("2006-01-02"), nil
	}
	if strings.HasPrefix(value, "+") {
		var days int
		if _, err := fmt.Sscanf(value, "+%d", &days); err == nil && days >= 0 {
			return now.AddDate(0, 0, days).Format("2006-01-02"), nil
		}
	}
	if isoDateRe.MatchString(value) {
		if _, err := time.Parse("2006-01-02", value); err == nil {
			return value, nil
		}
	}
	return "", fmt.Errorf("bad due date %q (YYYY-MM-DD, today, tomorrow, +N, or empty)", raw)
}
