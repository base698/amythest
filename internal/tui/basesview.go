package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
)

// basesView (key 6) is the dataview surface: the vault's .base files
// rendered as tables. v cycles a base's views, enter on a row opens its
// note, e edits a frontmatter property on the row's note.
type basesView struct {
	client *apiclient.Client

	names      []string
	nameCursor int

	base    *apiclient.BaseData
	viewIdx int
	rows    []baseRow // flattened groups for cursor navigation
	cursor  int
	offset  int

	busy   bool
	loaded bool
	find   finder

	prop     textinput.Model
	propping bool
}

// baseRow is one navigable table line: a group header or a data row.
type baseRow struct {
	header string
	cells  []string
	slug   string
}

type basesListMsg struct{ names []string }
type baseDataMsg struct {
	data    *apiclient.BaseData
	viewIdx int
}
type basePropSavedMsg struct{ slug, key string }

func newBasesView(client *apiclient.Client) *basesView {
	prop := textinput.New()
	prop.Prompt = "set property (key=value): "
	prop.CharLimit = 500
	return &basesView{client: client, prop: prop}
}

func (v *basesView) Title() string {
	if v.base != nil {
		label := v.base.Name
		if len(v.base.Views) > 1 && v.viewIdx < len(v.base.Views) {
			label += " · " + v.base.Views[v.viewIdx]
		}
		return "bases › " + label
	}
	return "bases"
}
func (v *basesView) Busy() bool      { return v.busy }
func (v *basesView) Capturing() bool { return v.find.active() || v.propping }

func (v *basesView) Init() tea.Cmd {
	v.busy = true
	client := v.client
	return func() tea.Msg {
		names, err := client.ListBases(context.Background())
		if err != nil {
			return fail(err)
		}
		return basesListMsg{names}
	}
}

func (v *basesView) openCmd(name string, viewIdx int) tea.Cmd {
	client := v.client
	return func() tea.Msg {
		data, err := client.GetBase(context.Background(), name, viewIdx)
		if err != nil {
			return fail(err)
		}
		return baseDataMsg{data: data, viewIdx: viewIdx}
	}
}

func (v *basesView) flatten() {
	v.rows = v.rows[:0]
	if v.base == nil {
		return
	}
	for _, g := range v.base.Data.Groups {
		if g.Name != "" {
			v.rows = append(v.rows, baseRow{header: g.Name})
		}
		for i, cells := range g.Rows {
			row := baseRow{cells: cells}
			if i < len(g.Slugs) {
				row.slug = g.Slugs[i]
			}
			v.rows = append(v.rows, row)
		}
	}
	if v.cursor >= len(v.rows) {
		v.cursor = max(0, len(v.rows)-1)
	}
	// Never rest on a group header.
	if v.cursor < len(v.rows) && v.rows[v.cursor].header != "" {
		v.moveCursor(1)
	}
}

func (v *basesView) current() *baseRow {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}
	return &v.rows[v.cursor]
}

func (v *basesView) searchTexts() []string {
	texts := make([]string, len(v.rows))
	for i, r := range v.rows {
		texts[i] = strings.Join(r.cells, " ")
	}
	return texts
}

func (v *basesView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case basesListMsg:
		v.busy = false
		v.loaded = true
		v.names = msg.names
		return v, nil

	case baseDataMsg:
		v.busy = false
		v.base = msg.data
		v.viewIdx = msg.viewIdx
		v.cursor, v.offset = 0, 0
		v.flatten()
		return v, nil

	case basePropSavedMsg:
		v.busy = true
		return v, tea.Batch(v.openCmd(v.base.Name, v.viewIdx), flash(fmt.Sprintf("%s.%s saved ✓", msg.slug, msg.key)))

	case openNoteMsg:
		v.busy = false
		return v, pushView(newNoteView(v.client, msg.note))

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.find.active() {
			committed, cmd := v.find.handleKey(msg)
			if committed {
				if hit := findMatch(v.searchTexts(), v.cursor, 1, v.find.query); hit >= 0 {
					v.cursor = hit
				} else {
					return v, flash("no match: " + v.find.query)
				}
			}
			return v, cmd
		}
		if v.propping {
			return v.handlePropKey(msg)
		}
		if v.base == nil {
			return v.updateList(msg)
		}
		return v.updateTable(msg)
	}
	return v, nil
}

func (v *basesView) updateList(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if v.nameCursor < len(v.names)-1 {
			v.nameCursor++
		}
	case "k", "up":
		if v.nameCursor > 0 {
			v.nameCursor--
		}
	case "r":
		return v, v.Init()
	case "enter":
		if v.nameCursor < len(v.names) {
			v.busy = true
			return v, v.openCmd(v.names[v.nameCursor], 0)
		}
	}
	return v, nil
}

func (v *basesView) updateTable(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		v.base = nil
		v.rows = nil
		return v, nil
	case "j", "down":
		v.moveCursor(1)
	case "k", "up":
		v.moveCursor(-1)
	case "v":
		if len(v.base.Views) > 1 {
			next := (v.viewIdx + 1) % len(v.base.Views)
			v.busy = true
			return v, v.openCmd(v.base.Name, next)
		}
	case "r":
		v.busy = true
		return v, v.openCmd(v.base.Name, v.viewIdx)
	case "/":
		return v, v.find.start()
	case "n", "N":
		dir := 1
		if msg.String() == "N" {
			dir = -1
		}
		if hit := findMatch(v.searchTexts(), v.cursor, dir, v.find.query); hit >= 0 {
			v.cursor = hit
		}
	case "enter":
		if row := v.current(); row != nil && row.slug != "" {
			v.busy = true
			return v, openNoteCmd(v.client, row.slug)
		}
	case "e":
		if row := v.current(); row != nil && row.slug != "" {
			v.propping = true
			v.prop.SetValue("")
			return v, v.prop.Focus()
		}
	}
	return v, nil
}

func (v *basesView) moveCursor(delta int) {
	next := v.cursor + delta
	for next >= 0 && next < len(v.rows) && v.rows[next].header != "" {
		next += delta
	}
	if next >= 0 && next < len(v.rows) {
		v.cursor = next
	}
}

func (v *basesView) handlePropKey(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		v.propping = false
		v.prop.Blur()
		return v, nil
	case tea.KeyEnter:
		raw := strings.TrimSpace(v.prop.Value())
		v.propping = false
		v.prop.Blur()
		key, value, ok := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		row := v.current()
		if !ok || key == "" || row == nil || row.slug == "" {
			return v, flash("format: key=value")
		}
		v.busy = true
		client, slug := v.client, row.slug
		return v, func() tea.Msg {
			if err := client.SetNoteProperty(context.Background(), slug, key, value); err != nil {
				return fail(err)
			}
			return basePropSavedMsg{slug: slug, key: key}
		}
	}
	var cmd tea.Cmd
	v.prop, cmd = v.prop.Update(msg)
	return v, cmd
}

func (v *basesView) View(width, height int) string {
	if !v.loaded {
		return "\n  loading bases…"
	}
	if v.base == nil {
		var b strings.Builder
		b.WriteString("\n")
		if len(v.names) == 0 {
			b.WriteString("  no .base files in the vault\n")
		}
		for i, name := range v.names {
			if i == v.nameCursor {
				b.WriteString(cursorStyle.Render(" ▸ "+name) + "\n")
			} else {
				b.WriteString("   " + name + "\n")
			}
		}
		b.WriteString("\n" + dimStyle.Render(" enter open · r refresh · esc back"))
		return b.String()
	}
	return v.tableView(width, height)
}

func (v *basesView) tableView(width, height int) string {
	widths := columnWidths(v.base.Data.Columns, v.rows, width-4)
	var b strings.Builder
	// Header row.
	var cells []string
	for i, col := range v.base.Data.Columns {
		cells = append(cells, pad(col, widths[i]))
	}
	b.WriteString(" " + columnTitleStyle.Render(strings.Join(cells, "  ")) + "\n")

	reserved := 0
	if v.find.bar() != "" || v.propping {
		reserved++
	}
	rowsAvail := max(3, height-4-reserved)
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+rowsAvail {
		v.offset = v.cursor - rowsAvail + 1
	}
	end := min(len(v.rows), v.offset+rowsAvail)
	for i := v.offset; i < end; i++ {
		row := v.rows[i]
		if row.header != "" {
			b.WriteString(" " + groupHeaderStyle.Render(row.header) + "\n")
			continue
		}
		cells = cells[:0]
		for c := range widths {
			cell := ""
			if c < len(row.cells) {
				cell = row.cells[c]
			}
			cells = append(cells, pad(cell, widths[c]))
		}
		line := strings.Join(cells, "  ")
		if i == v.cursor {
			b.WriteString(cursorStyle.Render(" ▸ "+line) + "\n")
		} else {
			b.WriteString("   " + line + "\n")
		}
	}
	hint := dimStyle.Render(" enter open note · e set property · v cycle view · / search · r refresh · esc list")
	if bar := v.find.bar(); bar != "" {
		hint = " " + bar
	}
	if v.propping {
		hint = " " + v.prop.View()
	}
	b.WriteString(hint)
	return b.String()
}

// columnWidths sizes columns to content, then squeezes proportionally into
// the available width.
func columnWidths(columns []string, rows []baseRow, avail int) []int {
	widths := make([]int, len(columns))
	for i, col := range columns {
		widths[i] = lipgloss.Width(col)
	}
	for _, row := range rows {
		for i := 0; i < len(widths) && i < len(row.cells); i++ {
			if w := lipgloss.Width(row.cells[i]); w > widths[i] {
				widths[i] = w
			}
		}
	}
	const maxCol = 40
	total := 0
	for i := range widths {
		if widths[i] > maxCol {
			widths[i] = maxCol
		}
		total += widths[i] + 2
	}
	for total > avail && avail > 0 {
		// Shrink the widest column until it fits (floor 8).
		widest := 0
		for i := range widths {
			if widths[i] > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= 8 {
			break
		}
		widths[widest]--
		total--
	}
	return widths
}

func pad(s string, width int) string {
	w := lipgloss.Width(s)
	if w > width {
		return truncate(s, max(1, width-1)) + "…"
	}
	return s + strings.Repeat(" ", width-w)
}
