package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
)

// Quick capture: one line in, one task appended to today's daily note, out.
// It is the herdr popup entrypoint (prefix+a style) and also works as a
// plain CLI one-shot: `amy capture "call the vet"`.
//
// Deliberately not the full add-task flow from the TUI — a popup that asks
// where to file the task defeats the point of capturing without thinking.

func runCapture(args []string) {
	// Split flags from free text: any non-flag word is task text, so
	// `amy capture -endpoint URL call the vet` captures "call the vet".
	valueFlags := map[string]bool{"-config": true, "--config": true, "-endpoint": true, "--endpoint": true}
	var text, flags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			text = append(text, arg)
			continue
		}
		flags = append(flags, arg)
		// "-config path" needs its value; "-config=path" carries its own.
		if valueFlags[arg] && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	cfg, err := apiclient.LoadConfig(flags)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	client := apiclient.New(cfg)

	if line := strings.TrimSpace(strings.Join(text, " ")); line != "" {
		path, err := addCaptured(client, line)
		if err != nil {
			fmt.Fprintln(os.Stderr, "capture:", err)
			os.Exit(1)
		}
		fmt.Println("added to", path)
		return
	}

	model := captureModel{client: client, where: herdrContext()}
	input := textinput.New()
	input.Prompt = "› "
	input.CharLimit = 2000
	input.Width = 60
	input.Focus()
	model.input = input

	program := tea.NewProgram(model)
	final, err := program.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if m, ok := final.(captureModel); ok && m.err != nil {
		os.Exit(1)
	}
}

func addCaptured(client *apiclient.Client, text string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return client.AddTask(ctx, "", true, text)
}

type captureModel struct {
	client *apiclient.Client
	input  textinput.Model
	where  string // herdr workspace/cwd, shown for orientation
	saving bool
	saved  string
	err    error
}

type captureSavedMsg struct{ path string }
type captureErrMsg struct{ err error }

func (m captureModel) Init() tea.Cmd { return textinput.Blink }

func (m captureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case captureSavedMsg:
		m.saving = false
		m.saved = msg.path
		// Let the confirmation land before the popup closes.
		return m, tea.Sequence(tea.Tick(600*time.Millisecond, func(time.Time) tea.Msg {
			return tea.Quit()
		}), tea.Quit)

	case captureErrMsg:
		m.saving, m.err = false, msg.err
		return m, nil

	case tea.KeyMsg:
		if m.saving {
			return m, nil
		}
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			line := strings.TrimSpace(m.input.Value())
			if line == "" {
				return m, tea.Quit
			}
			m.saving, m.err = true, nil
			client := m.client
			return m, func() tea.Msg {
				path, err := addCaptured(client, line)
				if err != nil {
					return captureErrMsg{err}
				}
				return captureSavedMsg{path}
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

var (
	captureTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	captureDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	captureOK    = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	captureBad   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

func (m captureModel) View() string {
	var b strings.Builder
	b.WriteString("\n  " + captureTitle.Render("capture") + "  " + captureDim.Render("→ today's daily note"))
	if m.where != "" {
		b.WriteString(captureDim.Render("  ·  " + m.where))
	}
	b.WriteString("\n\n  " + m.input.View() + "\n\n")
	switch {
	case m.saved != "":
		b.WriteString("  " + captureOK.Render("✓ added to "+m.saved))
	case m.saving:
		b.WriteString("  " + captureDim.Render("saving…"))
	case m.err != nil:
		b.WriteString("  " + captureBad.Render("✗ "+m.err.Error()) + "\n  " + captureDim.Render("enter retry · esc discard"))
	default:
		b.WriteString("  " + captureDim.Render("enter save · esc discard"))
	}
	return b.String() + "\n"
}

// herdrContext summarizes where the capture was invoked from, so a popup
// that covers the screen still says which workspace you came from. Empty
// outside herdr.
func herdrContext() string {
	raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")
	if raw == "" {
		return ""
	}
	var ctx struct {
		Workspace struct {
			Name string `json:"name"`
		} `json:"workspace"`
		Worktree struct {
			Branch string `json:"branch"`
		} `json:"worktree"`
		Pane struct {
			Cwd string `json:"cwd"`
		} `json:"pane"`
	}
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return ""
	}
	var parts []string
	if ctx.Workspace.Name != "" {
		parts = append(parts, ctx.Workspace.Name)
	}
	if ctx.Worktree.Branch != "" {
		parts = append(parts, ctx.Worktree.Branch)
	} else if ctx.Pane.Cwd != "" {
		parts = append(parts, filepath.Base(ctx.Pane.Cwd))
	}
	return strings.Join(parts, " · ")
}
