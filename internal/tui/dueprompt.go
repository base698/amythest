package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/tasks"
)

// editSavedMsg announces a completed task edit; summary names what changed.
type editSavedMsg struct{ summary string }

var isoDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// duePrompt is the inline task editor: step one edits the 📅 due date, step
// two the 🔁 recurrence rule (Obsidian notation). The server only lets a
// client change task metadata — the text lives in the note.
type duePrompt struct {
	input  textinput.Model
	task   tasks.Task
	typing bool
	step   int // 0 = due, 1 = repeat
	due    string
	dueChanged bool
}

func newDuePrompt() duePrompt {
	ti := textinput.New()
	ti.CharLimit = 200
	return duePrompt{input: ti}
}

func (p *duePrompt) active() bool { return p.typing }

func (p *duePrompt) start(t tasks.Task) tea.Cmd {
	p.typing = true
	p.task = t
	p.step = 0
	p.due = t.Due
	p.dueChanged = false
	p.input.Prompt = "due: "
	p.input.Placeholder = "YYYY-MM-DD · today · tomorrow · +3 · empty clears"
	p.input.SetValue(t.Due)
	return p.input.Focus()
}

func (p *duePrompt) startRepeatStep() tea.Cmd {
	p.step = 1
	p.input.Prompt = "repeat: "
	p.input.Placeholder = `every 4 days · every week on wed,sat · append "when done" · empty = none`
	p.input.SetValue(p.task.Recurrence)
	return p.input.Focus()
}

// handleKey consumes keys while the prompt is open. When the user commits
// the final step it returns the save command; otherwise cmd drives the input.
func (p *duePrompt) handleKey(msg tea.KeyMsg, client *apiclient.Client, now time.Time) (save tea.Cmd, cmd tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		if p.step == 1 {
			// Back to the due step rather than abandoning the edit.
			p.step = 0
			p.input.Prompt = "due: "
			p.input.SetValue(p.due)
			return nil, nil
		}
		p.typing = false
		p.input.Blur()
		return nil, nil
	case tea.KeyEnter:
		if p.step == 0 {
			due, err := parseDueInput(p.input.Value(), now)
			if err != nil {
				return nil, flash(err.Error())
			}
			p.due = due
			p.dueChanged = due != p.task.Due
			return nil, p.startRepeatStep()
		}
		rule := strings.TrimSpace(p.input.Value())
		if rule != "" && !tasks.ValidRecurrence(rule) {
			return nil, flash(fmt.Sprintf("bad repeat rule %q (every 4 days · every week on wed,sat · … when done)", rule))
		}
		p.typing = false
		p.input.Blur()
		ruleChanged := rule != p.task.Recurrence
		if !p.dueChanged && !ruleChanged {
			return nil, flash("no changes")
		}
		return saveTaskEditCmd(client, p.task, p.due, p.dueChanged, rule, ruleChanged), nil
	}
	p.input, cmd = p.input.Update(msg)
	return nil, cmd
}

func (p *duePrompt) view() string {
	return p.input.View()
}

// saveTaskEditCmd applies the changed fields sequentially. The file version
// changes after the first write, so the second write re-finds the task by
// (slug, text) for a fresh version — same identity trick as toggle retries.
func saveTaskEditCmd(client *apiclient.Client, t tasks.Task, due string, dueChanged bool, rule string, ruleChanged bool) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		current := t
		var changed []string
		if dueChanged {
			err := client.SetTaskDue(ctx, current, due)
			if errors.Is(err, apiclient.ErrConflict) {
				fresh, ferr := refetchTask(ctx, client, current)
				if ferr != nil {
					return fail(ferr)
				}
				err = client.SetTaskDue(ctx, fresh, due)
			}
			if err != nil {
				return fail(err)
			}
			changed = append(changed, "due "+displayOrNone(due))
		}
		if ruleChanged {
			fresh := current
			if dueChanged {
				var err error
				fresh, err = refetchTask(ctx, client, current)
				if err != nil {
					return fail(fmt.Errorf("due saved, but refreshing for the repeat edit failed: %w", err))
				}
			}
			err := client.SetTaskRecurrence(ctx, fresh, rule)
			if errors.Is(err, apiclient.ErrConflict) {
				retry, ferr := refetchTask(ctx, client, current)
				if ferr != nil {
					return fail(ferr)
				}
				err = client.SetTaskRecurrence(ctx, retry, rule)
			}
			if err != nil {
				return fail(err)
			}
			changed = append(changed, "repeat "+displayOrNone(rule))
		}
		return editSavedMsg{summary: strings.Join(changed, " · ") + " ✓"}
	}
}

func refetchTask(ctx context.Context, client *apiclient.Client, t tasks.Task) (tasks.Task, error) {
	groups, err := client.ListTasks(ctx, "description includes "+t.Text)
	if err != nil {
		return tasks.Task{}, err
	}
	fresh, ok := findTask(groups, t.Slug, t.Text)
	if !ok {
		return tasks.Task{}, fmt.Errorf("task changed on server; refresh (r) and retry")
	}
	return fresh, nil
}

func displayOrNone(value string) string {
	if value == "" {
		return "cleared"
	}
	return value
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
