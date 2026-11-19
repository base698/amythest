package chatui

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// drain runs commands as Bubble Tea would and feeds results back. Commands
// that wait (the event long-poll, the refresh tick) are left pending.
func drain(t *testing.T, m *Model, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 12 {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(300 * time.Millisecond):
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drain(t, m, c, depth+1)
		}
		return
	}
	if msg == nil {
		return
	}
	_, next := m.Update(msg)
	drain(t, m, next, depth+1)
}

func harness(t *testing.T) (*Model, *messages.Store, *messages.Client) {
	t.Helper()
	s, e := messages.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.Seed(); e != nil {
		t.Fatal(e)
	}
	eventWait = time.Second
	token := strings.Repeat("x", 64)
	srv := httptest.NewServer((&messages.Gateway{Store: s, OwnerToken: token}).Handler())
	t.Cleanup(srv.Close)
	c := messages.NewClient(srv.URL, token)
	m := New(c, Options{Listener: messages.Listener{Machine: "test", Pane: "t1"}})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	drain(t, m, m.poll(), 0)
	return m, s, c
}

func typeLine(t *testing.T, m *Model, text string) {
	t.Helper()
	m.input.SetValue(text)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(t, m, cmd, 0)
}

func alt(n rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{n}, Alt: true} }

func screen(m *Model) string { return ansi.Strip(m.View()) }

// windowNamed opens (or finds) the window for a known conversation.
func (m *Model) windowNamed(t *testing.T, conv string) (int, *window) {
	t.Helper()
	if i, w := m.window(conv); w != nil {
		return i, w
	}
	it, ok := m.known[conv]
	if !ok {
		t.Fatalf("unknown conversation %s", conv)
	}
	i, w, cmd := m.open(it.Conversation)
	drain(t, m, cmd, 0)
	return i, w
}

func TestHomeListsTheQueueAndOpensTheSelection(t *testing.T) {
	m, _, _ := harness(t)
	if len(m.wins) != 1 || len(m.focus) < 3 {
		t.Fatalf("home should start alone with the queue: %d windows, %d waiting", len(m.wins), len(m.focus))
	}
	view := screen(m)
	for _, want := range []string{"waiting on you", "[home]", "Robot build", "Enter opens", "[1:amy]"} {
		if !strings.Contains(view, want) {
			t.Errorf("home lacks %q\n%s", want, view)
		}
	}
	first := m.focus[0].Conversation.ID
	if p := m.previews[first]; p == nil || len(p.msgs) == 0 {
		t.Fatal("the selection has no preview")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	drain(t, m, cmd, 0)
	second := m.focus[1].Conversation.ID
	if m.homeSel != 1 || m.previews[second] == nil {
		t.Fatalf("down moved to %d", m.homeSel)
	}
	// Typing filters; Enter opens the match in a window of its own.
	for _, r := range "robot" {
		_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		drain(t, m, cmd, 0)
	}
	if items := m.homeItems(); len(items) != 1 || items[0].Conversation.ID != "demo-telegram-robot" {
		t.Fatalf("filter: %+v", items)
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(t, m, cmd, 0)
	if m.active != 1 || m.cur().conv.ID != "demo-telegram-robot" || !m.cur().loaded {
		t.Fatalf("enter opened %d %q", m.active, m.cur().conv.ID)
	}
	if v := screen(m); !strings.Contains(v, "[2:") || !strings.Contains(v, "message replies to") {
		t.Fatalf("window 2:\n%s", v)
	}
}

func TestBrowserListsChatsByServiceAndOpensOne(t *testing.T) {
	m, _, _ := harness(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	drain(t, m, cmd, 0)
	view := screen(m)
	for _, want := range []string{"All chats", "Slack (demo)", "#release", "Telegram (demo)", "[find]"} {
		if !strings.Contains(view, want) {
			t.Errorf("browser lacks %q\n%s", want, view)
		}
	}
	for _, r := range "release" {
		_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		drain(t, m, cmd, 0)
	}
	rows := m.rows()
	if len(rows) != 2 || !rows[0].heading || rows[1].l.Conversation.ID != "demo-slack-release" || m.br.sel != 1 {
		t.Fatalf("filtered rows: %+v sel=%d", rows, m.br.sel)
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(t, m, cmd, 0)
	if m.br.open || m.cur().conv.ID != "demo-slack-release" || m.input.Value() != "" {
		t.Fatalf("enter: open=%v conv=%q input=%q", m.br.open, m.cur().conv.ID, m.input.Value())
	}
	// Esc returns to where you were, with what you had typed.
	m.input.SetValue("half a thought")
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	drain(t, m, cmd, 0)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.br.open || m.input.Value() != "half a thought" {
		t.Fatalf("esc: open=%v input=%q", m.br.open, m.input.Value())
	}
	// Folding a service hides its chats.
	m.input.SetValue("")
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	drain(t, m, cmd, 0)
	before := len(m.rows())
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if len(m.rows()) >= before {
		t.Fatal("left did not fold the service")
	}
	// /query for a chat not seen yet searches the browser.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	typeLine(t, m, "/query nobody-like-this")
	if !m.br.open || m.input.Value() != "nobody-like-this" || !strings.Contains(screen(m), "No conversation matches") {
		t.Fatalf("query fallback:\n%s", screen(m))
	}
}

func TestPlainTextAndReplyRefsAnswerTheRightMessage(t *testing.T) {
	m, s, _ := harness(t)
	i, w := m.windowNamed(t, "demo-telegram-robot")
	drain(t, m, m.show(i), 0)
	target, _ := w.replyTarget()
	typeLine(t, m, "Here is the change")
	if v := screen(m); !strings.Contains(v, "Here is the change") || !strings.Contains(v, "simulated") {
		t.Fatalf("reply not shown:\n%s", v)
	}
	drafts, _ := s.Drafts("owner")
	if len(drafts) != 1 || drafts[0].ReplyTo != target.ID || drafts[0].Target.ID != "demo-telegram-robot" {
		t.Fatalf("plain text answered %+v", drafts)
	}
	ref := w.refFor(target.ID)
	typeLine(t, m, "/reply "+ref+" and a screenshot")
	drafts, _ = s.Drafts("owner")
	if len(drafts) != 2 {
		t.Fatalf("drafts: %d", len(drafts))
	}
	for _, d := range drafts {
		if d.Body == "and a screenshot" && d.ReplyTo != target.ID {
			t.Fatalf("/reply answered %s", d.ReplyTo)
		}
	}
	typeLine(t, m, "/reply zzzz nope")
	if !strings.Contains(screen(m), "/reply REF text") {
		t.Fatal("bad ref not explained")
	}
}

func TestThreadCommandStartsAndOpensASlackThread(t *testing.T) {
	m, s, _ := harness(t)
	i, w := m.windowNamed(t, "demo-slack-release")
	drain(t, m, m.show(i), 0)
	root, _ := w.replyTarget()
	ref := w.refFor(root.ID)
	typeLine(t, m, "/thread "+ref)
	if !strings.Contains(screen(m), "No thread under") {
		t.Fatalf("missing-thread hint:\n%s", screen(m))
	}
	typeLine(t, m, "/thread "+ref+" On it")
	thread := "demo-slack-release/thread/" + root.NativeID
	if th, e := s.Thread("owner", thread); e != nil || len(th.Messages) != 1 {
		t.Fatalf("thread reply: %+v %v", th, e)
	}
	if v := screen(m); !strings.Contains(v, "[1 reply · /thread "+ref+"]") {
		t.Fatalf("reply count not shown:\n%s", v)
	}
	typeLine(t, m, "/thread "+ref)
	if m.cur().conv.ID != thread || !strings.Contains(screen(m), "On it") || !strings.Contains(screen(m), "Slack thread") {
		t.Fatalf("thread window:\n%s", screen(m))
	}
	// Text in the thread window stays in the thread.
	typeLine(t, m, "Done")
	if th, _ := s.Thread("owner", thread); len(th.Messages) != 2 {
		t.Fatalf("thread window reply left the thread: %d", len(th.Messages))
	}
}

func TestSearchWindowOpensTheHitHighlighted(t *testing.T) {
	m, _, _ := harness(t)
	typeLine(t, m, "/search deployment fix")
	w := m.cur()
	if w.kind != kindSearch || len(w.hits) == 0 {
		t.Fatalf("search window: %+v", w)
	}
	if v := screen(m); !strings.Contains(v, "1. #release") {
		t.Fatalf("results:\n%s", v)
	}
	want := w.hits[0].Message.ID
	typeLine(t, m, "1")
	cw := m.cur()
	if cw.kind != kindConv || cw.conv.ID != "demo-slack-release" || cw.highlight != want {
		t.Fatalf("result did not open: %s highlight=%q", cw.conv.ID, cw.highlight)
	}
	if !strings.Contains(screen(m), "▶") {
		t.Fatalf("no highlight marker:\n%s", screen(m))
	}
	typeLine(t, m, "/lastlog deployment")
	if lw := m.cur(); lw.kind != kindSearch || len(lw.hits) != 1 {
		t.Fatalf("lastlog: %+v", lw.hits)
	}
}

func TestNewMessagesRaiseActivityAndOpenWindows(t *testing.T) {
	m, s, _ := harness(t)
	conv := messages.Conversation{ID: "new-chat", Service: "Telegram", Account: "personal (demo)", Name: "Kim", Recipient: "kim", Demo: true}
	if _, e := s.Ingest(messages.Ingress{Conversation: conv, Message: messages.Message{NativeID: "1", Sender: "Kim", Body: "deadline moved to Monday"}}); e != nil {
		t.Fatal(e)
	}
	// Elsewhere when it arrives: home (window 1) lights up and lists it.
	j, _ := m.windowNamed(t, "demo-slack-release")
	drain(t, m, m.show(j), 0)
	drain(t, m, m.poll(), 0)
	if _, w := m.window("new-chat"); w != nil {
		t.Fatal("a new message opened a window by itself")
	}
	if m.wins[0].level != actHilight || !strings.Contains(screen(m), "1!") {
		t.Fatalf("home not hilighted (level %d):\n%s", m.wins[0].level, screen(m))
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}, Alt: true})
	drain(t, m, cmd, 0)
	if m.active != 0 || !strings.Contains(screen(m), "Kim") {
		t.Fatalf("alt+a went to %d:\n%s", m.active, screen(m))
	}
	// Activity in a window that is open but not shown marks that window.
	i, w := m.windowNamed(t, "new-chat")
	if _, e := s.Ingest(messages.Ingress{Conversation: conv, Message: messages.Message{NativeID: "1b", Sender: "Kim", Body: "urgent: call me"}}); e != nil {
		t.Fatal(e)
	}
	drain(t, m, m.poll(), 0)
	if w.level == actNone || !strings.Contains(screen(m), fmt.Sprint(i+1)) {
		t.Fatalf("open window not marked (level %d)", w.level)
	}
	drain(t, m, m.show(i), 0)
	// While the window is open, a new message appears in it.
	if _, e := s.Ingest(messages.Ingress{Conversation: conv, Message: messages.Message{NativeID: "2", Sender: "Kim", Body: "and the room changed"}}); e != nil {
		t.Fatal(e)
	}
	drain(t, m, m.poll(), 0)
	if !strings.Contains(screen(m), "and the room changed") || w.level != actNone {
		t.Fatalf("active window did not update:\n%s", screen(m))
	}
}

func TestJumpEventOpensTheMessageAndOlderPagesLoad(t *testing.T) {
	m, s, c := harness(t)
	conv := messages.Conversation{ID: "long", Service: "Telegram", Account: "personal (demo)", Name: "Long", Recipient: "long", Demo: true}
	start := time.Now().Add(-10 * time.Hour)
	var early messages.Message
	for i := range 260 {
		msg, e := s.Ingest(messages.Ingress{Conversation: conv, Message: messages.Message{NativeID: fmt.Sprint(i), Sender: "Dana", Body: fmt.Sprintf("line %03d", i), At: start.Add(time.Duration(i) * time.Minute)}})
		if e != nil {
			t.Fatal(e)
		}
		if i == 30 {
			early = msg
		}
	}
	drain(t, m, m.poll(), 0)
	if _, e := c.Jump(context.Background(), messages.Jump{Message: early.ID}); e != nil {
		t.Fatal(e)
	}
	drain(t, m, m.listen(), 0)
	w := m.cur()
	if w.conv.ID != "long" || w.highlight != early.ID || !strings.Contains(screen(m), "line 030") {
		t.Fatalf("jump: %s highlight=%q\n%s", w.conv.ID, w.highlight, screen(m))
	}
	// The page around the hit, then everything newer, is loaded.
	if len(w.msgs) != 260 || w.hasBefore {
		t.Fatalf("loaded %d messages, hasBefore=%v", len(w.msgs), w.hasBefore)
	}
	// A fresh window loads the newest page; PgUp at the top loads older ones.
	typeLine(t, m, "/close")
	typeLine(t, m, "/query long")
	w = m.cur()
	if len(w.msgs) != pageSize || !w.hasBefore {
		t.Fatalf("first page: %d hasBefore=%v", len(w.msgs), w.hasBefore)
	}
	for i := 0; i < 400 && w.hasBefore; i++ {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
		drain(t, m, cmd, 0)
	}
	if len(w.msgs) != 260 || w.msgs[0].Body != "line 000" {
		t.Fatalf("older pages: %d first=%q", len(w.msgs), w.msgs[0].Body)
	}
}

func TestCommandsWindowsAndHelp(t *testing.T) {
	m, _, _ := harness(t)
	m.windowNamed(t, "demo-slack-release")
	m.windowNamed(t, "demo-telegram-robot")
	n := len(m.wins)
	typeLine(t, m, "/win 3")
	if m.active != 2 {
		t.Fatalf("/win 3 -> %d", m.active)
	}
	typeLine(t, m, "/close")
	if len(m.wins) != n-1 {
		t.Fatal("/close kept the window")
	}
	m.input.SetValue("/se")
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.input.Value() != "/search " {
		t.Fatalf("tab completed %q", m.input.Value())
	}
	m.input.SetValue("/q rob")
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.input.Value() != "/q Robot build" {
		t.Fatalf("tab completed %q", m.input.Value())
	}
	typeLine(t, m, "/help")
	if m.cur().title != "help" || !strings.Contains(screen(m), "/thread REF") || !strings.Contains(screen(m), "Ctrl+F") {
		t.Fatalf("help:\n%s", screen(m))
	}
	typeLine(t, m, "/bogus")
	if !strings.Contains(screen(m), "Unknown command /bogus") {
		t.Fatal("unknown command not reported")
	}
}

func TestDoneMarksTheConversationHandled(t *testing.T) {
	m, s, _ := harness(t)
	i, w := m.windowNamed(t, "demo-gmail-review")
	drain(t, m, m.show(i), 0)
	target, _ := w.replyTarget()
	typeLine(t, m, "/done")
	if msg, _ := s.Message("owner", target.ID); !msg.Handled {
		t.Fatal("/done did not mark handled")
	}
	if !strings.Contains(screen(m), "Marked handled.") {
		t.Fatalf("no confirmation:\n%s", screen(m))
	}
}
