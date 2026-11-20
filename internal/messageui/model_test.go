package messageui

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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
	token := strings.Repeat("x", 64)
	srv := httptest.NewServer((&messages.Gateway{Store: s, OwnerToken: token}).Handler())
	t.Cleanup(srv.Close)
	c := messages.NewClient(srv.URL, token)
	m := New(c, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	_, cmd := m.Update(m.load()())
	if cmd != nil {
		m.Update(cmd())
	}
	return m, s, c
}
func TestComposerKeepsOriginalMessageDuringIncomingRefresh(t *testing.T) {
	m, s, _ := harness(t)
	target := *m.current()
	m.startCompose()
	m.input.SetValue("Here is the captured patch")
	a, e := s.AddAttachment("selected.patch", []byte("selected content"))
	if e != nil {
		t.Fatal(e)
	}
	m.selected = []messages.Attachment{a}
	// A new request sorts ahead of the selected conversation while composing.
	_, e = s.Ingest(messages.Ingress{Conversation: messages.Conversation{ID: "new", Service: "Telegram", Account: "other", Name: "New conversation", Recipient: "Someone else", Demo: true}, Message: messages.Message{NativeID: "n", Sender: "Other", Body: "Please reply to me?", At: time.Now().Add(time.Minute)}})
	if e != nil {
		t.Fatal(e)
	}
	m.Update(m.load()())
	msg := m.send(false)().(sentMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.draft.ReplyTo != target.Message.ID || msg.draft.Target.ID != target.Conversation.ID || len(msg.draft.Attachments) != 1 || msg.draft.Attachments[0].ID != a.ID {
		t.Fatalf("reply target or attachment changed: %+v", msg.draft)
	}
	th, e := s.Thread("owner", "new")
	if e != nil || len(th.Messages) != 1 {
		t.Fatal("reply went to newly arrived message")
	}
}
func TestFocusRemainsUsableWithoutIRCAndAgentDraftCanBeReviewed(t *testing.T) {
	m, s, c := harness(t)
	it := *m.current()
	if e := c.Focus(context.Background(), it.Message.ID, ""); e == nil {
		t.Fatal("focus claimed an IRC client exists")
	}
	before, e := s.Thread("owner", it.Conversation.ID)
	if e != nil {
		t.Fatal(e)
	}
	m.startCompose()
	m.input.SetValue("A saved draft")
	result := m.send(true)().(sentMsg)
	m.Update(result)
	if result.err != nil || result.draft.State != "draft" {
		t.Fatal("could not save draft without IRC")
	}
	th, e := s.Thread("owner", it.Conversation.ID)
	if e != nil || len(th.Messages) != len(before.Messages) {
		t.Fatal("saving a draft sent a reply")
	}
}
func TestViewsFitTerminalAndShowPinnedDestination(t *testing.T) {
	m, _, _ := harness(t)
	for _, size := range [][2]int{{120, 32}, {70, 24}, {40, 12}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, mode := range []string{"focus", "compose"} {
			if mode == "compose" {
				m.startCompose()
				m.input.SetValue("A reply\nwith another line")
			}
			m.mode = mode
			view := m.View()
			lines := strings.Split(view, "\n")
			if len(lines) > size[1] {
				t.Fatalf("%s exceeds %dx%d: %d rows", mode, size[0], size[1], len(lines))
			}
			for _, line := range lines {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("%s row exceeds width %d: %q", mode, size[0], line)
				}
			}
			if mode == "compose" && !strings.Contains(view, "To:") {
				t.Fatal("composer hid the reply destination")
			}
		}
	}
}

func TestLiveComposerAndReceiptRefreshAreExplicit(t *testing.T) {
	m, s, _ := harness(t)
	c := messages.Connector{Account: "123", Worker: "test", Chats: []string{"-100456"}, Send: true}
	if e := s.Heartbeat(c); e != nil {
		t.Fatal(e)
	}
	r := messages.Route{Provider: "telegram", Account: "123", Chat: "-100456"}
	in := messages.Ingress{Conversation: messages.Conversation{ID: messages.TelegramConversationID(r), Service: "Telegram", Name: "Real group", Account: r.Account, Recipient: r.Chat, Route: r}, Message: messages.Message{NativeID: "12", Sender: "Colleague", Body: "Please reply?"}}
	original, e := s.Ingest(in)
	if e != nil {
		t.Fatal(e)
	}
	m.Update(m.load()())
	for i, it := range m.items {
		if it.Message.ID == original.ID {
			m.cursor = i
		}
	}
	m.startCompose()
	m.input.SetValue("A real reply")
	view := m.View()
	if !strings.Contains(view, "Live reply") || !strings.Contains(view, "send to Telegram") || !strings.Contains(view, "Real group") {
		t.Fatal("live composer hid delivery mode or destination")
	}
	result := m.send(false)().(sentMsg)
	m.Update(result)
	if result.err != nil || result.draft.State != "queued" || !strings.Contains(m.status, "queued") {
		t.Fatal("queued reply claimed delivery", result.err, m.status)
	}
	m.Update(m.loadDrafts(false)())
	m.review = &result.draft
	m.mode = "review"
	claimed, e := s.Claim(c.Account, c.Worker)
	if e != nil || claimed == nil {
		t.Fatal(e)
	}
	if _, e = s.Complete(messages.Receipt{Account: c.Account, Draft: result.draft.ID, Attempt: claimed.Draft.Attempt, State: "sent", NativeID: "13"}); e != nil {
		t.Fatal(e)
	}
	m.Update(m.loadDrafts(true)())
	if m.mode != "review" || m.review.State != "sent" || !strings.Contains(m.View(), "Telegram message: 13") {
		t.Fatal("receipt refresh lost the selected review")
	}
}

func TestContactViewAndServiceFilter(t *testing.T) {
	m := New(nil, Options{})
	m.w = 100
	m.h = 30
	m.all = true
	contact := messages.Conversation{ID: "signal/contact", Service: "Signal", Name: "Quiet contact", Account: "test", Recipient: "native"}
	m.items = []messages.Item{{Conversation: contact, Message: messages.Message{Category: "Contact", Reason: "No messages imported yet."}}}
	m.thread = messages.Thread{Conversation: contact}
	if !strings.Contains(m.View(), "Quiet contact") || !strings.Contains(m.View(), "New incoming messages") {
		t.Fatal("empty contact state hidden")
	}
	m.startCompose()
	if m.mode == "compose" || m.target != nil || !strings.Contains(m.status, "No imported message") {
		t.Fatal("contact invented a reply parent")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m.provider != "Signal" || !m.all {
		t.Fatal("service filter did not select Signal contacts")
	}
}

func TestMailComposerUsesLatestIncomingParent(t *testing.T) {
	for _, provider := range []string{"gmail", "outlook"} {
		t.Run(provider, func(t *testing.T) {
			m := New(nil, Options{})
			conversation := messages.Conversation{ID: "mail-thread", Route: messages.Route{Provider: provider}}
			m.items = []messages.Item{{Conversation: conversation, Message: messages.Message{ID: "sent", Outgoing: true}}}
			m.thread = messages.Thread{Conversation: conversation, Messages: []messages.Message{
				{ID: "older", At: time.Unix(1, 0)},
				{ID: "sent", Outgoing: true, At: time.Unix(3, 0)},
				{ID: "incoming", At: time.Unix(2, 0)},
			}}
			m.startCompose()
			if m.target == nil || m.target.Message.ID != "incoming" {
				t.Fatal("mail reply did not pin the most recent incoming message")
			}
			if m.items[0].Message.ID != "sent" {
				t.Fatal("composition mutated the conversation list")
			}
		})
	}
}

func TestMailComposerRejectsMissingOrStaleIncomingParent(t *testing.T) {
	m := New(nil, Options{})
	m.items = []messages.Item{{Conversation: messages.Conversation{ID: "selected", Route: messages.Route{Provider: "gmail"}}, Message: messages.Message{ID: "sent", Outgoing: true}}}
	for _, thread := range []messages.Thread{{}, {Conversation: messages.Conversation{ID: "previous"}, Messages: []messages.Message{{ID: "unrelated"}}}} {
		m.thread = thread
		m.startCompose()
		if m.target != nil || m.mode == "compose" {
			t.Fatal("mail composer accepted an unrelated or absent incoming parent")
		}
	}
}

func TestWKeyTogglesTheWorkspaceWall(t *testing.T) {
	m, s, _ := harness(t)
	it := *m.current()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if !strings.Contains(m.status, "workspace") {
		t.Fatalf("no workspace not explained: %q", m.status)
	}
	m.Update(workspaceMsg{"robot"})
	run := func(cmd tea.Cmd) {
		for cmd != nil {
			msg := cmd()
			_, cmd = m.Update(msg)
		}
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	run(cmd)
	if tagged, _ := s.Tagged("owner", "robot", 0); len(tagged) != 1 || tagged[0].Message.ID != it.Message.ID {
		t.Fatalf("tagged: %+v (%s)", tagged, m.status)
	}
	if !strings.Contains(strings.Join(func() []string { l, _ := m.threadLines(80, ""); return l }(), "\n"), "on the robot wall") {
		t.Fatal("tag not shown in the conversation")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	run(cmd)
	if tagged, _ := s.Tagged("owner", "robot", 0); len(tagged) != 0 || !strings.Contains(m.status, "off the robot wall") {
		t.Fatalf("second w did not untag: %q", m.status)
	}
}
