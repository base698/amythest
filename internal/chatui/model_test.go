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

func (m *Model) windowNamed(t *testing.T, conv string) (int, *window) {
	t.Helper()
	i, w := m.window(conv)
	if w == nil {
		t.Fatalf("no window for %s; have %d windows", conv, len(m.wins))
	}
	return i, w
}

func TestOpensAWindowForEachConversationWaitingOnYou(t *testing.T) {
	m, _, _ := harness(t)
	if len(m.wins) < 3 {
		t.Fatalf("expected windows for the seeded queue, got %d", len(m.wins))
	}
	view := screen(m)
	for _, want := range []string{"[1:amy]", "[Act:", "Waiting on you:", "Robot build", "[amy]"} {
		if !strings.Contains(view, want) {
			t.Errorf("status view lacks %q\n%s", want, view)
		}
	}
	// Alt+2 shows window 2 and clears its activity.
	_, cmd := m.Update(alt('2'))
	drain(t, m, cmd, 0)
	if m.active != 1 || m.cur().level != actNone || !m.cur().loaded {
		t.Fatalf("alt+2: active=%d level=%d loaded=%v", m.active, m.cur().level, m.cur().loaded)
	}
	if v := screen(m); !strings.Contains(v, "[2:") || !strings.Contains(v, "<") {
		t.Fatalf("window 2 shows no messages:\n%s", v)
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
	drain(t, m, m.poll(), 0)
	i, w := m.windowNamed(t, "new-chat")
	if w.level != actHilight {
		t.Fatalf("a Don't-miss message should hilight, level=%d", w.level)
	}
	if v := screen(m); !strings.Contains(v, fmt.Sprintf("%d!", i+1)) {
		t.Fatalf("Act bar lacks the hilight:\n%s", v)
	}
	for _, other := range m.wins {
		if other != w {
			other.level = actNone
		}
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}, Alt: true})
	drain(t, m, cmd, 0)
	if m.active != i || w.level != actNone {
		t.Fatalf("alt+a went to %d (want %d)", m.active, i)
	}
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
	if m.active != 0 || !strings.Contains(screen(m), "/thread REF") {
		t.Fatalf("help:\n%s", screen(m))
	}
	typeLine(t, m, "hello")
	if !strings.Contains(screen(m), "This window has no conversation") {
		t.Fatal("text in the status window not explained")
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
