package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/source"
)

// sourcesView (key 0) shows each registered source's health: connected,
// stubbed, or missing credentials — the config-flow surface.
type sourcesView struct {
	reg    *source.Registry
	rows   []sourceHealthRow
	busy   bool
	loaded bool
}

type sourceHealthRow struct {
	name   string
	health source.Health
}

type sourcesLoadedMsg struct{ rows []sourceHealthRow }

func newSourcesView(reg *source.Registry) *sourcesView {
	return &sourcesView{reg: reg}
}

func (v *sourcesView) Title() string   { return "sources" }
func (v *sourcesView) Busy() bool      { return v.busy }
func (v *sourcesView) Capturing() bool { return false }

func (v *sourcesView) Init() tea.Cmd {
	v.busy = true
	reg := v.reg
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var rows []sourceHealthRow
		for _, s := range reg.All() {
			rows = append(rows, sourceHealthRow{name: s.Name(), health: s.Health(ctx)})
		}
		return sourcesLoadedMsg{rows}
	}
}

func (v *sourcesView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case sourcesLoadedMsg:
		v.busy = false
		v.loaded = true
		v.rows = msg.rows
		return v, nil
	case errMsg:
		v.busy = false
		return v, nil
	case tea.KeyMsg:
		if msg.String() == "r" {
			return v, v.Init()
		}
	}
	return v, nil
}

func (v *sourcesView) View(width, height int) string {
	if !v.loaded {
		return "\n  checking sources…"
	}
	out := "\n"
	for _, row := range v.rows {
		state := ""
		switch row.health.State {
		case source.StateOK:
			state = groupHeaderStyle.Render(string(row.health.State))
		case source.StateStubbed:
			state = dueStyle.Render(string(row.health.State))
		default:
			state = blockedStyle.Render(string(row.health.State))
		}
		out += "  " + columnTitleStyle.Render(row.name) + "  " + state + "  " + dimStyle.Render(row.health.Detail) + "\n"
	}
	have := map[string]bool{}
	for _, row := range v.rows {
		have[row.name] = true
	}
	if !have["jira"] || !have["azboards"] {
		out += "\n" + dimStyle.Render("  add a ticketing source: run `amy source init jira` or `amy source init azboards`, then restart amy")
	}
	out += "\n" + dimStyle.Render("  r refresh · esc back")
	return out
}
