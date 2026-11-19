package messageui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// drain runs commands the way Bubble Tea would, feeding results back in.
// Commands that wait (the event long-poll, the refresh tick) are left
// unanswered after a short time, as they would still be pending.
func drain(t *testing.T, m *Model, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 8 {
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

func TestJumpFromFindLandsOnAHandledMessageDeepInAConversation(t *testing.T) {
	m, s, c := harness(t)
	ctx := context.Background()
	conv := messages.Conversation{ID: "long-chat", Service: "Telegram", Account: "personal (demo)", Name: "Long chat", Recipient: "long", Demo: true}
	start := time.Now().Add(-3 * time.Hour)
	var target messages.Message
	for i := range 40 {
		msg, e := s.Ingest(messages.Ingress{Conversation: conv, Message: messages.Message{NativeID: fmt.Sprint(i), Sender: "Dana", Body: fmt.Sprintf("line %02d", i), At: start.Add(time.Duration(i) * time.Minute)}})
		if e != nil {
			t.Fatal(e)
		}
		if i == 5 {
			target = msg
		}
		if e = s.Attention(msg.ID, "handled"); e != nil {
			t.Fatal(e)
		}
	}
	// A filter and Focus would both hide the conversation; the jump clears them.
	m.query = "nothing matches this"
	m.all = false
	if _, e := c.Jump(ctx, messages.Jump{Message: target.ID}); e != nil {
		t.Fatal(e)
	}
	drain(t, m, m.listen(), 0)
	if it := m.current(); it == nil || it.Conversation.ID != "long-chat" {
		t.Fatalf("cursor not on the jumped conversation: %+v", it)
	}
	if m.highlight != target.ID || m.query != "" || !m.all {
		t.Fatalf("highlight=%q query=%q all=%v", m.highlight, m.query, m.all)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "▶ Dana") || !strings.Contains(view, "line 05") {
		t.Fatalf("jumped-to message not shown:\n%s", view)
	}
	if strings.Contains(view, "line 39") {
		t.Fatal("view stayed at the newest messages")
	}
	if m.eventSeq < 1 {
		t.Fatalf("event position not kept: %d", m.eventSeq)
	}
	// Moving on clears the mark.
	m.move(1)
	if m.highlight != "" {
		t.Fatal("highlight survived moving to another conversation")
	}
}

func TestGotoOptionOpensAConversationOrDrafts(t *testing.T) {
	m, _, c := harness(t)
	m.opts.Goto = &messages.Jump{Conversation: "demo-slack-release"}
	drain(t, m, m.startJump(*m.opts.Goto), 0)
	if it := m.current(); it == nil || it.Conversation.ID != "demo-slack-release" {
		t.Fatalf("goto conversation: %+v", it)
	}
	if m.highlight != "" {
		t.Fatal("a conversation jump should not mark a message")
	}
	drain(t, m, m.startJump(messages.Jump{Section: "drafts"}), 0)
	if m.mode != "drafts" || m.busy {
		t.Fatalf("drafts jump: mode=%s busy=%v", m.mode, m.busy)
	}
	drain(t, m, m.startJump(messages.Jump{Conversation: "missing"}), 0)
	if !strings.Contains(m.status, "not in this list") {
		t.Fatalf("missing conversation status: %q", m.status)
	}
	_ = c
}

func TestEventErrorsRetryFromTheStart(t *testing.T) {
	m, _, _ := harness(t)
	m.eventSeq = 12
	cmd := m.onEvents(eventsMsg{err: fmt.Errorf("gateway restarted")})
	if cmd == nil || m.eventSeq != -1 {
		t.Fatalf("retry not scheduled from a fresh position: %d", m.eventSeq)
	}
}
