// Package messageui presents the messaging attention queue and a shared reply
// composer. The selected destination is copied when composition starts.
package messageui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/messages"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type API interface {
	Items(context.Context, bool) ([]messages.Item, error)
	Thread(context.Context, string) (messages.Thread, error)
	Draft(context.Context, messages.DraftInput) (messages.Draft, error)
	Send(context.Context, string) (messages.Draft, error)
	Attachments(context.Context) ([]messages.Attachment, error)
	Focus(context.Context, string, string) error
	Attention(context.Context, string, string) error
	Do(context.Context, string, string, any, any) error
}
type Options struct {
	Notes       *apiclient.Client
	Trash       bool
	All         bool
	Service     string
	Attachments []messages.Attachment
	IRCClient   string
	FocusPane   func() error
}
type Model struct {
	link                      linkState
	query                     string
	serviceCursor             int
	trash                     bool
	provider                  string
	api                       API
	opts                      Options
	w, h, cursor, top, scroll int
	items                     []messages.Item
	thread                    messages.Thread
	mode                      string
	all, loading, busy        bool
	status                    string
	input                     textarea.Model
	target                    *messages.Item
	selected                  []messages.Attachment
	library                   []messages.Attachment
	pick                      int
	requestID, requestBody    string
	drafts                    []messages.Draft
	review                    *messages.Draft
}
type itemsMsg struct {
	trash bool
	items []messages.Item
	err   error
	all   bool
}
type threadMsg struct {
	thread messages.Thread
	id     string
	err    error
}
type attachmentMsg struct {
	items []messages.Attachment
	err   error
}
type sentMsg struct {
	draft messages.Draft
	err   error
	save  bool
}
type statusMsg struct {
	focus bool
	text  string
	err   error
}
type draftsMsg struct {
	items   []messages.Draft
	err     error
	refresh bool
}
type tickMsg struct{}

func New(api API, o Options) *Model {
	t := textarea.New()
	t.Placeholder = "Write your reply…"
	t.CharLimit = 20000
	t.ShowLineNumbers = false
	t.SetHeight(5)
	// Pair text and background explicitly. A terminal's ANSI black can be
	// gray (or white in a light palette); textarea's defaults only set one
	// side of the pair, making the focused line and placeholder unreadable.
	fg := lipgloss.AdaptiveColor{Light: "#20242b", Dark: "#f1f3f5"}
	bg := lipgloss.AdaptiveColor{Light: "#edf0f4", Dark: "#252a33"}
	muted := lipgloss.AdaptiveColor{Light: "#515b6b", Dark: "#b8c2d1"}
	base := lipgloss.NewStyle().Foreground(fg).Background(bg)
	for _, style := range []*textarea.Style{&t.FocusedStyle, &t.BlurredStyle} {
		style.Base, style.Text, style.CursorLine = base, base, base
		style.Placeholder = base.Foreground(muted)
		style.Prompt = base.Foreground(muted)
		style.EndOfBuffer = base.Foreground(bg)
	}
	return &Model{api: api, opts: o, mode: "focus", loading: true, input: t, all: o.All, provider: o.Service, trash: o.Trash}
}
func (m *Model) Init() tea.Cmd { return tea.Batch(m.load(), tick()) }
func tick() tea.Cmd            { return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }
func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}
func (m *Model) load() tea.Cmd {
	all := m.all
	trash := m.trash
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		var v []messages.Item
		var e error
		if trash {
			e = m.api.Do(c, "GET", "/items?all=1&trash=1", nil, &v)
		} else {
			v, e = m.api.Items(c, all)
		}
		return itemsMsg{items: v, err: e, all: all, trash: trash}
	}
}
func (m *Model) loadDrafts(refresh bool) tea.Cmd {
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		var ds []messages.Draft
		e := m.api.Do(c, "GET", "/drafts", nil, &ds)
		return draftsMsg{items: ds, err: e, refresh: refresh}
	}
}
func (m *Model) current() *messages.Item {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	return &m.items[m.cursor]
}
func (m *Model) loadThread() tea.Cmd {
	it := m.current()
	if it == nil {
		m.thread = messages.Thread{}
		return nil
	}
	id := it.Conversation.ID
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		v, e := m.api.Thread(c, id)
		return threadMsg{v, id, e}
	}
}
func (m *Model) move(n int) tea.Cmd {
	m.cursor = max(0, min(len(m.items)-1, m.cursor+n))
	m.scroll = 0
	return m.loadThread()
}
func (m *Model) startCompose() {
	if m.trash {
		m.status = "Restore this conversation before replying."
		return
	}
	it := m.current()
	if it == nil {
		return
	}
	if it.Message.ID == "" {
		m.status = "No imported message to reply to yet."
		return
	}
	copy := *it
	// Mail replies need an incoming parent, even when All shows our own
	// newer message as the conversation's latest activity.
	if copy.Message.Outgoing && (copy.Conversation.Route.Provider == "gmail" || copy.Conversation.Route.Provider == "outlook") {
		var parent messages.Message
		if m.thread.Conversation.ID == copy.Conversation.ID {
			for _, msg := range m.thread.Messages {
				if !msg.Outgoing && msg.ID != "" && (parent.ID == "" || msg.At.After(parent.At)) {
					parent = msg
				}
			}
		}
		if parent.ID == "" {
			m.status = "No incoming email loaded to reply to yet."
			return
		}
		copy.Message = parent
	}
	m.target = &copy
	m.selected = append([]messages.Attachment{}, m.opts.Attachments...)
	m.input.SetValue("")
	m.input.Focus()
	m.mode = "compose"
	m.requestID = messages.ID()
	m.requestBody = ""
	m.status = ""
}
func (m *Model) send(save bool) tea.Cmd {
	if m.target == nil || m.busy {
		return nil
	}
	var ids []string
	for _, a := range m.selected {
		ids = append(ids, a.ID)
	}
	body := m.input.Value()
	key := body + "\x00" + strings.Join(ids, ",")
	if strings.TrimSpace(body) == "" && len(ids) == 0 {
		m.status = "Write a reply or attach a file first"
		return nil
	}
	if m.requestBody != "" && key != m.requestBody {
		m.requestID = messages.ID()
	}
	m.requestBody = key
	in := messages.DraftInput{RequestID: m.requestID, ReplyTo: m.target.Message.ID, Body: body, Attachments: ids}
	m.busy = true
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		d, e := m.api.Draft(c, in)
		if e == nil && !save {
			d, e = m.api.Send(c, d.ID)
		}
		return sentMsg{d, e, save}
	}
}
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case linkTargetsMsg:
		if m.mode != "link" {
			return m, nil
		}
		if x.err != nil {
			m.status = x.err.Error()
		} else {
			m.link.targets = x.targets
			m.status = ""
		}
		return m, nil
	case contactsMsg:
		if m.mode == "contact" {
			m.link.names = x.names
		}
		return m, nil
	case linkedMsg:
		m.busy = false
		if x.err != nil {
			m.status = x.err.Error()
			return m, nil
		}
		m.mode = "focus"
		m.status = "Message attached to note"
		return m, nil
	case tea.WindowSizeMsg:
		m.w, m.h = x.Width, x.Height
		m.input.SetWidth(max(20, m.rightWidth()-4))
		m.input.SetHeight(max(3, min(8, m.h-15)))
	case tickMsg:
		if m.busy {
			return m, tick()
		}
		if m.mode == "drafts" || m.mode == "review" {
			return m, tea.Batch(tick(), m.load(), m.loadDrafts(true))
		}
		return m, tea.Batch(tick(), m.load())
	case itemsMsg:
		if x.all != m.all || x.trash != m.trash {
			return m, nil
		}
		m.loading = false
		if x.err != nil {
			m.status = x.err.Error()
			return m, nil
		}
		keep := ""
		if it := m.current(); it != nil {
			keep = it.Conversation.ID
		}
		if m.provider != "" {
			filtered := []messages.Item{}
			for _, it := range x.items {
				if strings.EqualFold(it.Conversation.Service, m.provider) || ((strings.EqualFold(m.provider, "Messages") || strings.EqualFold(m.provider, "iMessage")) && it.Conversation.Route.Provider == "imessage") {
					filtered = append(filtered, it)
				}
			}
			x.items = filtered
		}
		if m.query != "" {
			filtered := []messages.Item{}
			for _, it := range x.items {
				hay := strings.ToLower(it.Conversation.Name + " " + it.Message.Sender + " " + it.Message.Body + " " + it.Conversation.Service)
				ok := true
				for _, word := range strings.Fields(strings.ToLower(m.query)) {
					if !strings.Contains(hay, word) {
						ok = false
					}
				}
				if ok {
					filtered = append(filtered, it)
				}
			}
			x.items = filtered
		}
		m.items = x.items
		m.cursor = min(m.cursor, max(0, len(m.items)-1))
		for i, it := range m.items {
			if it.Conversation.ID == keep {
				m.cursor = i
				break
			}
		}
		return m, m.loadThread()
	case threadMsg:
		if it := m.current(); it != nil && it.Conversation.ID == x.id {
			if x.err != nil {
				m.status = x.err.Error()
			} else {
				m.thread = x.thread
			}
		}
	case attachmentMsg:
		if x.err != nil {
			m.status = x.err.Error()
			return m, nil
		}
		m.library = x.items
		m.pick = 0
		m.mode = "attach"
	case sentMsg:
		m.busy = false
		if x.err != nil {
			m.status = x.err.Error()
			return m, nil
		}
		if x.save {
			m.status = "Draft saved. No message sent."
		} else {
			m.status = messages.DeliveryStatus(x.draft)
		}
		m.mode = "focus"
		m.target = nil
		m.review = nil
		m.selected = nil
		m.opts.Attachments = nil
		return m, m.load()
	case statusMsg:
		m.busy = false
		if x.focus && x.err == nil && m.opts.FocusPane != nil {
			return m, tea.Quit
		}
		if x.err != nil {
			m.status = x.err.Error()
		} else {
			m.status = x.text
		}
		return m, m.load()
	case draftsMsg:
		if x.refresh && m.mode != "drafts" && m.mode != "review" {
			return m, nil
		}
		m.busy = false
		if x.err != nil {
			m.status = x.err.Error()
		} else {
			selected := ""
			if m.pick >= 0 && m.pick < len(m.drafts) {
				selected = m.drafts[m.pick].ID
			}
			m.drafts = x.items
			if x.refresh {
				m.pick = 0
				for i, d := range m.drafts {
					if d.ID == selected {
						m.pick = i
					}
					if m.review != nil && m.review.ID == d.ID {
						copy := d
						m.review = &copy
					}
				}
			} else {
				m.pick = 0
				m.mode = "drafts"
			}
		}
	case tea.KeyMsg:
		if x.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.busy {
			return m, nil
		}
		if m.mode == "link" {
			return m, m.linkKey(x)
		}
		if m.mode == "contact" || m.mode == "search" || m.mode == "service" {
			return m, m.utilityKey(x)
		}
		if m.mode == "compose" {
			switch x.String() {
			case "esc":
				m.mode = "focus"
				m.input.Blur()
				m.status = "Reply kept in this window; press r to resume"
				return m, nil
			case "ctrl+s":
				return m, m.send(false)
			case "ctrl+d":
				return m, m.send(true)
			case "ctrl+a":
				return m, func() tea.Msg {
					c, cancel := ctx()
					defer cancel()
					a, e := m.api.Attachments(c)
					return attachmentMsg{a, e}
				}
			case "ctrl+x":
				m.selected = nil
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		if m.mode == "attach" {
			switch x.String() {
			case "esc":
				m.mode = "compose"
			case "j", "down":
				m.pick = min(len(m.library)-1, m.pick+1)
			case "k", "up":
				m.pick = max(0, m.pick-1)
			case "enter", " ":
				if m.pick >= 0 && m.pick < len(m.library) {
					a := m.library[m.pick]
					found := -1
					for i, v := range m.selected {
						if v.ID == a.ID {
							found = i
						}
					}
					if found >= 0 {
						m.selected = append(m.selected[:found], m.selected[found+1:]...)
					} else if len(m.selected) < 8 {
						m.selected = append(m.selected, a)
					} else {
						m.status = "A reply can have up to 8 attachments"
					}
				}
			case "p":
				providers := []string{"", "Signal", "Telegram", "Messages", "Gmail", "Outlook", "Slack", "Outlook (Chrome)", "Calendar (Chrome)"}
				next := 0
				for i, p := range providers {
					if p == m.provider {
						next = (i + 1) % len(providers)
						break
					}
				}
				m.provider = providers[next]
				m.all = true
				m.cursor = 0
				m.scroll = 0
				m.loading = true
				return m, m.load()
			case "tab":
				m.mode = "compose"
			}
			return m, nil
		}
		if m.mode == "drafts" {
			switch x.String() {
			case "esc", "q":
				m.mode = "focus"
			case "j", "down":
				m.pick = min(len(m.drafts)-1, m.pick+1)
			case "k", "up":
				m.pick = max(0, m.pick-1)
			case "enter":
				if m.pick >= 0 && m.pick < len(m.drafts) {
					d := m.drafts[m.pick]
					m.review = &d
					m.mode = "review"
					m.scroll = 0
				}
			}
			return m, nil
		}
		if m.mode == "review" {
			switch x.String() {
			case "esc", "q":
				m.mode = "drafts"
			case "ctrl+s":
				if m.review != nil && (m.review.State == "draft" || m.review.State == "failed" || m.review.State == "blocked") {
					id := m.review.ID
					m.busy = true
					return m, func() tea.Msg {
						c, cancel := ctx()
						defer cancel()
						d, e := m.api.Send(c, id)
						return sentMsg{draft: d, err: e}
					}
				}
			case "pgdown", "down", "j":
				m.scroll++
			case "pgup", "up", "k":
				m.scroll = max(0, m.scroll-1)
			}
			return m, nil
		}
		switch x.String() {
		case "l":
			return m, m.startLink()
		case "C", "/":
			it := m.current()
			if x.String() == "C" && it == nil {
				return m, nil
			}
			i := textinput.New()
			i.CharLimit = 200
			i.Focus()
			m.link = linkState{input: i}
			if x.String() == "/" {
				m.mode = "search"
				m.link.input.Prompt = "Find messages: "
				m.link.input.SetValue(m.query)
				return m, textinput.Blink
			}
			m.mode = "contact"
			m.link.message = *it
			m.link.input.Prompt = "Contact name: "
			return m, func() tea.Msg {
				c, cancel := ctx()
				defer cancel()
				var names []string
				e := m.api.Do(c, "GET", "/contacts", nil, &names)
				return contactsMsg{names, e}
			}
		case "f":
			m.mode = "service"
			m.serviceCursor = 0
			return m, nil
		case "q", "esc":
			return m, tea.Quit
		case "j", "down":
			return m, m.move(1)
		case "k", "up":
			return m, m.move(-1)
		case "pgdown":
			m.scroll += max(1, m.h/2)
		case "pgup":
			m.scroll = max(0, m.scroll-m.h/2)
		case "r":
			if m.target != nil {
				m.mode = "compose"
				m.input.Focus()
			} else {
				m.startCompose()
			}
			return m, textarea.Blink
		case "n":
			m.target = nil
			m.startCompose()
			return m, textarea.Blink
		case "a":
			if m.target == nil {
				m.startCompose()
			}
			if m.target != nil {
				return m, func() tea.Msg {
					c, cancel := ctx()
					defer cancel()
					a, e := m.api.Attachments(c)
					return attachmentMsg{a, e}
				}
			}
		case "p":
			providers := []string{"", "Signal", "Telegram", "Messages", "Gmail", "Outlook", "Slack", "Outlook (Chrome)", "Calendar (Chrome)"}
			next := 0
			for i, p := range providers {
				if p == m.provider {
					next = (i + 1) % len(providers)
					break
				}
			}
			m.provider = providers[next]
			m.all = true
			m.cursor = 0
			m.scroll = 0
			m.loading = true
			return m, m.load()
		case "tab":
			m.all = !m.all
			m.cursor = 0
			m.scroll = 0
			m.loading = true
			return m, m.load()
		case "x":
			if !m.trash {
				return m, m.trashConversation(true)
			}
		case "T":
			m.trash = !m.trash
			m.all = true
			m.cursor = 0
			m.scroll = 0
			m.loading = true
			return m, m.load()
		case "d", "z", "u":
			if x.String() == "u" && m.trash {
				return m, m.trashConversation(false)
			}
			if it := m.current(); it != nil && it.Message.ID != "" {
				id := it.Message.ID
				action := map[string]string{"d": "handled", "z": "snooze", "u": "reopen"}[x.String()]
				m.busy = true
				return m, func() tea.Msg {
					c, cancel := ctx()
					defer cancel()
					e := m.api.Attention(c, id, action)
					return statusMsg{text: map[string]string{"handled": "Marked handled", "snooze": "Snoozed for one hour", "reopen": "Returned to focus"}[action], err: e}
				}
			}
		case "D":
			m.busy = true
			return m, m.loadDrafts(false)
		case "enter":
			if it := m.current(); it != nil && it.Message.ID != "" {
				id := it.Message.ID
				m.busy = true
				return m, func() tea.Msg {
					c, cancel := ctx()
					defer cancel()
					e := m.api.Focus(c, id, m.opts.IRCClient)
					if e == nil && m.opts.FocusPane != nil {
						e = m.opts.FocusPane()
					}
					return statusMsg{text: "Opened selected-message context in IRC", err: e, focus: true}
				}
			}
		}
	}
	return m, nil
}

var (
	accent = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "55", Dark: "177"})
	quiet  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "242", Dark: "245"})
	amber  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "179"})
	bold   = lipgloss.NewStyle().Bold(true)
)

func cut(s string, w int) string {
	return ansi.Truncate(strings.NewReplacer("\n", " ", "\t", " ").Replace(messages.Plain(s)), max(1, w), "…")
}
func wrap(s string, w int) []string {
	return strings.Split(ansi.Hardwrap(messages.Plain(s), max(1, w), true), "\n")
}
func (m *Model) leftWidth() int {
	if m.w < 90 {
		return 0
	}
	return min(42, m.w/3)
}
func (m *Model) rightWidth() int {
	if n := m.leftWidth(); n > 0 {
		return m.w - n - 5
	}
	return m.w - 4
}
func (m *Model) View() string {
	if m.w < 40 || m.h < 12 {
		return "Messages needs a terminal at least 40 × 12. Ctrl+C exits."
	}
	title := "Focus"
	if m.all {
		title = "All conversations"
	}
	if m.trash {
		title = "Trash"
	}
	if m.provider != "" {
		title += " · " + m.provider
	}
	header := accent.Bold(true).Render("Messages") + "  " + title + fmt.Sprintf(" (%d)", len(m.items))
	label := "Demo · simulated delivery"
	for _, it := range m.items {
		if !it.Conversation.Demo {
			label = "Live connections · demos simulated"
			break
		}
	}
	if m.target != nil && !m.target.Conversation.Demo || m.review != nil && !m.review.Target.Demo {
		label = "Live connections · demos simulated"
	}
	header += "  " + amber.Render(label)
	height := m.h - 5
	var body string
	if m.mode == "link" {
		body = m.linkView(height)
	} else if m.mode == "contact" || m.mode == "search" || m.mode == "service" {
		body = m.utilityView(height)
	} else if m.mode == "drafts" || m.mode == "review" {
		body = m.draftView(height)
	} else if m.mode == "attach" {
		body = m.attachmentView(height)
	} else {
		lw := m.leftWidth()
		detail := m.detailLines(m.rightWidth())
		if m.mode == "compose" {
			detail = m.composeLines(m.rightWidth())
		}
		if lw == 0 {
			if m.mode != "compose" {
				detail = append(m.listLines(m.w-4, min(6, height/3)), detail...)
			}
			body = fit(detail, height, m.w-2)
		} else {
			left := fit(m.listLines(lw, height), height, lw)
			right := fit(detail, height, m.rightWidth())
			body = lipgloss.JoinHorizontal(lipgloss.Top, left, "  "+quiet.Render("│")+" ", right)
		}
	}
	help := "r reply · l link note/task · C contact · / find · f service · Tab all · x trash · T view trash · D drafts · q close"
	if m.trash {
		help = "↑↓ choose  u restore  T back  p service  q close"
	}
	switch m.mode {
	case "compose":
		help = "Ctrl+S simulate send  Ctrl+D save draft  Ctrl+A attach  Ctrl+X clear files  Esc back"
		if m.target != nil && !m.target.Conversation.Demo {
			help = "Ctrl+S send to " + messages.Plain(m.target.Conversation.Service) + "  Ctrl+D save draft  Ctrl+A attach  Ctrl+X clear files  Esc back"
		}
	case "attach":
		help = "↑↓ choose  Space toggle attachment  Tab/Esc return to reply"
	case "drafts":
		help = "↑↓ choose  Enter review  Esc back"
	case "review":
		help = "Ctrl+S simulate send  PgUp/PgDn scroll  Esc drafts"
		if m.review != nil && !m.review.Target.Demo {
			help = "Ctrl+S send to " + messages.Plain(m.review.Target.Service) + "  PgUp/PgDn scroll  Esc drafts"
			if m.review.State == "sending" || m.review.State == "queued" {
				help = "Waiting for " + messages.Plain(m.review.Target.Service) + " · status refreshes automatically  Esc drafts"
			}
			if m.review.State == "uncertain" {
				help = "Check " + messages.Plain(m.review.Target.Service) + " before composing another reply · no automatic retry  Esc drafts"
			}
			if m.review.State == "submitted" {
				help = "Handed to Messages · delivery unconfirmed · check on Mac  Esc drafts"
			}
			if m.review.State == "sent" {
				help = "Delivered to " + messages.Plain(m.review.Target.Service) + "  PgUp/PgDn scroll  Esc drafts"
			}
		}
	}
	status := m.status
	if m.loading {
		status = "Loading conversations…"
	}
	if m.busy {
		status = "Working…"
	}
	if status == "" && len(m.opts.Attachments) > 0 {
		status = fmt.Sprintf("%d wall item(s) ready. Choose a conversation and press r.", len(m.opts.Attachments))
	}
	return " " + ansi.Truncate(header, m.w-2, "…") + "\n\n " + strings.ReplaceAll(body, "\n", "\n ") + "\n " + amber.Render(cut(status, m.w-2)) + "\n " + quiet.Render(cut(help, m.w-2))
}
func fit(lines []string, h, w int) string {
	out := make([]string, h)
	for i := 0; i < h; i++ {
		s := ""
		if i < len(lines) {
			s = lines[i]
		}
		s = ansi.Truncate(s, w, "…")
		out[i] = s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
	}
	return strings.Join(out, "\n")
}
func (m *Model) listLines(w, h int) []string {
	if len(m.items) == 0 {
		if m.trash {
			return []string{"Trash is empty.", "", "T returns to conversations."}
		}
		return []string{"Nothing needs attention.", "", quiet.Render("Tab shows all conversations and contacts.")}
	}
	count := max(1, h/4)
	start := max(0, min(m.cursor-count+1, len(m.items)-count))
	out := []string{}
	for i := start; i < len(m.items) && len(out) < h; i++ {
		it := m.items[i]
		prefix := "  "
		if i == m.cursor {
			prefix = "› "
		}
		head := prefix + cut(it.Conversation.Name, w-2)
		if i == m.cursor {
			head = accent.Bold(true).Render(head)
		}
		category := it.Message.Category
		if it.Message.Outgoing {
			category = "Sent"
		} else if it.Message.Handled {
			category = "Handled"
		} else if it.Message.SnoozedUntil.After(time.Now()) {
			category = "Snoozed"
		}
		service := messages.AccountDisplay(it.Conversation)
		if !it.LastActivity.IsZero() {
			service += " · " + it.LastActivity.Local().Format("Jan 2 15:04")
		}
		out = append(out, head, quiet.Render("  "+cut(service, w-2)), "  "+cut(strings.TrimSuffix(category+" · "+it.Message.Sender, " · "), w-2), "")
	}
	return out
}
func (m *Model) detailLines(w int) []string {
	it := m.current()
	if it == nil {
		return nil
	}
	out := []string{bold.Render(cut(it.Conversation.Name, w)), quiet.Render(cut(messages.Destination(it.Conversation), w)), amber.Render(cut(it.Message.Reason, w)), ""}
	if m.thread.Conversation.ID != it.Conversation.ID {
		return append(out, "Loading context…")
	}
	if len(m.thread.Messages) == 0 {
		text := "This contact is connected. New incoming messages will appear here."
		if it.Conversation.Service == "Signal" {
			text += " Earlier Signal history is not imported by this linked device."
		}
		return append(out, wrap(text, w)...)
	}
	var lines []string
	for _, msg := range m.thread.Messages {
		prefix := ""
		if msg.ID == it.Message.ID {
			prefix = "› "
		}
		lines = append(lines, accent.Render(cut(prefix+msg.Sender+"  "+msg.At.Local().Format("Jan 2 15:04"), w)))
		lines = append(lines, wrap(msg.Body, w)...)
		for _, a := range msg.Attachments {
			lines = append(lines, quiet.Render(cut("↳ "+a.Name+fmt.Sprintf(" (%d bytes)", a.Size), w)))
		}
		lines = append(lines, "")
	}
	available := max(1, m.h-10)
	start := max(0, len(lines)-available-m.scroll)
	end := min(len(lines), start+available)
	return append(out, lines[start:end]...)
}
func (m *Model) composeLines(w int) []string {
	if m.target == nil {
		return nil
	}
	it := *m.target
	title := "Reply · " + it.Conversation.Name
	if !it.Conversation.Demo {
		title = "Live reply · " + it.Conversation.Name
	}
	out := []string{accent.Bold(true).Render(cut(title, w)), ""}
	out = append(out, wrap("To: "+messages.Destination(it.Conversation), w)...)
	if it.Conversation.Route.Provider == "imessage" {
		out = append(out, wrap("Conversation reply; inline quotes are unavailable. Delivery is confirmed in Messages on your Mac.", w)...)
	}
	out = append(out, quiet.Render(cut("In reply to "+it.Message.Sender+": "+strings.ReplaceAll(it.Message.Body, "\n", " "), w)), "")
	if len(m.selected) > 0 {
		for _, a := range m.selected {
			out = append(out, cut("↳ "+a.Name+fmt.Sprintf(" (%d bytes)", a.Size), w))
		}
	} else {
		out = append(out, quiet.Render("No attachments · Ctrl+A chooses a file"))
	}
	out = append(out, "", m.input.View())
	// textarea.View is multiline; normalize before the outer height clamp.
	return strings.Split(strings.Join(out, "\n"), "\n")
}
func (m *Model) attachmentView(h int) string {
	lines := []string{accent.Bold(true).Render("Attach to this reply"), ""}
	count := max(1, h-3)
	start := max(0, m.pick-count+1)
	for i := start; i < len(m.library) && len(lines) < h; i++ {
		a := m.library[i]
		mark := "[ ]"
		for _, s := range m.selected {
			if s.ID == a.ID {
				mark = "[x]"
			}
		}
		line := fmt.Sprintf("%s %s  %d bytes", mark, a.Name, a.Size)
		if i == m.pick {
			line = accent.Render("› " + line)
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	if len(m.library) == 0 {
		lines = append(lines, "No files yet. Share an item from the wall, or use amy messages attach.")
	}
	return fit(lines, h, m.w-2)
}
func (m *Model) draftView(h int) string {
	if m.mode == "review" && m.review != nil {
		d := m.review
		lines := []string{accent.Bold(true).Render("Review reply · " + d.State), ""}
		lines = append(lines, wrap("To: "+messages.Destination(d.Target), m.w-4)...)
		lines = append(lines, "Prepared by: "+d.Actor, "", messages.Plain(d.Body), "")
		if d.DeliveryError != "" {
			lines = append(lines, wrap("Delivery: "+d.DeliveryError, m.w-4)...)
		}
		if d.NativeID != "" {
			lines = append(lines, messages.Plain(d.Target.Service)+" message: "+d.NativeID)
		}
		for _, a := range d.Attachments {
			lines = append(lines, "↳ "+a.Name)
		}
		lines = strings.Split(strings.Join(lines, "\n"), "\n")
		start := min(m.scroll, max(0, len(lines)-h))
		return fit(lines[start:], h, m.w-2)
	}
	lines := []string{accent.Bold(true).Render("Drafts and delivery records"), ""}
	start := max(0, m.pick-max(1, (h-2)/3)+1)
	for i := start; i < len(m.drafts) && len(lines) < h; i++ {
		d := m.drafts[i]
		head := d.State + " · " + d.Target.Name + " · " + d.Actor
		if i == m.pick {
			head = accent.Render("› " + head)
		} else {
			head = "  " + head
		}
		lines = append(lines, cut(head, m.w-4), quiet.Render("  "+cut(strings.ReplaceAll(d.Body, "\n", " "), m.w-6)), "")
	}
	if len(m.drafts) == 0 {
		lines = append(lines, "No drafts yet. Press r on a message to compose a reply.")
	}
	return fit(lines, h, m.w-2)
}
