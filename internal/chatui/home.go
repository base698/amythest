package chatui

import (
	"fmt"
	"strings"
	"time"

	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Window 1 is home: what is waiting on you on the left, the selected
// conversation's recent messages on the right, as the focus view has it.
// Enter opens the selection in a window of its own. Typing filters.
//
// Ctrl+F browses every conversation by service — channels, groups and
// direct chats, with Slack threads under their channel — and Enter opens
// the choice as a window.

const previewSize = 40

type preview struct {
	msgs    []messages.Message
	at      time.Time // the activity it was loaded for
	loading bool
}

type previewMsg struct {
	conv string
	at   time.Time
	msgs []messages.Message
	err  error
}

type dirMsg struct {
	list []messages.Listing
	err  error
}

// browser is the Ctrl+F conversation picker.
type browser struct {
	open      bool
	list      []messages.Listing
	loaded    bool
	sel       int
	collapsed map[string]bool
	back      string // the input line before the browser opened
}

// row is one line of the browser: a service heading or a conversation.
type row struct {
	group   string
	heading bool
	count   int
	l       messages.Listing
	depth   int
}

var (
	selStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "57", Dark: "141"})
	headStyle = lipgloss.NewStyle().Bold(true)
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "179"})
)

// --- home --------------------------------------------------------------------

// homeFilter is the input when it filters rather than commands.
func (m *Model) homeFilter() string {
	v := m.input.Value()
	if strings.HasPrefix(v, "/") {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func (m *Model) homeItems() []messages.Item {
	f := m.homeFilter()
	if f == "" {
		return m.focus
	}
	var out []messages.Item
	for _, it := range m.focus {
		if strings.Contains(strings.ToLower(itemText(it)), f) {
			out = append(out, it)
		}
	}
	return out
}

func itemText(it messages.Item) string {
	return messages.Plain(it.Conversation.Name) + " " + it.Conversation.Service + " " + messages.Plain(it.Message.Sender) + " " + it.Message.Body
}

func (m *Model) homeSelected() (messages.Item, bool) {
	items := m.homeItems()
	if len(items) == 0 {
		return messages.Item{}, false
	}
	m.homeSel = min(max(0, m.homeSel), len(items)-1)
	return items[m.homeSel], true
}

func (m *Model) homeMove(d int) tea.Cmd {
	m.homeSel += d
	_, _ = m.homeSelected()
	return m.loadPreview()
}

// loadPreview fetches the selected conversation's recent messages when they
// are missing or older than its latest activity.
func (m *Model) loadPreview() tea.Cmd {
	it, ok := m.homeSelected()
	if !ok {
		return nil
	}
	id := it.Conversation.ID
	p := m.previews[id]
	if p != nil && (p.loading || !p.at.Before(it.LastActivity)) {
		return nil
	}
	if p == nil {
		p = &preview{}
		m.previews[id] = p
	}
	p.loading = true
	at := it.LastActivity
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		t, e := m.api.ThreadPage(c, id, messages.Page{Limit: previewSize})
		return previewMsg{conv: id, at: at, msgs: t.Messages, err: e}
	}
}

func (m *Model) onPreview(x previewMsg) tea.Cmd {
	p := m.previews[x.conv]
	if p == nil {
		return nil
	}
	p.loading = false
	if x.err != nil {
		m.status("-!- %v", x.err)
		return nil
	}
	p.msgs, p.at = x.msgs, x.at
	return nil
}

func (m *Model) openSelected() tea.Cmd {
	it, ok := m.homeSelected()
	if !ok {
		return nil
	}
	m.input.SetValue("")
	i, w, cmd := m.open(it.Conversation)
	if w == nil {
		return nil
	}
	return tea.Batch(cmd, m.show(i))
}

// homeLines draws home in exactly h rows of width w.
func (m *Model) homeLines(width, h int) []string {
	out := make([]string, 0, h)
	if n := m.lastNotice(); n != "" {
		out = append(out, dim.Render(ansi.Truncate(n, width, "…")))
		h--
	}
	items := m.homeItems()
	lw := min(max(28, width*2/5), width-20)
	rw := width - lw - 3
	var left []string
	selTop, selBottom := 0, 0
	if len(items) == 0 {
		if m.homeFilter() != "" {
			left = append(left, dim.Render("Nothing waiting matches. Ctrl+F searches every chat."))
		} else if m.started {
			left = append(left, dim.Render("Nothing is waiting on you."), "", dim.Render("Ctrl+F opens any chat."))
		} else {
			left = append(left, dim.Render("loading…"))
		}
	}
	for i, it := range items {
		if i == m.homeSel {
			selTop = len(left)
		}
		mark, name := "  ", messages.Plain(it.Conversation.Name)
		if name == "" {
			name = messages.Plain(it.Conversation.Recipient)
		}
		if i == m.homeSel {
			mark, name = selStyle.Render("› "), selStyle.Render(name)
		}
		if j, _ := m.window(it.Conversation.ID); j >= 0 {
			name += dim.Render(fmt.Sprintf("  [%d]", j+1))
		}
		left = append(left, mark+name)
		left = append(left, "  "+dim.Render(it.Conversation.Service+" · "+when(it.LastActivity)))
		summary := it.Message.Category + " · " + messages.Plain(it.Message.Sender)
		if it.Message.Category == "Don't miss" {
			summary = warnStyle.Render(summary)
		}
		left = append(left, "  "+summary)
		if i == m.homeSel {
			selBottom = len(left)
		}
		left = append(left, "")
	}
	// Keep the selection on screen.
	off := 0
	if selBottom > h {
		off = selBottom - h
	}
	if selTop < off {
		off = selTop
	}
	right := m.previewLines(rw, h)
	for i := 0; i < h; i++ {
		l := ""
		if k := off + i; k < len(left) {
			l = ansi.Truncate(left[k], lw, "…")
		}
		r := ""
		if i < len(right) {
			r = right[i]
		}
		out = append(out, l+strings.Repeat(" ", max(0, lw-lipgloss.Width(l)))+dim.Render(" │ ")+r)
	}
	return out
}

func (m *Model) previewLines(width, h int) []string {
	it, ok := m.homeSelected()
	if !ok {
		return nil
	}
	c := it.Conversation
	head := []string{
		headStyle.Render(ansi.Truncate(messages.Plain(c.Name), width, "…")),
		dim.Render(ansi.Truncate(messages.Destination(c), width, "…")),
		dim.Render("Enter opens · /done · /snooze"),
		"",
	}
	p := m.previews[c.ID]
	var body []string
	if p == nil || p.msgs == nil && p.loading {
		body = append(body, dim.Render("loading…"))
	} else {
		day := ""
		for _, msg := range p.msgs {
			if d := msg.At.Local().Format("Mon Jan 2"); d != day {
				body = append(body, dim.Render("-- "+d+" --"))
				day = d
			}
			hd := clock(msg.At) + " <" + nick(msg.Sender, msg.Outgoing) + "> "
			for _, l := range indented(hd, msg.Body, width, msg.ID) {
				body = append(body, l.text)
			}
		}
	}
	room := max(0, h-len(head))
	if len(body) > room {
		body = body[len(body)-room:]
	}
	out := append(head, body...)
	for i := range out {
		out[i] = ansi.Truncate(out[i], width, "…")
	}
	return out
}

func (m *Model) lastNotice() string {
	ns := m.wins[0].notices
	if len(ns) == 0 {
		return ""
	}
	n := ns[len(ns)-1]
	return clock(n.at) + " " + n.text
}

func when(t time.Time) string {
	if t.IsZero() {
		return "no messages yet"
	}
	t = t.Local()
	now := time.Now()
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

// --- browser -----------------------------------------------------------------

func (m *Model) openBrowser(filter string) tea.Cmd {
	m.br.open = true
	m.br.back = m.input.Value()
	m.input.SetValue(filter)
	m.input.CursorEnd()
	m.br.sel = 0
	return m.loadDir()
}

func (m *Model) closeBrowser() {
	m.br.open = false
	m.input.SetValue(m.br.back)
	m.input.CursorEnd()
}

func (m *Model) loadDir() tea.Cmd {
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		l, e := m.api.Conversations(c)
		return dirMsg{l, e}
	}
}

func (m *Model) onDir(x dirMsg) {
	if x.err != nil {
		m.status("-!- Could not list conversations: %v", x.err)
		return
	}
	m.br.list, m.br.loaded = x.list, true
	if rs := m.rows(); m.br.sel == 0 && len(rs) > 1 && rs[0].heading {
		m.br.sel = 1
	}
	for _, l := range x.list {
		if _, ok := m.known[l.Conversation.ID]; !ok {
			m.known[l.Conversation.ID] = messages.Item{Conversation: l.Conversation, LastActivity: l.LastActivity}
		}
	}
}

// groupOf names a listing's service section; the account is added only
// where one service has several.
func (m *Model) groupOf(l messages.Listing) string {
	svc := l.Conversation.Service
	if svc == "" {
		svc = "Other"
	}
	if l.Conversation.Demo {
		svc += " (demo)"
	}
	accounts := map[string]bool{}
	for _, x := range m.br.list {
		if x.Conversation.Service == l.Conversation.Service && x.Conversation.Demo == l.Conversation.Demo {
			accounts[x.Conversation.Account] = true
		}
	}
	if len(accounts) > 1 {
		svc += " · " + shortAccount(l.Conversation.Account)
	}
	return svc
}

func shortAccount(a string) string {
	a = strings.TrimPrefix(a, "profile/")
	if len(a) > 12 {
		return a[:12] + "…"
	}
	return a
}

func listingName(l messages.Listing) string {
	n := messages.Plain(l.Conversation.Name)
	if n == "" {
		n = messages.Plain(l.Conversation.Recipient)
	}
	return n
}

// rows lays the listing out under service headings, with Slack threads
// under their channel. A filter shows every match and ignores folding.
func (m *Model) rows() []row {
	f := strings.ToLower(strings.TrimSpace(m.input.Value()))
	type group struct {
		name  string
		items []messages.Listing
	}
	var groups []*group
	byName := map[string]*group{}
	present := map[string]bool{}
	for _, l := range m.br.list {
		present[l.Conversation.ID] = true
	}
	children := map[string][]messages.Listing{}
	for _, l := range m.br.list {
		if f != "" && !strings.Contains(strings.ToLower(listingName(l)+" "+l.Conversation.Service+" "+l.Kind), f) {
			continue
		}
		if l.Parent != "" && present[l.Parent] && f == "" {
			children[l.Parent] = append(children[l.Parent], l)
			continue
		}
		g := m.groupOf(l)
		if byName[g] == nil {
			byName[g] = &group{name: g}
			groups = append(groups, byName[g])
		}
		byName[g].items = append(byName[g].items, l)
	}
	var out []row
	for _, g := range groups {
		out = append(out, row{group: g.name, heading: true, count: len(g.items)})
		if f == "" && m.br.collapsed[g.name] {
			continue
		}
		for _, l := range g.items {
			out = append(out, row{group: g.name, l: l})
			for _, c := range children[l.Conversation.ID] {
				out = append(out, row{group: g.name, l: c, depth: 1})
			}
		}
	}
	return out
}

// rowName labels a browser row; a thread names its channel when it is not
// shown under it.
func (m *Model) rowName(r row) string {
	name := listingName(r.l)
	if r.l.Kind == "channel" && !strings.HasPrefix(name, "#") {
		name = "#" + name
	}
	if r.l.Kind != "thread" {
		return name
	}
	label := "↳ thread"
	if r.l.Conversation.Thread != "" {
		label += " " + r.l.Conversation.Thread
	}
	if r.depth == 0 {
		return name + " " + label
	}
	return label
}

func (m *Model) browserKey(k tea.KeyMsg) (tea.Cmd, bool) {
	rows := m.rows()
	clamp := func() { m.br.sel = min(max(0, m.br.sel), max(0, len(rows)-1)) }
	switch k.String() {
	case "esc", "ctrl+f":
		m.closeBrowser()
		return nil, true
	case "up", "ctrl+k":
		m.br.sel--
		clamp()
		return nil, true
	case "down", "ctrl+j":
		m.br.sel++
		clamp()
		return nil, true
	case "pgup":
		m.br.sel -= m.bodyHeight() - 2
		clamp()
		return nil, true
	case "pgdown":
		m.br.sel += m.bodyHeight() - 2
		clamp()
		return nil, true
	case "left", "right":
		clamp()
		if len(rows) == 0 || strings.TrimSpace(m.input.Value()) != "" {
			return nil, false // move the cursor in the filter
		}
		g := rows[m.br.sel].group
		m.br.collapsed[g] = k.String() == "left"
		// Stay on the heading of the folded group.
		for i, r := range m.rows() {
			if r.heading && r.group == g {
				m.br.sel = i
			}
		}
		return nil, true
	case "enter":
		clamp()
		if len(rows) == 0 {
			return nil, true
		}
		r := rows[m.br.sel]
		if r.heading {
			m.br.collapsed[r.group] = !m.br.collapsed[r.group]
			return nil, true
		}
		m.br.open = false
		m.input.SetValue("")
		i, w, cmd := m.open(r.l.Conversation)
		if w == nil {
			return nil, true
		}
		return tea.Batch(cmd, m.show(i)), true
	}
	if k.Alt || k.String() == "ctrl+n" || k.String() == "ctrl+p" {
		// Window keys leave the browser and act as usual.
		m.closeBrowser()
		return nil, false
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if m.input.Value() != before {
		m.br.sel = 0
		// Land on the first conversation, not a heading.
		if rs := m.rows(); len(rs) > 1 && rs[0].heading {
			m.br.sel = 1
		}
	}
	return cmd, true
}

func (m *Model) browserLines(width, h int) []string {
	if !m.br.loaded {
		return []string{dim.Render("loading conversations…")}
	}
	rows := m.rows()
	if len(rows) == 0 {
		return []string{dim.Render("No conversation matches. Slack channels appear after the connector lists them.")}
	}
	m.br.sel = min(max(0, m.br.sel), len(rows)-1)
	off := 0
	if m.br.sel >= h {
		off = m.br.sel - h + 1
	}
	var out []string
	for i := off; i < len(rows) && len(out) < h; i++ {
		r := rows[i]
		sel := i == m.br.sel
		mark := "  "
		if sel {
			mark = selStyle.Render("› ")
		}
		if r.heading {
			fold := "▾ "
			if m.br.collapsed[r.group] && strings.TrimSpace(m.input.Value()) == "" {
				fold = "▸ "
			}
			out = append(out, ansi.Truncate(mark+headStyle.Render(fold+r.group)+dim.Render(fmt.Sprintf("  %d", r.count)), width, "…"))
			continue
		}
		name := m.rowName(r)
		if j, _ := m.window(r.l.Conversation.ID); j >= 0 {
			name += fmt.Sprintf("  [%d]", j+1)
		}
		right := fmt.Sprintf("%-8s %s", r.l.Kind, when(r.l.LastActivity))
		room := max(10, width-lipgloss.Width(right)-4)
		left := ansi.Truncate(strings.Repeat("  ", 1+r.depth)+name, room-2, "…")
		gap := strings.Repeat(" ", max(1, room-2-lipgloss.Width(left)+2))
		if sel {
			left = selStyle.Render(left)
		}
		out = append(out, ansi.Truncate(mark+left+gap+dim.Render(right), width, "…"))
	}
	return out
}

// --- hints -------------------------------------------------------------------

// hint is the input line's placeholder: what typing here does.
func (m *Model) hint() string {
	if m.br.open {
		return "type to filter · ↑↓ choose · Enter opens · ←→ fold · Esc back"
	}
	w := m.cur()
	switch w.kind {
	case kindStatus:
		return "↑↓ choose · Enter opens · type to filter · Ctrl+F all chats · /help"
	case kindSearch:
		if w.title == "help" {
			return "/close closes help · Alt+1 home · Ctrl+F chats"
		}
		return "type a result number to open it · Alt+1 home"
	}
	if t, ok := w.replyTarget(); ok && !t.Outgoing {
		return "message replies to " + messages.Plain(t.Sender) + " · /reply TAG · /thread TAG · Ctrl+V picture · Ctrl+F chats"
	}
	return "message " + w.name() + " · Ctrl+F chats · Alt+1 home"
}

// homeKey handles the keys that act on home's list.
func (m *Model) homeKey(k tea.KeyMsg) (tea.Cmd, bool) {
	slash := strings.HasPrefix(m.input.Value(), "/")
	switch k.String() {
	case "up", "ctrl+k":
		if slash {
			return nil, false
		}
		return m.homeMove(-1), true
	case "down", "ctrl+j":
		if slash {
			return nil, false
		}
		return m.homeMove(1), true
	case "enter":
		if slash {
			return nil, false
		}
		return m.openSelected(), true
	case "esc":
		m.input.SetValue("")
		return m.loadPreview(), true
	}
	return nil, false
}
