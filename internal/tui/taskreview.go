package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/messages"
	"github.com/base698/amythest/internal/taskreview"
	"github.com/base698/amythest/internal/tasks"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type taskReviewDone struct {
	owner  *taskReviewView
	result taskreview.Result
	err    error
}
type taskReviewView struct {
	client   *apiclient.Client
	selected []tasks.Task
	input    textinput.Model
	provider string
	step     int
	result   taskreview.Result
	err      error
	cancel   context.CancelFunc
	scroll   int
}

func newTaskReviewView(c *apiclient.Client, selected []tasks.Task) *taskReviewView {
	i := textinput.New()
	i.Prompt = "Review prompt: "
	i.Placeholder = taskreview.DefaultPrompt
	i.CharLimit = 2000
	return &taskReviewView{client: c, selected: selected, input: i, provider: "claude"}
}
func (v *taskReviewView) Title() string {
	return fmt.Sprintf("review %d marked tasks", len(v.selected))
}
func (v *taskReviewView) Busy() bool      { return v.step == 2 }
func (v *taskReviewView) Capturing() bool { return true }
func (v *taskReviewView) Init() tea.Cmd   { return v.input.Focus() }
func (v *taskReviewView) Cancel() {
	if v.cancel != nil {
		v.cancel()
	}
}
func (v *taskReviewView) Update(msg tea.Msg) (view, tea.Cmd) {
	if done, ok := msg.(taskReviewDone); ok && done.owner == v {
		v.Cancel()
		v.step = 3
		v.result, v.err = done.result, done.err
		return v, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		if k.String() == "esc" {
			if v.step == 2 {
				v.Cancel()
				return v, nil
			}
			return v, popView()
		}
		switch v.step {
		case 0:
			if k.String() == "enter" {
				v.step = 1
				v.input.Blur()
				return v, nil
			}
			var cmd tea.Cmd
			v.input, cmd = v.input.Update(k)
			return v, cmd
		case 1:
			switch k.String() {
			case "tab", "j", "k", "up", "down", "left", "right":
				if v.provider == "claude" {
					v.provider = "codex"
				} else {
					v.provider = "claude"
				}
			case "enter":
				v.step = 2
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				v.cancel = cancel
				selected := append([]tasks.Task{}, v.selected...)
				provider, prompt := v.provider, v.input.Value()
				return v, func() tea.Msg {
					r, e := taskreview.Review(ctx, v.client, selected, provider, prompt, taskreview.RunCLI)
					return taskReviewDone{v, r, e}
				}
			}
		case 3:
			switch k.String() {
			case "j", "down":
				v.scroll++
			case "k", "up":
				v.scroll = max(0, v.scroll-1)
			case "pgdown", "ctrl+d":
				v.scroll += 10
			case "pgup", "ctrl+u":
				v.scroll = max(0, v.scroll-10)
			}
		}
	}
	return v, nil
}
func (v *taskReviewView) View(w, h int) string {
	v.input.Width = max(20, w-20)
	var lines []string
	lines = append(lines, "Investigate selected tasks and save evidence below each task.", "Task status stays unchanged.", "")
	switch v.step {
	case 0:
		lines = append(lines, v.input.View(), "Enter: continue with this prompt (blank uses the default)", "Esc: back")
	case 1:
		lines = append(lines, "Agent: "+v.provider, "Tab: Claude / Codex · Enter: investigate · Esc: back")
	case 2:
		lines = append(lines, "Investigating with "+v.provider+"…", "Esc: cancel; no automatic task completion")
	case 3:
		lines = append(lines, fmt.Sprintf("Saved context for %d tasks.", v.result.Saved))
		if v.err != nil {
			lines = append(lines, v.err.Error())
		}
		lines = append(lines, v.result.Errors...)
		for _, f := range v.result.Findings {
			if f.ID < 0 || f.ID >= len(v.selected) {
				continue
			}
			lines = append(lines, "", v.selected[f.ID].Text, f.Summary)
			lines = append(lines, f.Sources...)
		}
		lines = append(lines, "", "Esc: return to tasks · D: cancel/delete there")
	}
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, wrapLine(messages.Plain(line), max(20, w-4))...)
	}
	v.scroll = min(v.scroll, max(0, len(wrapped)-max(1, h-2)))
	return "\n " + strings.Join(wrapped[v.scroll:min(len(wrapped), v.scroll+max(1, h-2))], "\n ")
}
