package messages

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func incoming(t *testing.T, s *Store, id, account string, demo bool) Message {
	t.Helper()
	m, e := s.Ingest(Ingress{Conversation: Conversation{ID: id, Service: "Telegram", Account: account, Name: "Same display name", Recipient: "recipient-" + id, Thread: "thread-" + id, Demo: demo}, Message: Message{NativeID: "native-1", Sender: "Colleague", Body: "Can you send the patch?"}})
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func status(t *testing.T, e error, want int) {
	t.Helper()
	var f *Fault
	if !errors.As(e, &f) || f.Status != want {
		t.Fatalf("expected status %d, got %v", want, e)
	}
}
func TestReplySurvivesRestartPinsDestinationAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	first := incoming(t, s, "account-one/chat", "personal", true)
	incoming(t, s, "account-two/chat", "work", true)
	a, e := s.AddAttachment("change.patch", []byte("exact captured diff"))
	if e != nil {
		t.Fatal(e)
	}
	in := DraftInput{RequestID: "request-1", ReplyTo: first.ID, Body: "Here is the change", Attachments: []string{a.ID}}
	d, e := s.Draft("owner", in)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Draft("owner", in)
	if e != nil || again.ID != d.ID {
		t.Fatalf("duplicate draft: %+v %v", again, e)
	}
	changed := in
	changed.Body = "different"
	_, e = s.Draft("owner", changed)
	status(t, e, 409)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, e := s.Send("owner", d.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.Target.Account != "personal" || got.Target.Recipient != "recipient-account-one/chat" || got.Target.Thread != "thread-account-one/chat" || got.ReplyTo != first.ID || got.State != "simulated" {
		t.Fatalf("reply routing changed: %+v", got)
	}
	if _, e = s.Send("owner", d.ID); e != nil {
		t.Fatal(e)
	}
	thread, e := s.Thread("owner", first.Conversation)
	if e != nil || len(thread.Messages) != 2 {
		t.Fatalf("retry duplicated delivery: %+v %v", thread, e)
	}
	other, e := s.Thread("owner", "account-two/chat")
	if e != nil || len(other.Messages) != 1 {
		t.Fatalf("cross-account reply: %+v %v", other, e)
	}
	_, data, e := s.Attachment(got.Attachments[0].ID)
	if e != nil || !bytes.Equal(data, []byte("exact captured diff")) {
		t.Fatalf("attachment changed: %q %v", data, e)
	}
}
func TestAgentGrantCheckedAtDraftAndSendAndRevocation(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	m := incoming(t, s, "chat", "personal", true)
	other := incoming(t, s, "other", "personal", true)
	in := DraftInput{RequestID: "agent-draft", ReplyTo: m.ID, Body: "A proposed reply"}
	_, e := s.Draft("agent", in)
	status(t, e, 403)
	g := Grant{Conversation: m.Conversation, Expires: now.Add(time.Hour)}
	if e = s.Grant(g); e != nil {
		t.Fatal(e)
	}
	d, e := s.Draft("agent", in)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Send("agent", d.ID)
	status(t, e, 403)
	items, e := s.Items("agent", true)
	if e != nil || len(items) != 1 || items[0].Conversation.ID != m.Conversation {
		t.Fatalf("grant leaked another conversation: %+v %v", items, e)
	}
	_, e = s.Thread("agent", other.Conversation)
	status(t, e, 403)
	a, e := s.AddAttachment("notes.txt", []byte("notes"))
	if e != nil {
		t.Fatal(e)
	}
	in.RequestID = "agent-files"
	in.Attachments = []string{a.ID}
	_, e = s.Draft("agent", in)
	status(t, e, 403)
	g.Send = true
	g.Attachments = true
	if e = s.Grant(g); e != nil {
		t.Fatal(e)
	}
	d, e = s.Draft("agent", in)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Hour)
	_, e = s.Send("agent", d.ID)
	status(t, e, 403)
	g.Expires = now.Add(time.Hour)
	if e = s.Grant(g); e != nil {
		t.Fatal(e)
	}
	if e = s.Revoke(m.Conversation); e != nil {
		t.Fatal(e)
	}
	_, e = s.Send("agent", d.ID)
	status(t, e, 403)
	// The owner can explicitly review and send a draft after revoking the agent.
	if _, e = s.Send("owner", d.ID); e != nil {
		t.Fatal(e)
	}
}
func TestReplayKeepsAttentionAndLiveSendFailsClosed(t *testing.T) {
	s := testStore(t)
	m := incoming(t, s, "live", "work", false)
	if e := s.Attention(m.ID, "handled"); e != nil {
		t.Fatal(e)
	}
	replay := incoming(t, s, "live", "work", false)
	if replay.ID != m.ID || !replay.Handled {
		t.Fatal("replay reopened or duplicated a message")
	}
	items, e := s.Items("owner", false)
	if e != nil || len(items) != 0 {
		t.Fatalf("unexpected attention items: %+v %v", items, e)
	}
	if e = s.Attention(m.ID, "reopen"); e != nil {
		t.Fatal(e)
	}
	d, e := s.Draft("owner", DraftInput{RequestID: "live-reply", ReplyTo: m.ID, Body: "Pending live connector"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Send("owner", d.ID)
	status(t, e, 409)
	th, e := s.Thread("owner", m.Conversation)
	if e != nil || len(th.Messages) != 1 || th.Messages[0].Handled {
		t.Fatal("failed delivery changed source attention state")
	}
	ds, e := s.Drafts("owner")
	if e != nil || len(ds) != 1 || ds[0].State != "draft" {
		t.Fatalf("failed send lost its draft: %+v %v", ds, e)
	}
}
func TestIngressCannotChangeAnExistingDestination(t *testing.T) {
	s := testStore(t)
	m := incoming(t, s, "chat", "personal", true)
	th, e := s.Thread("owner", m.Conversation)
	if e != nil {
		t.Fatal(e)
	}
	c := th.Conversation
	c.Account = "other account"
	_, e = s.Ingest(Ingress{Conversation: c, Message: Message{NativeID: "native-2", Sender: "Colleague", Body: "New message"}})
	status(t, e, 409)
}
func TestPlainStripsTerminalControlPayloads(t *testing.T) {
	text := "hello\x1b]52;c;secret\x07 world\x1b[31m!\x1b[0m\x1bPpayload\x1b\\\nnext"
	if got := Plain(text); got != "hello world!\nnext" {
		t.Fatalf("unsafe display text %q", got)
	}
}

func TestProviderTimesOrderMessagesAndReplyFocus(t *testing.T) {
	s := testStore(t)
	c := Conversation{ID: "ordered", Service: "Telegram", Account: "personal", Recipient: "group", Demo: true}
	base := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	// Provider backfills can arrive late, and timestamps need not have the same
	// fractional precision. The last arrival is not necessarily the newest.
	times := []time.Time{base.Add(500 * time.Millisecond), base, base.Add(100 * time.Millisecond), base.Add(500 * time.Millisecond)}
	var ingested []Message
	for i, at := range times {
		m, e := s.Ingest(Ingress{Conversation: c, Message: Message{NativeID: ID(), Sender: "Colleague", Body: "Please review", At: at}})
		if e != nil {
			t.Fatalf("message %d: %v", i, e)
		}
		ingested = append(ingested, m)
	}
	items, e := s.Items("owner", false)
	if e != nil || len(items) != 1 || items[0].Message.ID != ingested[3].ID {
		t.Fatalf("focus selected an older message: %+v %v", items, e)
	}
	thread, e := s.Thread("owner", c.ID)
	if e != nil || len(thread.Messages) != 4 {
		t.Fatalf("thread: %+v %v", thread, e)
	}
	for i, want := range []int{1, 2, 0, 3} {
		if thread.Messages[i].ID != ingested[want].ID {
			t.Fatalf("thread message %d is out of order", i)
		}
	}
}
