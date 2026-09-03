package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// themePickerView (global key T) lists built-in and cli.yaml themes. Moving
// the cursor applies the theme immediately — the whole UI is the preview —
// enter keeps it for the session, esc restores what you came in with.
type themePickerView struct {
	names    []string
	cursor   int
	original string
}

func newThemePickerView() *themePickerView {
	v := &themePickerView{names: ThemeNames(), original: CurrentTheme()}
	for i, n := range v.names {
		if n == CurrentTheme() {
			v.cursor = i
		}
	}
	return v
}

func (v *themePickerView) Title() string { return "theme" }
func (v *themePickerView) Busy() bool    { return false }

// Capturing: the picker owns esc so it can restore the original theme.
func (v *themePickerView) Capturing() bool { return true }

func (v *themePickerView) Init() tea.Cmd { return nil }

func (v *themePickerView) Update(msg tea.Msg) (view, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return v, nil
	}
	switch key.String() {
	case "j", "down":
		if v.cursor < len(v.names)-1 {
			v.cursor++
			SetTheme(v.names[v.cursor])
		}
	case "k", "up":
		if v.cursor > 0 {
			v.cursor--
			SetTheme(v.names[v.cursor])
		}
	case "enter":
		name := v.names[v.cursor]
		SetTheme(name)
		return v, tea.Batch(popView(),
			flash("theme: "+name+" — add 'theme: "+name+"' to cli.yaml to keep it"))
	case "esc", "q", "T":
		SetTheme(v.original)
		return v, popView()
	}
	return v, nil
}

// themeSwatch renders sample chips in a theme's own colors so every row
// previews itself even while another theme is applied.
func themeSwatch(t Theme) string {
	t = resolve(t)
	chip := func(color string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("▮")
	}
	return chip(t.Accent) + chip(t.Title) + chip(t.Due) + chip(t.Blocked) + chip(t.Link) + chip(t.Dim)
}

func themeByName(name string) Theme {
	if t, ok := customThemes[name]; ok {
		return t
	}
	return builtinThemes[name]
}

func (v *themePickerView) View(width, height int) string {
	var b strings.Builder
	b.WriteString("\n " + columnTitleStyle.Render("Theme") + "  " +
		dimStyle.Render("moving previews live") + "\n\n")
	for i, name := range v.names {
		prefix := "   "
		label := name
		if i == v.cursor {
			prefix = cursorStyle.Render(" ▸ ")
			label = cursorStyle.Render(name)
		}
		marker := ""
		if name == v.original {
			marker = dimStyle.Render("  (current)")
		}
		if _, isCustom := customThemes[name]; isCustom {
			marker += dimStyle.Render("  [cli.yaml]")
		}
		b.WriteString(prefix + label + "  " + themeSwatch(themeByName(name)) + marker + "\n")
	}
	b.WriteString("\n" + dimStyle.Render(" enter keep for this session · esc cancel · persist with 'theme: <name>' in cli.yaml"))
	return b.String()
}
