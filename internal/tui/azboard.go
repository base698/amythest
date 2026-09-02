package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/source/azboards"
)

// azBoardView renders one Azure Boards virtual board (WIQL + AreaPath +
// type from cli.yaml) as columns of work items, one column per state. It
// reads through the source's cache; r forces a refresh. Moving between
// columns is an ADO state transition (m), comments go through --discussion
// (c), and o opens the work item in the browser. When az is logged out the
// view shows the fix instead of an error trail.
type azBoardView struct {
	src *azboards.Source
	cfg azboards.BoardConfig

	items   []azboards.WorkItem
	cols    []string
	col     int
	cursors []int
	offsets []int
	mine    bool // f: only work items assigned to the signed-in user
	busy    bool
	loaded  bool
	loadErr error
	find    finder
	picker  movePicker

	comment    textinput.Model
	commenting bool
}

// Messages are tagged with the board (and source is a singleton per app) so
// the stack broadcast can't cross wires between two open virtual boards.
type (
	azItemsMsg struct {
		board string
		mine  bool
		items []azboards.WorkItem
		err   error
	}
	azStateSetMsg struct {
		board string
		id    int
		state string
	}
	azCommentedMsg struct {
		board string
		id    int
	}
	azItemMsg struct {
		board string
		item  azboards.WorkItem
		err   error
	}
	azCommentsMsg struct {
		board    string
		id       int
		comments []azboards.WorkItemComment
		err      error
	}
)

func newAZBoardView(src *azboards.Source, cfg azboards.BoardConfig) *azBoardView {
	ci := textinput.New()
	ci.Prompt = "comment: "
	ci.CharLimit = 4000
	return &azBoardView{src: src, cfg: cfg, find: newFinder(), comment: ci}
}

func (v *azBoardView) Title() string { return v.cfg.Name }
func (v *azBoardView) Busy() bool    { return v.busy }
func (v *azBoardView) Capturing() bool {
	return v.find.active() || v.picker.active || v.commenting
}

func (v *azBoardView) Init() tea.Cmd { return v.loadCmd(false) }

func (v *azBoardView) loadCmd(force bool) tea.Cmd {
	v.busy = true
	src, cfg, mine := v.src, v.cfg, v.mine
	return func() tea.Msg {
		items, err := src.BoardItems(context.Background(), cfg, force, mine)
		return azItemsMsg{board: cfg.Name, mine: mine, items: items, err: err}
	}
}

// columnItems returns the work items in column order, preserving list order
// and applying the live "/" fuzzy filter.
func (v *azBoardView) columnItems(col int) []azboards.WorkItem {
	if col < 0 || col >= len(v.cols) {
		return nil
	}
	state := v.cols[col]
	query := v.find.liveQuery()
	var items []azboards.WorkItem
	for _, it := range v.items {
		if it.State != state {
			continue
		}
		if query != "" && !fuzzyMatch(fmt.Sprintf("#%d %s %s", it.ID, it.Title, it.Assignee), query) {
			continue
		}
		items = append(items, it)
	}
	return items
}

func (v *azBoardView) currentItem() *azboards.WorkItem {
	items := v.columnItems(v.col)
	if len(v.cursors) != len(v.cols) || v.col >= len(v.cursors) {
		return nil
	}
	cursor := v.cursors[v.col]
	if cursor < 0 || cursor >= len(items) {
		return nil
	}
	return &items[cursor]
}

func (v *azBoardView) reshape() {
	v.cols = azboards.Columns(v.cfg, v.items)
	if len(v.cursors) != len(v.cols) {
		v.cursors = make([]int, len(v.cols))
		v.offsets = make([]int, len(v.cols))
	}
	if v.col >= len(v.cols) {
		v.col = max(0, len(v.cols)-1)
	}
	for col := range v.cols {
		if n := len(v.columnItems(col)); v.cursors[col] >= n {
			v.cursors[col] = max(0, n-1)
		}
	}
}

// focusFirstMatch moves focus off an emptied column after a filter commit.
func (v *azBoardView) focusFirstMatch() {
	if len(v.columnItems(v.col)) > 0 {
		return
	}
	for col := range v.cols {
		if len(v.columnItems(col)) > 0 {
			v.col = col
			v.cursors[col] = 0
			return
		}
	}
}

func (v *azBoardView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case azItemsMsg:
		if msg.board != v.cfg.Name || msg.mine != v.mine {
			return v, nil
		}
		v.busy = false
		v.loaded = true
		v.loadErr = msg.err
		if msg.err == nil {
			v.items = msg.items
			v.reshape()
		}
		return v, nil

	case azStateSetMsg:
		if msg.board != v.cfg.Name {
			return v, nil
		}
		return v, tea.Batch(v.loadCmd(true),
			flash(fmt.Sprintf("#%d moved to %s ✓", msg.id, msg.state)))

	case azCommentedMsg:
		if msg.board != v.cfg.Name {
			return v, nil
		}
		return v, tea.Batch(v.loadCmd(true),
			flash(fmt.Sprintf("comment posted to #%d ✓", msg.id)))

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.find.active() {
			committed, cmd := v.find.handleKey(msg)
			v.reshape() // the live filter narrows columns as it's typed
			if committed {
				v.focusFirstMatch()
			}
			return v, cmd
		}
		if v.picker.active {
			choice := v.picker.handleKey(msg)
			if choice == nil {
				return v, nil
			}
			return v, v.setStateCmd(choice.label)
		}
		if v.commenting {
			return v.handleCommentKey(msg)
		}
		switch msg.String() {
		case "h", "left":
			if v.col > 0 {
				v.col--
			}
		case "l", "right":
			if v.col < len(v.cols)-1 {
				v.col++
			}
		case "j", "down":
			if v.col < len(v.cursors) && v.cursors[v.col] < len(v.columnItems(v.col))-1 {
				v.cursors[v.col]++
			}
		case "k", "up":
			if v.col < len(v.cursors) && v.cursors[v.col] > 0 {
				v.cursors[v.col]--
			}
		case "/":
			return v, v.find.start()
		case "r":
			return v, v.loadCmd(true)
		case "f":
			v.mine = !v.mine
			note := "showing all work items"
			if v.mine {
				note = "showing only your work items (@Me)"
			}
			return v, tea.Batch(v.loadCmd(false), flash(note))
		case "enter":
			if it := v.currentItem(); it != nil {
				return v, pushView(newAZItemView(v.src, v.cfg, it.ID))
			}
		case "o":
			if it := v.currentItem(); it != nil {
				return v, openURLCmd(v.src.WebURL(it.ID))
			}
		case "m":
			if it := v.currentItem(); it != nil {
				v.picker.open(fmt.Sprintf("#%d %s", it.ID, it.Title), azMoveOptions(v.cols, it.State))
			}
		case "c":
			if v.currentItem() == nil {
				return v, nil
			}
			v.commenting = true
			v.comment.SetValue("")
			return v, v.comment.Focus()
		}
	}
	return v, nil
}

// azMoveOptions builds the column picker: every state, current one marked.
func azMoveOptions(cols []string, current string) []moveOption {
	options := make([]moveOption, len(cols))
	for i, col := range cols {
		options[i] = moveOption{label: col, current: col == current}
	}
	return options
}

func (v *azBoardView) setStateCmd(state string) tea.Cmd {
	it := v.currentItem()
	if it == nil || v.busy {
		return nil
	}
	v.busy = true
	src, name, id := v.src, v.cfg.Name, it.ID
	return func() tea.Msg {
		if err := src.SetState(context.Background(), id, state); err != nil {
			return fail(err)
		}
		return azStateSetMsg{board: name, id: id, state: state}
	}
}

func (v *azBoardView) handleCommentKey(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		v.commenting = false
		v.comment.Blur()
		return v, nil
	case tea.KeyEnter:
		body := strings.TrimSpace(v.comment.Value())
		v.commenting = false
		v.comment.Blur()
		it := v.currentItem()
		if body == "" || it == nil {
			return v, nil
		}
		v.busy = true
		src, name, id := v.src, v.cfg.Name, it.ID
		return v, func() tea.Msg {
			if err := src.Comment(context.Background(), id, body); err != nil {
				return fail(err)
			}
			return azCommentedMsg{board: name, id: id}
		}
	}
	var cmd tea.Cmd
	v.comment, cmd = v.comment.Update(msg)
	return v, cmd
}

// azLoginBanner is the shared logged-out rendering: what happened, the fix,
// and how to retry — instead of a raw error in the status bar.
func azLoginBanner(src *azboards.Source, err error) string {
	if !errors.Is(err, azboards.ErrNotLoggedIn) {
		return "\n  " + blockedStyle.Render("azure boards error") + "  " + err.Error() + "\n\n" +
			dimStyle.Render("  r retry · esc back") + "\n"
	}
	return "\n  " + blockedStyle.Render("not logged in to Azure DevOps") + "\n\n" +
		"  " + cursorStyle.Render("az devops login --organization "+src.Config().Org) + "\n" +
		dimStyle.Render("  (or export AZURE_DEVOPS_EXT_PAT with a Work Items read/write PAT)") + "\n\n" +
		dimStyle.Render("  r retry once logged in · esc back") + "\n"
}

func (v *azBoardView) View(width, height int) string {
	if !v.loaded {
		return "\n  loading " + v.cfg.Name + "…"
	}
	if v.loadErr != nil {
		return azLoginBanner(v.src, v.loadErr)
	}
	if v.picker.active {
		return v.picker.view()
	}
	if len(v.cols) == 0 {
		return "\n  no work items"
	}
	colWidth := max(18, width/len(v.cols)-2)
	inner := colWidth - 2 // columnStyle pads one cell each side
	maxRows := max(3, height-4)
	var columns []string
	for col, state := range v.cols {
		items := v.columnItems(col)
		lines := []string{columnTitleStyle.Render(fmt.Sprintf("%s (%d)", state, len(items)))}
		selectedRow := func(i int) bool {
			return col == v.col && col < len(v.cursors) && i == v.cursors[col]
		}
		if len(items) <= maxRows {
			if col < len(v.offsets) {
				v.offsets[col] = 0
			}
			for i, it := range items {
				lines = append(lines, renderAZItemLine(it, selectedRow(i), inner))
			}
		} else {
			// Scroll window: the cursor stays visible, markers show what's
			// clipped above and below.
			visible := max(1, maxRows-2)
			v.offsets[col] = columnWindow(len(items), v.cursors[col], v.offsets[col], visible)
			off := v.offsets[col]
			above := " "
			if off > 0 {
				above = fmt.Sprintf("↑ %d more", off)
			}
			lines = append(lines, dimStyle.Render(above))
			for i := off; i < min(off+visible, len(items)); i++ {
				lines = append(lines, renderAZItemLine(items[i], selectedRow(i), inner))
			}
			if below := len(items) - (off + visible); below > 0 {
				lines = append(lines, dimStyle.Render(fmt.Sprintf("↓ %d more", below)))
			}
		}
		if len(items) == 0 {
			lines = append(lines, dimStyle.Render("—"))
		}
		style := columnStyle
		if col == v.col {
			style = columnFocusStyle
		}
		columns = append(columns, style.Width(colWidth).Render(strings.Join(lines, "\n")))
	}
	out := lipgloss.JoinHorizontal(lipgloss.Top, columns...)
	mineHint := "f mine"
	if v.mine {
		mineHint = "f all " + dueStyle.Render("[mine]")
	}
	hint := dimStyle.Render(" enter open · m move · c comment · o browser · / filter · "+mineHint+" · r refresh")
	if bar := v.find.filterBar(); bar != "" {
		hint = " " + bar
		if v.mine {
			hint += " " + dueStyle.Render("[mine]")
		}
	}
	if v.commenting {
		hint = " " + v.comment.View()
	}
	return out + "\n" + hint
}

// renderAZItemLine renders one work item as a single row that never exceeds
// width cells — overflow would wrap inside the lipgloss column and break the
// row-per-item scroll math. The assignee is dropped before the title is
// starved, the id never is.
func renderAZItemLine(it azboards.WorkItem, selected bool, width int) string {
	prefix := "  "
	if selected {
		prefix = cursorStyle.Render("▸ ")
	}
	id := fmt.Sprintf("#%d", it.ID)
	avail := width - 2 - len(id) - 1
	name := ""
	if it.Assignee != "" {
		name = azShortName(it.Assignee)
		if len(name)+1+10 > avail {
			name = ""
		} else {
			avail -= len(name) + 1
		}
	}
	title := it.Title
	if lipgloss.Width(title) > avail {
		title = truncate(title, max(1, avail-1)) + "…"
	}
	if selected {
		title = cursorStyle.Render(title)
	}
	line := prefix + dimStyle.Render(id) + " " + title
	if name != "" {
		line += " " + dimStyle.Render(name)
	}
	return line
}

// azShortName compresses "First Last" to "First L." for column rows.
func azShortName(name string) string {
	parts := strings.Fields(name)
	if len(parts) < 2 {
		return name
	}
	last := []rune(parts[len(parts)-1])
	return parts[0] + " " + string(last[0]) + "."
}

// azItemView is the pushed work item detail: full description (HTML
// flattened), assignee, comment count, plus the same actions as the board.
type azItemView struct {
	src *azboards.Source
	cfg azboards.BoardConfig
	id  int

	item        azboards.WorkItem
	comments    []azboards.WorkItemComment
	commentsErr error
	busy        bool
	loaded      bool
	loadErr     error
	picker      movePicker

	comment    textinput.Model
	commenting bool
}

func newAZItemView(src *azboards.Source, cfg azboards.BoardConfig, id int) *azItemView {
	ci := textinput.New()
	ci.Prompt = "comment: "
	ci.CharLimit = 4000
	return &azItemView{src: src, cfg: cfg, id: id, comment: ci}
}

func (v *azItemView) Title() string   { return fmt.Sprintf("#%d", v.id) }
func (v *azItemView) Busy() bool      { return v.busy }
func (v *azItemView) Capturing() bool { return v.picker.active || v.commenting }

func (v *azItemView) Init() tea.Cmd { return v.loadCmd(false) }

func (v *azItemView) loadCmd(force bool) tea.Cmd {
	v.busy = true
	src, name, id := v.src, v.cfg.Name, v.id
	return tea.Batch(
		func() tea.Msg {
			item, err := src.Item(context.Background(), id, force)
			return azItemMsg{board: name, item: item, err: err}
		},
		func() tea.Msg {
			comments, err := src.Comments(context.Background(), id, force)
			return azCommentsMsg{board: name, id: id, comments: comments, err: err}
		},
	)
}

func (v *azItemView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case azItemMsg:
		if msg.board != v.cfg.Name || (msg.err == nil && msg.item.ID != v.id) {
			return v, nil
		}
		v.busy = false
		v.loaded = true
		v.loadErr = msg.err
		if msg.err == nil {
			v.item = msg.item
		}
		return v, nil

	case azCommentsMsg:
		if msg.board != v.cfg.Name || msg.id != v.id {
			return v, nil
		}
		v.commentsErr = msg.err
		if msg.err == nil {
			v.comments = msg.comments
		}
		return v, nil

	case azStateSetMsg:
		if msg.board != v.cfg.Name || msg.id != v.id {
			return v, nil
		}
		return v, tea.Batch(v.loadCmd(true),
			flash(fmt.Sprintf("#%d moved to %s ✓", msg.id, msg.state)))

	case azCommentedMsg:
		if msg.board != v.cfg.Name || msg.id != v.id {
			return v, nil
		}
		return v, tea.Batch(v.loadCmd(true),
			flash(fmt.Sprintf("comment posted to #%d ✓", msg.id)))

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.picker.active {
			choice := v.picker.handleKey(msg)
			if choice == nil {
				return v, nil
			}
			v.busy = true
			src, name, id, state := v.src, v.cfg.Name, v.id, choice.label
			return v, func() tea.Msg {
				if err := src.SetState(context.Background(), id, state); err != nil {
					return fail(err)
				}
				return azStateSetMsg{board: name, id: id, state: state}
			}
		}
		if v.commenting {
			return v.handleCommentKey(msg)
		}
		switch msg.String() {
		case "r":
			return v, v.loadCmd(true)
		case "o":
			return v, openURLCmd(v.src.WebURL(v.id))
		case "m":
			if v.loaded && v.loadErr == nil {
				cols := azboards.Columns(v.cfg, []azboards.WorkItem{v.item})
				v.picker.open(fmt.Sprintf("#%d %s", v.item.ID, v.item.Title), azMoveOptions(cols, v.item.State))
			}
		case "c":
			if !v.loaded || v.loadErr != nil {
				return v, nil
			}
			v.commenting = true
			v.comment.SetValue("")
			return v, v.comment.Focus()
		}
	}
	return v, nil
}

func (v *azItemView) handleCommentKey(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		v.commenting = false
		v.comment.Blur()
		return v, nil
	case tea.KeyEnter:
		body := strings.TrimSpace(v.comment.Value())
		v.commenting = false
		v.comment.Blur()
		if body == "" {
			return v, nil
		}
		v.busy = true
		src, name, id := v.src, v.cfg.Name, v.id
		return v, func() tea.Msg {
			if err := src.Comment(context.Background(), id, body); err != nil {
				return fail(err)
			}
			return azCommentedMsg{board: name, id: id}
		}
	}
	var cmd tea.Cmd
	v.comment, cmd = v.comment.Update(msg)
	return v, cmd
}

func (v *azItemView) View(width, height int) string {
	if !v.loaded {
		return fmt.Sprintf("\n  loading #%d…", v.id)
	}
	if v.loadErr != nil {
		return azLoginBanner(v.src, v.loadErr)
	}
	if v.picker.active {
		return v.picker.view()
	}
	it := v.item
	var b strings.Builder
	b.WriteString("\n  " + columnTitleStyle.Render(fmt.Sprintf("#%d", it.ID)) + "  " + cursorStyle.Render(it.Title) + "\n")
	meta := []string{dueStyle.Render(it.State)}
	if it.Assignee != "" {
		meta = append(meta, it.Assignee)
	}
	b.WriteString("  " + strings.Join(meta, "  ") + "\n")
	b.WriteString("  " + dimStyle.Render(v.src.WebURL(it.ID)) + "\n\n")
	if desc := azboards.StripHTML(it.Description); desc != "" {
		for _, line := range strings.Split(desc, "\n") {
			for _, row := range wrapLine("  "+line, max(20, width-4)) {
				b.WriteString(row + "\n")
			}
		}
	} else {
		b.WriteString(dimStyle.Render("  no description") + "\n")
	}
	count := it.CommentCount
	if v.commentsErr == nil {
		count = len(v.comments)
	}
	b.WriteString("\n  " + columnTitleStyle.Render(fmt.Sprintf("Comments (%d)", count)) + "\n")
	switch {
	case v.commentsErr != nil:
		b.WriteString(dimStyle.Render("  comments unavailable: "+v.commentsErr.Error()) + "\n")
	case len(v.comments) == 0:
		b.WriteString(dimStyle.Render("  no comments yet — c writes one") + "\n")
	default:
		for _, c := range v.comments {
			b.WriteString("  " + dueStyle.Render(c.Author) + "  " + dimStyle.Render(c.Date) + "\n")
			for _, line := range strings.Split(c.Text, "\n") {
				for _, row := range wrapLine("    "+line, max(20, width-4)) {
					b.WriteString(row + "\n")
				}
			}
		}
	}
	hint := dimStyle.Render(" m move column · c comment · o browser · r refresh · esc back")
	if v.commenting {
		hint = " " + v.comment.View()
	}
	b.WriteString("\n" + hint)
	return b.String()
}
