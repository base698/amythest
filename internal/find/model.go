package find

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Options configure a search.
type Options struct {
	// Finish embeds search in another UI; nil closes the standalone program.
	Finish func(*Choice) tea.Cmd

	Query   string          // start with this query
	Screens func() []Screen // reads open terminals; nil: that source is off
	Tickets func() []Ticket // lists external trackers' tickets; nil: none configured
	Local   string          // this machine's collector id
	Now     func() time.Time
}

// Choice is what the person picked: a pane to go to, a thing an agent made
// to show, or a web address.
type Choice struct {
	Pane string
	View *View
	URL  string
}

// View names something an agent made, for the wall to show.
type View struct {
	Project string
	Kind    string
	Title   string
	Ref     string
}

// settle is the typing pause before the server is asked.
var settle = 200 * time.Millisecond

const workLimit = 30

type model struct {
	api  API
	o    Options
	w, h int

	input   textinput.Model
	seq     int
	asked   string // the query the shown server results answer
	places  *Places
	screens []Screen
	tickets []Ticket

	agents, vault, ticket, screen []Result
	rows                          []Result // what is listed, in order
	heads                         map[int]string
	cursor, top                   int
	moved                         bool // the person moved the selection for this query
	pending                       int  // server searches in flight
	errs                          map[string]string
	note                          string
	choice                        *Choice
}

type (
	overviewMsg struct {
		places *Places
		err    error
	}
	screensMsg struct{ screens []Screen }
	ticketsMsg struct{ tickets []Ticket }
	settledMsg struct{ seq int }
	agentsMsg  struct {
		seq  int
		hits []Result
		err  error
	}
	vaultMsg struct {
		seq  int
		hits []Result
		err  error
	}
	locatedMsg struct {
		run  string
		pane bool // the person asked for the pane, not the result itself
		err  error
	}
)

// New returns the search as a Bubble Tea model.
func New(api API, o Options) tea.Model {
	if o.Now == nil {
		o.Now = time.Now
	}
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = "search agents, notes, cards and open terminals"
	in.CharLimit = 200
	in.PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#515b6b", Dark: "#b8c2d1"})
	in.SetValue(o.Query)
	in.Focus()
	return &model{api: api, o: o, input: in, errs: map[string]string{}, heads: map[int]string{}}
}

// Chosen returns what the person picked once the program has ended.
func Chosen(m tea.Model) *Choice {
	if f, ok := m.(*model); ok {
		return f.choice
	}
	return nil
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		o, err := m.api.WorkOverview(ctx)
		return overviewMsg{places: NewPlaces(o, m.o.Local), err: err}
	}}
	if m.o.Screens != nil {
		cmds = append(cmds, func() tea.Msg { return screensMsg{m.o.Screens()} })
	}
	if m.o.Tickets != nil {
		cmds = append(cmds, func() tea.Msg { return ticketsMsg{m.o.Tickets()} })
	}
	if strings.TrimSpace(m.o.Query) != "" {
		m.seq++
		seq := m.seq
		cmds = append(cmds, func() tea.Msg { return settledMsg{seq} })
	}
	return tea.Batch(cmds...)
}

func (m *model) finish() tea.Cmd {
	if m.o.Finish != nil {
		choice := m.choice
		m.choice = nil // an embedded search may reopen; Escape must only cancel
		return m.o.Finish(choice)
	}
	return tea.Quit
}

func (m *model) query() string { return strings.TrimSpace(m.input.Value()) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.Width = max(10, m.w-12)
		m.arrange()
		return m, nil
	case overviewMsg:
		if msg.err != nil {
			m.errs["agents"] = msg.err.Error()
		}
		m.places = msg.places
		m.relocate()
		m.arrange()
		return m, nil
	case screensMsg:
		m.screens = msg.screens
		m.screen = searchScreens(m.screens, m.query(), 2, 20)
		m.arrange()
		return m, nil
	case ticketsMsg:
		m.tickets = msg.tickets
		m.ticket = searchTickets(m.tickets, m.query(), 15)
		m.arrange()
		return m, nil
	case settledMsg:
		if msg.seq != m.seq {
			return m, nil // more was typed since
		}
		q := m.query()
		if len([]rune(q)) < 2 {
			m.agents, m.vault, m.asked = nil, nil, ""
			m.arrange()
			return m, nil
		}
		m.pending += 2
		seq := msg.seq
		return m, tea.Batch(func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			hits, err := m.api.WorkSearch(ctx, q, workLimit)
			return agentsMsg{seq: seq, hits: fromWork(hits, nil, m.api.Endpoint()), err: err}
		}, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			hits, err := m.api.VaultSearch(ctx, q)
			return vaultMsg{seq: seq, hits: fromVault(hits, m.api.Endpoint()), err: err}
		})
	case agentsMsg:
		m.pending--
		if msg.seq != m.seq {
			return m, nil
		}
		delete(m.errs, "agents")
		if msg.err != nil {
			m.errs["agents"] = msg.err.Error()
		}
		m.agents, m.asked = msg.hits, m.query()
		m.relocate()
		m.arrange()
		return m, nil
	case vaultMsg:
		m.pending--
		if msg.seq != m.seq {
			return m, nil
		}
		delete(m.errs, "vault")
		if msg.err != nil {
			m.errs["vault"] = msg.err.Error()
		}
		m.vault = msg.hits
		m.arrange()
		return m, nil
	case locatedMsg:
		// The run's session is now known (or cannot be): go where it leads.
		m.relocate()
		m.arrange()
		for _, r := range m.rows {
			if r.Run == msg.run {
				if msg.pane {
					return m.toPane(r, true)
				}
				return m.choose(r, true)
			}
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, m.finish()
		case "up", "ctrl+p":
			m.move(-1)
			return m, nil
		case "down", "ctrl+n":
			m.move(1)
			return m, nil
		case "pgup":
			m.move(-m.listHeight() / 2)
			return m, nil
		case "pgdown":
			m.move(m.listHeight() / 2)
			return m, nil
		case "enter":
			if m.cursor < len(m.rows) {
				return m.choose(m.rows[m.cursor], false)
			}
			return m, nil
		case "tab":
			if m.cursor < len(m.rows) {
				return m.toPane(m.rows[m.cursor], false)
			}
			return m, nil
		case "ctrl+o":
			if m.cursor < len(m.rows) && m.rows[m.cursor].URL != "" {
				m.choice = &Choice{URL: m.rows[m.cursor].URL}
				return m, m.finish()
			}
			m.note = "this result has no web page"
			return m, nil
		}
		before := m.input.Value()
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if m.input.Value() == before {
			return m, cmd
		}
		// What is already here is searched at once; the server after a pause.
		m.note = ""
		m.screen = searchScreens(m.screens, m.query(), 2, 20)
		m.ticket = searchTickets(m.tickets, m.query(), 15)
		m.cursor, m.top, m.moved = 0, 0, false
		m.arrange()
		m.seq++
		seq := m.seq
		return m, tea.Batch(cmd, tea.Tick(settle, func(time.Time) tea.Msg { return settledMsg{seq} }))
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// choose acts on a result. A thing an agent made is shown itself; anything
// else goes to its open pane if it has one, else to its web page. A result
// whose session is not known yet is looked up first.
func (m *model) choose(r Result, looked bool) (tea.Model, tea.Cmd) {
	switch {
	case r.Viewable():
		m.choice = &Choice{View: &View{Project: r.Project, Kind: r.Kind, Title: r.Title, Ref: r.Ref}}
		return m, m.finish()
	case r.Pane != "":
		m.choice = &Choice{Pane: r.Pane}
		return m, m.finish()
	case r.Group == "agents" && !looked && m.places != nil && !m.places.Known(r.Run):
		return m, m.lookUp(r.Run, false)
	case r.URL != "":
		m.choice = &Choice{URL: r.URL}
		return m, m.finish()
	}
	m.note = "nowhere to go for this result"
	return m, nil
}

// toPane goes to the pane a result came from, if it is still open here.
func (m *model) toPane(r Result, looked bool) (tea.Model, tea.Cmd) {
	switch {
	case r.Pane != "":
		m.choice = &Choice{Pane: r.Pane}
		return m, m.finish()
	case r.Group == "agents" && !looked && m.places != nil && !m.places.Known(r.Run):
		return m, m.lookUp(r.Run, true)
	case r.Group == "agents":
		m.note = "its pane is no longer open on this machine"
	default:
		m.note = "this result did not come from a pane"
	}
	return m, nil
}

func (m *model) lookUp(run string, pane bool) tea.Cmd {
	m.note = "finding where that ran…"
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s, err := m.api.WorkRun(ctx, run)
		if err == nil {
			m.places.add(s)
		}
		return locatedMsg{run: run, pane: pane, err: err}
	}
}

// relocate fills in panes and tabs for the agent results once the overview
// is known, and lists results whose pane is open here first.
func (m *model) relocate() {
	if m.places == nil {
		return
	}
	for i := range m.agents {
		r := &m.agents[i]
		project := strings.TrimSpace(strings.SplitN(r.Where, " / ", 2)[0])
		pane, tab, ok := m.places.Pane(r.Run)
		if ok {
			r.Pane = pane
		} else {
			r.Pane = ""
			if tab == "" {
				tab = m.places.Tab(r.Run)
			}
		}
		if tab != "" {
			r.Where = strings.Trim(project+" / "+tab, " /")
		}
	}
	sort.SliceStable(m.agents, func(i, j int) bool { return m.agents[i].Pane != "" && m.agents[j].Pane == "" })
}

// arrange builds the listed rows and where each group's heading goes.
func (m *model) arrange() {
	// Results arrive from three places at different times. A selection the
	// person made is kept; until they move, the selection stays at the top.
	keep := Result{}
	if m.moved && m.cursor < len(m.rows) {
		keep = m.rows[m.cursor]
	}
	if !m.moved {
		m.cursor = 0
	}
	m.rows, m.heads = m.rows[:0], map[int]string{}
	for _, g := range []struct {
		name string
		rows []Result
	}{{"Agents and what they made", m.agents}, {"Notes, cards and tasks", m.vault}, {"Tickets", m.ticket}, {"On screen in open terminals", m.screen}} {
		if len(g.rows) == 0 {
			continue
		}
		m.heads[len(m.rows)] = g.name
		m.rows = append(m.rows, g.rows...)
	}
	m.cursor = min(m.cursor, max(0, len(m.rows)-1))
	for i, r := range m.rows {
		if keep.Title != "" && r.Title == keep.Title && r.Group == keep.Group && r.Run == keep.Run && r.Pane == keep.Pane {
			m.cursor = i
		}
	}
	m.reveal()
}

func (m *model) move(d int) {
	m.moved = true
	m.cursor = max(0, min(m.cursor+d, len(m.rows)-1))
	m.reveal()
}

// Each result is two lines, and a group heading above its first result one more.
func (m *model) lineOf(i int) int {
	n := 0
	for j := 0; j < i; j++ {
		n += 2
		if _, ok := m.heads[j]; ok {
			n++
		}
	}
	return n
}

func (m *model) listHeight() int { return max(2, m.h-4) }

func (m *model) reveal() {
	if len(m.rows) == 0 {
		m.top = 0
		return
	}
	first := m.lineOf(m.cursor)
	last := first + 2
	if _, ok := m.heads[m.cursor]; ok {
		last++
	}
	if first < m.top {
		m.top = first
	}
	if last > m.top+m.listHeight() {
		m.top = last - m.listHeight()
	}
}

var (
	dim    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "244", Dark: "245"})
	accent = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "81"})
	strong = lipgloss.NewStyle().Bold(true)
	bad    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})
	here   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "114"})
)

func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func ago(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d < 300*24*time.Hour:
		return t.Local().Format("Jan 2")
	}
	return t.Local().Format("Jan 2 2006")
}

func (m *model) View() string {
	if m.w <= 0 || m.h <= 0 {
		return ""
	}
	var b strings.Builder
	count := ""
	switch {
	case m.pending > 0:
		count = "searching…"
	case m.query() != "":
		count = fmt.Sprintf("%d results", len(m.rows))
	}
	head := " " + strong.Render("find") + "  " + m.input.View()
	gap := max(1, m.w-lipgloss.Width(head)-lipgloss.Width(count)-1)
	b.WriteString(head + strings.Repeat(" ", gap) + dim.Render(count) + "\n")
	b.WriteString(dim.Render(strings.Repeat("─", m.w)) + "\n")

	var lines []string
	now := m.o.Now()
	for i, r := range m.rows {
		if name, ok := m.heads[i]; ok {
			lines = append(lines, " "+dim.Render(name))
		}
		mark := "  "
		if i == m.cursor {
			mark = accent.Render("▌ ")
		}
		title := r.Title
		if r.Snippet != "" && r.Snippet != r.Title {
			title += "  " + r.Snippet
		}
		kind := fmt.Sprintf("%-7s", clip(r.Kind, 7))
		lines = append(lines, mark+accent.Render(kind)+" "+clip(title, m.w-12))
		meta := []string{}
		for _, s := range []string{r.Where, ago(r.When, now)} {
			if s != "" {
				meta = append(meta, s)
			}
		}
		// Say what Enter does for this result.
		goes := ""
		switch {
		case r.Viewable() && r.Pane != "":
			goes = dim.Render("enter: view · tab: ") + here.Render("● its pane")
		case r.Viewable():
			goes = dim.Render("enter: view")
		case r.Pane != "":
			goes = here.Render("● enter: go to pane")
		case r.URL != "":
			goes = dim.Render("enter: browser")
		}
		text := clip(strings.Join(meta, " · "), m.w-44)
		if text != "" && goes != "" {
			text += " · "
		}
		lines = append(lines, "          "+dim.Render(text)+goes)
	}
	if len(m.rows) == 0 {
		msg := "Type to search notes, cards, tasks, what agents made and open terminals."
		if m.query() != "" && m.pending == 0 && m.asked == m.query() {
			msg = "Nothing found."
		}
		lines = append(lines, "", "  "+dim.Render(clip(msg, m.w-4)))
	}
	for i := m.top; i < len(lines) && i < m.top+m.listHeight(); i++ {
		b.WriteString(lines[i] + "\n")
	}
	for i := len(lines) - m.top; i < m.listHeight(); i++ {
		b.WriteString("\n")
	}
	b.WriteString(dim.Render(strings.Repeat("─", m.w)) + "\n")
	foot := "↑/↓ move · enter open · tab its pane · ctrl+o browser · esc close"
	switch {
	case m.note != "":
		foot = m.note
	case len(m.errs) > 0:
		parts := []string{}
		for _, k := range []string{"agents", "vault"} {
			if e, ok := m.errs[k]; ok {
				parts = append(parts, k+": "+e)
			}
		}
		return b.String() + bad.Render(clip(" "+strings.Join(parts, " · "), m.w))
	}
	b.WriteString(dim.Render(clip(" "+foot, m.w)))
	return b.String()
}
