package chatui

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/base698/amythest/internal/messages"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// API is what the client uses from the messaging gateway.
type API interface {
	Items(context.Context, bool) ([]messages.Item, error)
	ThreadPage(context.Context, string, messages.Page) (messages.Thread, error)
	Draft(context.Context, messages.DraftInput) (messages.Draft, error)
	Send(context.Context, string) (messages.Draft, error)
	Attention(context.Context, string, string) error
	Search(context.Context, string, string, int) ([]messages.Hit, error)
	Events(context.Context, int64, messages.Listener, time.Duration) (messages.Events, error)
	Message(context.Context, string) (messages.Message, error)
}

type Options struct {
	Goto        *messages.Jump
	Listener    messages.Listener
	EventsAfter int64
	Now         func() time.Time
}

const (
	pageSize   = 200
	pollEvery  = 4 * time.Second
	eventRetry = 5 * time.Second
	maxWindows = 99
)

// eventWait is how long one event poll waits (a variable for tests).
var eventWait = 10 * time.Second

type Model struct {
	api      API
	o        Options
	w, h     int
	wins     []*window // wins[0] is the status window
	active   int
	input    textinput.Model
	focus    []messages.Item          // the attention queue, for window 1
	seen     map[string]time.Time     // conversation -> activity already noticed
	known    map[string]messages.Item // conversation -> latest item (all)
	started  bool                     // the first poll has happened
	eventSeq int64
	pending  *messages.Jump // a jump waiting for its window to load
	history  []string
	histAt   int
}

type (
	itemsMsg struct {
		focus, all []messages.Item
		err        error
	}
	pageMsg struct {
		conv  string
		older bool
		page  messages.Thread
		err   error
	}
	sentMsg struct {
		conv   string
		draft  messages.Draft
		thread bool
		err    error
	}
	searchMsg struct {
		win  *window
		hits []messages.Hit
		err  error
	}
	doneMsg struct {
		conv, text string
		err        error
	}
	eventsMsg struct {
		ev  messages.Events
		err error
	}
	jumpMsg struct {
		j   messages.Jump
		err error
	}
	tickMsg   struct{}
	listenMsg struct{}
)

func New(api API, o Options) *Model {
	if o.Now == nil {
		o.Now = time.Now
	}
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 5000
	in.Focus()
	m := &Model{api: api, o: o, input: in, seen: map[string]time.Time{}, known: map[string]messages.Item{}, eventSeq: -1}
	m.wins = []*window{{kind: kindStatus, refs: map[string]string{}, byRef: map[string]string{}}}
	if o.EventsAfter > 0 {
		m.eventSeq = o.EventsAfter
	}
	m.status("Welcome to Messages. Type /help for commands; Alt+1…9 switch windows.")
	return m
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, m.poll(), m.listen()}
	if m.o.Goto != nil {
		cmds = append(cmds, m.jump(*m.o.Goto))
	}
	return tea.Batch(cmds...)
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func (m *Model) status(format string, args ...any) {
	m.wins[0].note(m.o.Now(), format, args...)
}

func (m *Model) cur() *window { return m.wins[m.active] }

// --- loading -----------------------------------------------------------------

func (m *Model) poll() tea.Cmd {
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		focus, e := m.api.Items(c, false)
		if e != nil {
			return itemsMsg{err: e}
		}
		all, e := m.api.Items(c, true)
		return itemsMsg{focus: focus, all: all, err: e}
	}
}

func (m *Model) load(w *window, older bool) tea.Cmd {
	if w.kind != kindConv || w.loading {
		return nil
	}
	p := messages.Page{Limit: pageSize}
	switch {
	case older && len(w.msgs) > 0:
		p.Before = w.msgs[0].ID
	case !older && len(w.msgs) > 0:
		p.After = w.msgs[len(w.msgs)-1].ID
	}
	w.loading = true
	id := w.conv.ID
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		t, e := m.api.ThreadPage(c, id, p)
		return pageMsg{conv: id, older: older, page: t, err: e}
	}
}

func (m *Model) loadAround(w *window, msgID string) tea.Cmd {
	w.loading = true
	id := w.conv.ID
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		t, e := m.api.ThreadPage(c, id, messages.Page{Around: msgID, Limit: pageSize})
		return pageMsg{conv: id, page: t, err: e}
	}
}

func (m *Model) listen() tea.Cmd {
	since, who := m.eventSeq, m.o.Listener
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), eventWait+5*time.Second)
		defer cancel()
		ev, e := m.api.Events(c, since, who, eventWait)
		return eventsMsg{ev, e}
	}
}

func (m *Model) window(conv string) (int, *window) {
	for i, w := range m.wins {
		if w.kind == kindConv && w.conv.ID == conv {
			return i, w
		}
	}
	return -1, nil
}

// open finds or makes the window for a conversation.
func (m *Model) open(c messages.Conversation) (int, *window, tea.Cmd) {
	if i, w := m.window(c.ID); w != nil {
		return i, w, nil
	}
	if len(m.wins) >= maxWindows {
		m.status("Too many windows; /close some first.")
		return -1, nil, nil
	}
	w := newConvWindow(c)
	m.wins = append(m.wins, w)
	return len(m.wins) - 1, w, m.load(w, false)
}

func (m *Model) show(i int) tea.Cmd {
	if i < 0 || i >= len(m.wins) {
		return nil
	}
	m.active = i
	w := m.wins[i]
	w.level = actNone
	if w.kind == kindConv && !w.loaded {
		return m.load(w, false)
	}
	return nil
}

// --- updates -----------------------------------------------------------------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = x.Width, x.Height
		m.input.Width = max(10, m.w-len(m.prompt())-2)
	case tickMsg:
		return m, m.poll()
	case itemsMsg:
		return m, tea.Batch(m.onItems(x), tea.Tick(pollEvery, func(time.Time) tea.Msg { return tickMsg{} }))
	case pageMsg:
		return m, m.onPage(x)
	case sentMsg:
		_, w := m.window(x.conv)
		if w == nil {
			w = m.wins[0]
		}
		if x.err != nil {
			w.note(m.o.Now(), "-!- Not sent: %v", x.err)
			return m, nil
		}
		w.note(m.o.Now(), "-!- %s", messages.DeliveryStatus(x.draft))
		if x.thread && x.draft.Target.ID != x.conv {
			// The reply went to the thread's own conversation; count it here.
			r := w.replies[x.draft.ReplyTo]
			if w.replies == nil {
				w.replies = map[string]messages.ReplySummary{}
			}
			w.replies[x.draft.ReplyTo] = messages.ReplySummary{Conversation: x.draft.Target.ID, Count: r.Count + 1}
			w.note(m.o.Now(), "-!- Replied in the thread under [%s]: /thread %s opens it.", w.refFor(x.draft.ReplyTo), w.refFor(x.draft.ReplyTo))
			return m, nil
		}
		if w.kind == kindConv {
			return m, m.load(w, false)
		}
	case searchMsg:
		if x.err != nil {
			x.win.note(m.o.Now(), "-!- Search failed: %v", x.err)
			return m, nil
		}
		x.win.hits = x.hits
		x.win.loaded = true
		if len(x.hits) == 0 {
			x.win.note(m.o.Now(), "-!- Nothing found.")
		}
	case doneMsg:
		_, w := m.window(x.conv)
		if w == nil {
			w = m.wins[0]
		}
		if x.err != nil {
			w.note(m.o.Now(), "-!- %v", x.err)
		} else {
			w.note(m.o.Now(), "-!- %s", x.text)
		}
		return m, m.poll()
	case eventsMsg:
		return m, m.onEvents(x)
	case listenMsg:
		return m, m.listen()
	case jumpMsg:
		if x.err != nil {
			m.status("-!- Could not open that message: %v", x.err)
			return m, nil
		}
		return m, m.jump(x.j)
	case tea.KeyMsg:
		return m, m.key(x)
	}
	return m, nil
}

func (m *Model) onItems(x itemsMsg) tea.Cmd {
	if x.err != nil {
		m.status("-!- %v", x.err)
		return nil
	}
	m.focus = x.focus
	var cmds []tea.Cmd
	for _, it := range x.all {
		id := it.Conversation.ID
		m.known[id] = it
		if _, w := m.window(id); w != nil {
			w.conv = it.Conversation // contact names can change
		}
		prev, seen := m.seen[id]
		m.seen[id] = it.LastActivity
		if !m.started || it.Message.ID == "" || seen && !it.LastActivity.After(prev) {
			continue
		}
		// New activity: refresh its window, or open one for a new message to us.
		i, w := m.window(id)
		if w == nil {
			if it.Message.Outgoing {
				continue
			}
			var cmd tea.Cmd
			i, w, cmd = m.open(it.Conversation)
			if w == nil {
				continue
			}
			cmds = append(cmds, cmd)
		} else if w.loaded {
			cmds = append(cmds, m.load(w, false))
		}
		if i != m.active {
			w.level = max(w.level, levelOf(it.Message, it.Conversation))
		}
	}
	if !m.started {
		m.started = true
		// Like irssi's autojoin: a window for everything waiting on you.
		// Newest first, so Alt+2 is always the most recent.
		for _, it := range x.focus {
			if _, w, cmd := m.open(it.Conversation); w != nil {
				w.level = levelOf(it.Message, it.Conversation)
				cmds = append(cmds, cmd)
			}
		}
		if len(x.focus) > 0 {
			m.status("%d conversation(s) need attention: Alt+A goes to the busiest.", len(x.focus))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) onPage(x pageMsg) tea.Cmd {
	_, w := m.window(x.conv)
	if w == nil {
		return nil
	}
	w.loading = false
	if x.err != nil {
		w.note(m.o.Now(), "-!- %v", x.err)
		return nil
	}
	if x.page.Conversation.ID != "" {
		w.conv = x.page.Conversation
	}
	var more tea.Cmd
	if j := m.pending; j != nil && j.Conversation == x.conv && j.Message != "" {
		// A page around the jumped-to message replaces what was loaded;
		// anything newer is fetched after it.
		w.msgs = nil
		w.merge(x.page.Messages, false)
		w.hasBefore = x.page.HasBefore
	} else {
		// Keep the view still when scrolled back and newer lines arrive.
		before := 0
		if w.scroll > 0 && !x.older {
			before = len(m.lines(w, m.w))
		}
		w.merge(x.page.Messages, x.older)
		if x.older || !w.loaded {
			w.hasBefore = x.page.HasBefore
		}
		if before > 0 {
			w.scroll += len(m.lines(w, m.w)) - before
		}
	}
	if w.replies == nil {
		w.replies = map[string]messages.ReplySummary{}
	}
	for k, v := range x.page.Replies {
		w.replies[k] = v
	}
	w.parent = x.page.Parent
	w.loaded = true
	if j := m.pending; j != nil && j.Conversation == x.conv {
		m.pending = nil
		w.highlight = j.Message
		w.scroll = 0
		if j.Message != "" {
			w.scroll = m.scrollTo(w, j.Message)
		}
	}
	if x.page.HasAfter && !x.older {
		more = m.load(w, false)
	}
	return more
}

func (m *Model) onEvents(x eventsMsg) tea.Cmd {
	if x.err != nil {
		m.eventSeq = -1
		return tea.Tick(eventRetry, func(time.Time) tea.Msg { return listenMsg{} })
	}
	m.eventSeq = x.ev.Seq
	var last *messages.Jump
	for _, e := range x.ev.Events {
		if e.Kind == "jump" && e.Jump != nil {
			last = e.Jump
		}
	}
	if last == nil {
		return m.listen()
	}
	return tea.Batch(m.listen(), m.jump(*last))
}

// jump shows a message, conversation or section.
func (m *Model) jump(j messages.Jump) tea.Cmd {
	if j.Message != "" && j.Conversation == "" {
		return func() tea.Msg {
			c, cancel := ctx()
			defer cancel()
			msg, e := m.api.Message(c, j.Message)
			j.Conversation = msg.Conversation
			return jumpMsg{j, e}
		}
	}
	if j.Conversation == "" {
		m.show(0)
		if j.Section == "drafts" {
			m.status("Drafts and delivery records: amy messages focus, then D.")
		}
		return nil
	}
	it, ok := m.known[j.Conversation]
	c := it.Conversation
	if !ok {
		c = messages.Conversation{ID: j.Conversation, Name: j.Conversation}
	}
	i, w, _ := m.open(c)
	if w == nil {
		return nil
	}
	m.active = i
	w.level = actNone
	copy := j
	m.pending = &copy
	if j.Message != "" {
		return m.loadAround(w, j.Message)
	}
	w.loading = false
	w.loaded = false
	return m.load(w, false)
}

// --- input -------------------------------------------------------------------

func (m *Model) prompt() string { return "[" + m.cur().name() + "] " }

func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	switch s {
	case "ctrl+c":
		return tea.Quit
	case "alt+a":
		return m.nextActive()
	case "ctrl+n", "alt+right":
		return m.show((m.active + 1) % len(m.wins))
	case "ctrl+p", "alt+left":
		return m.show((m.active - 1 + len(m.wins)) % len(m.wins))
	case "pgup":
		w := m.cur()
		w.scroll += max(1, m.bodyHeight()-2)
		if lines := m.lines(w, m.w); w.scroll > len(lines)-m.bodyHeight() {
			w.scroll = max(0, len(lines)-m.bodyHeight())
			if w.hasBefore {
				return m.load(w, true)
			}
		}
		return nil
	case "pgdown":
		w := m.cur()
		w.scroll = max(0, w.scroll-max(1, m.bodyHeight()-2))
		return nil
	case "up":
		if m.histAt > 0 {
			m.histAt--
			m.input.SetValue(m.history[m.histAt])
			m.input.CursorEnd()
		}
		return nil
	case "down":
		if m.histAt < len(m.history) {
			m.histAt++
			v := ""
			if m.histAt < len(m.history) {
				v = m.history[m.histAt]
			}
			m.input.SetValue(v)
			m.input.CursorEnd()
		}
		return nil
	case "tab":
		m.complete()
		return nil
	case "enter":
		line := m.input.Value()
		m.input.SetValue("")
		if strings.TrimSpace(line) == "" {
			return nil
		}
		m.history = append(m.history, line)
		m.histAt = len(m.history)
		return m.submit(line)
	}
	if strings.HasPrefix(s, "alt+") && len(s) == 5 && s[4] >= '0' && s[4] <= '9' {
		n := int(s[4] - '0')
		if n == 0 {
			n = 10
		}
		return m.show(n - 1)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return cmd
}

func (m *Model) nextActive() tea.Cmd {
	best, at := actNone, -1
	for i, w := range m.wins {
		if i != m.active && w.level > best {
			best, at = w.level, i
		}
	}
	if at < 0 {
		return nil
	}
	return m.show(at)
}

var commands = []string{"/close", "/done", "/go", "/help", "/lastlog", "/query", "/quit", "/reply", "/search", "/snooze", "/thread", "/win"}

// complete fills in a command, a window name after /query, or a ref.
func (m *Model) complete() {
	v := m.input.Value()
	fields := strings.Fields(v)
	if len(fields) == 1 && strings.HasPrefix(v, "/") && !strings.HasSuffix(v, " ") {
		for _, c := range commands {
			if strings.HasPrefix(c, fields[0]) {
				m.input.SetValue(c + " ")
				m.input.CursorEnd()
				return
			}
		}
	}
	if len(fields) >= 2 && (fields[0] == "/query" || fields[0] == "/q") {
		want := strings.ToLower(strings.Join(fields[1:], " "))
		for _, it := range m.sortedKnown() {
			if strings.HasPrefix(strings.ToLower(messages.Plain(it.Conversation.Name)), want) {
				m.input.SetValue(fields[0] + " " + messages.Plain(it.Conversation.Name))
				m.input.CursorEnd()
				return
			}
		}
	}
}

func (m *Model) sortedKnown() []messages.Item {
	out := make([]messages.Item, 0, len(m.known))
	for _, it := range m.known {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivity.After(out[j].LastActivity) })
	return out
}

func (m *Model) submit(line string) tea.Cmd {
	w := m.cur()
	if !strings.HasPrefix(line, "/") || strings.HasPrefix(line, "//") {
		line = strings.TrimPrefix(line, "/")
		if w.kind == kindSearch {
			if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
				return m.goResult(w, n)
			}
		}
		if w.kind != kindConv {
			m.status("-!- This window has no conversation. /query NAME opens one; Alt+A goes to activity.")
			return nil
		}
		target, ok := w.replyTarget()
		if !ok {
			w.note(m.o.Now(), "-!- Nothing to reply to yet in this conversation.")
			return nil
		}
		return m.send(w, target, line, false)
	}
	name, rest, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	rest = strings.TrimSpace(rest)
	switch strings.ToLower(name) {
	case "help":
		m.help()
		m.show(0)
	case "quit", "exit":
		return tea.Quit
	case "win", "w", "window":
		n, err := strconv.Atoi(rest)
		if err != nil {
			m.status("-!- /win N")
			return nil
		}
		return m.show(n - 1)
	case "close", "wc", "part":
		if m.active == 0 {
			return nil
		}
		m.wins = append(m.wins[:m.active], m.wins[m.active+1:]...)
		m.active = min(m.active, len(m.wins)-1)
	case "query", "q":
		return m.query(rest)
	case "reply", "r":
		ref, text, _ := strings.Cut(rest, " ")
		msg, ok := w.message(ref)
		if !ok || strings.TrimSpace(text) == "" {
			w.note(m.o.Now(), "-!- /reply REF text — REF is the [abc] before a message")
			return nil
		}
		return m.send(w, msg, text, false)
	case "thread", "t":
		ref, text, _ := strings.Cut(rest, " ")
		msg, ok := w.message(ref)
		if !ok {
			w.note(m.o.Now(), "-!- /thread REF [text] — opens REF's thread, or starts it with text")
			return nil
		}
		if strings.TrimSpace(text) != "" {
			return m.send(w, msg, text, true)
		}
		if r, ok := w.replies[msg.ID]; ok {
			return m.jump(messages.Jump{Conversation: r.Conversation})
		}
		w.note(m.o.Now(), "-!- No thread under [%s] yet: /thread %s text starts one.", ref, ref)
	case "done", "snooze":
		return m.attention(w, strings.ToLower(name))
	case "search", "s":
		return m.search(rest, "")
	case "lastlog", "ll":
		if w.kind != kindConv {
			m.status("-!- /lastlog searches the current conversation; use /search here.")
			return nil
		}
		return m.search(rest, w.conv.ID)
	case "go":
		n, err := strconv.Atoi(rest)
		if err != nil || w.kind != kindSearch {
			m.status("-!- /go N works in a search window")
			return nil
		}
		return m.goResult(w, n)
	default:
		w.note(m.o.Now(), "-!- Unknown command /%s. /help lists them.", name)
	}
	return nil
}

func (m *Model) help() {
	for _, l := range []string{
		"Text replies to the newest message from someone else in this window.",
		"/reply REF text     reply to a message; REF is its [abc] label (/r)",
		"/thread REF [text]  open REF's Slack thread, or reply in it, starting it (/t)",
		"/query NAME         open a conversation (/q; Tab completes)",
		"/search WORDS       search all messages (/s); /lastlog WORDS this one (/ll)",
		"/go N               open search result N (or type N in a search window)",
		"/done  /snooze      mark this conversation handled, or snooze it an hour",
		"/win N  /close      switch to or close a window; /quit exits",
		"Alt+1…9 windows · Alt+A next activity · Ctrl+N/P next/previous · PgUp/PgDn scroll",
	} {
		m.status("%s", l)
	}
}

func (m *Model) query(name string) tea.Cmd {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		m.status("-!- /query NAME")
		return nil
	}
	var pick *messages.Item
	for _, it := range m.sortedKnown() {
		n := strings.ToLower(messages.Plain(it.Conversation.Name))
		if n == want {
			it := it
			pick = &it
			break
		}
		if pick == nil && strings.Contains(n, want) {
			it := it
			pick = &it
		}
	}
	if pick == nil {
		m.status("-!- No conversation matches %q.", name)
		return nil
	}
	i, w, cmd := m.open(pick.Conversation)
	if w == nil {
		return nil
	}
	return tea.Batch(cmd, m.show(i))
}

func (m *Model) send(w *window, target messages.Message, body string, thread bool) tea.Cmd {
	in := messages.DraftInput{RequestID: messages.ID(), ReplyTo: target.ID, Body: body, Thread: thread}
	conv := w.conv.ID
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		d, e := m.api.Draft(c, in)
		if e == nil {
			d, e = m.api.Send(c, d.ID)
		}
		return sentMsg{conv: conv, draft: d, thread: thread, err: e}
	}
}

func (m *Model) attention(w *window, action string) tea.Cmd {
	if w.kind != kindConv {
		return nil
	}
	target, ok := w.replyTarget()
	if !ok {
		return nil
	}
	api := map[string]string{"done": "handled", "snooze": "snooze"}[action]
	text := map[string]string{"done": "Marked handled.", "snooze": "Snoozed for an hour."}[action]
	conv := w.conv.ID
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		return doneMsg{conv: conv, text: text, err: m.api.Attention(c, target.ID, api)}
	}
}

func (m *Model) search(q, conversation string) tea.Cmd {
	if strings.TrimSpace(q) == "" {
		m.status("-!- /search WORDS")
		return nil
	}
	title := "search: " + q
	if conversation != "" {
		title = "lastlog: " + q
	}
	w := &window{kind: kindSearch, title: title, refs: map[string]string{}, byRef: map[string]string{}}
	m.wins = append(m.wins, w)
	m.active = len(m.wins) - 1
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		hits, e := m.api.Search(c, q, conversation, 50)
		return searchMsg{win: w, hits: hits, err: e}
	}
}

func (m *Model) goResult(w *window, n int) tea.Cmd {
	if n < 1 || n > len(w.hits) {
		w.note(m.o.Now(), "-!- No result %d.", n)
		return nil
	}
	h := w.hits[n-1]
	if _, ok := m.known[h.Conversation.ID]; !ok {
		m.known[h.Conversation.ID] = messages.Item{Conversation: h.Conversation}
	}
	return m.jump(messages.Jump{Conversation: h.Conversation.ID, Message: h.Message.ID})
}

// scrollTo is the scroll that puts message id near the top of the view.
func (m *Model) scrollTo(w *window, id string) int {
	lines := m.lines(w, m.w)
	for i, l := range lines {
		if l.msg == id {
			return max(0, len(lines)-m.bodyHeight()-max(0, i-2))
		}
	}
	return 0
}

func (m *Model) bodyHeight() int { return max(1, m.h-3) }
