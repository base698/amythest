package messages

import (
	"testing"
	"time"
)

func seeded(t *testing.T, s *Store, conversation string) Message {
	t.Helper()
	if e := s.Seed(); e != nil {
		t.Fatal(e)
	}
	th, e := s.Thread("owner", conversation)
	if e != nil || len(th.Messages) == 0 {
		t.Fatalf("seeded %s: %v", conversation, e)
	}
	return th.Messages[len(th.Messages)-1]
}

func TestDemoSlackThreadStartsOnceAndIsLinkedBothWays(t *testing.T) {
	s := testStore(t)
	root := seeded(t, s, "demo-slack-release")
	send := func(body string, reply string) Draft {
		t.Helper()
		d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: reply, Body: body, Thread: true})
		if e != nil {
			t.Fatal(e)
		}
		if d, e = s.Send("owner", d.ID); e != nil || d.State != "simulated" {
			t.Fatalf("send: %+v %v", d, e)
		}
		return d
	}
	first := send("On it", root.ID)
	thread := "demo-slack-release/thread/" + root.NativeID
	if first.Target.ID != thread || first.Target.Thread != root.NativeID {
		t.Fatalf("first reply went to %+v", first.Target)
	}
	// Replying in the thread again, from either side, reuses it.
	send("Merged", root.ID)
	inThread, e := s.Thread("owner", thread)
	if e != nil || len(inThread.Messages) != 2 {
		t.Fatalf("thread conversation: %+v %v", inThread, e)
	}
	third := send("Deployed", inThread.Messages[0].ID)
	if third.Target.ID != thread {
		t.Fatalf("reply inside the thread left it: %s", third.Target.ID)
	}
	channel, e := s.Thread("owner", "demo-slack-release")
	if e != nil {
		t.Fatal(e)
	}
	if r := channel.Replies[root.ID]; r.Conversation != thread || r.Count != 3 {
		t.Fatalf("reply summary: %+v", channel.Replies)
	}
	for _, m := range channel.Messages {
		if m.Conversation != "demo-slack-release" {
			t.Fatal("thread replies leaked into the channel")
		}
	}
	if inThread, _ = s.Thread("owner", thread); inThread.Parent == nil || inThread.Parent.Conversation != "demo-slack-release" || inThread.Parent.Message != root.ID {
		t.Fatalf("parent: %+v", inThread.Parent)
	}
	page, e := s.ThreadPage("owner", "demo-slack-release", Page{Around: root.ID})
	if e != nil || page.Replies[root.ID].Count != 3 {
		t.Fatalf("paged reply summary: %+v %v", page.Replies, e)
	}
	if !root.Handled {
		if m, _ := s.Message("owner", root.ID); !m.Handled {
			t.Fatal("thread reply did not handle its parent")
		}
	}
}

func TestOnlyOwnerStartsThreadsAndOnlyOnSlack(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	root := seeded(t, s, "demo-slack-release")
	if e := s.Grant(Grant{Conversation: "demo-slack-release", Expires: now.Add(time.Hour), Send: true}); e != nil {
		t.Fatal(e)
	}
	_, e := s.Draft("agent", DraftInput{RequestID: ID(), ReplyTo: root.ID, Body: "hi", Thread: true})
	status(t, e, 403)
	// An agent still replies in the channel with its grant, and sees no
	// summaries of threads its grant does not cover.
	if _, e = s.Draft("agent", DraftInput{RequestID: ID(), ReplyTo: root.ID, Body: "hi"}); e != nil {
		t.Fatal(e)
	}
	d, _ := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: root.ID, Body: "x", Thread: true})
	if _, e = s.Send("owner", d.ID); e != nil {
		t.Fatal(e)
	}
	if th, _ := s.Thread("agent", "demo-slack-release"); th.Replies != nil || th.Parent != nil {
		t.Fatalf("agent saw thread annotations: %+v", th.Replies)
	}
	tg := seeded(t, s, "demo-telegram-robot")
	_, e = s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: tg.ID, Body: "hi", Thread: true})
	status(t, e, 400)
}

// A thread started from Amy must be the same conversation the Slack import
// later files the thread's replies under, or replies would split in two.
func TestLiveSlackThreadMatchesImportAndDeliversWithThreadTS(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	c := Connector{Provider: "slack", Transport: "agent", Account: "T123:U456", Worker: "slack-worker", Chats: []string{"C789"}, Send: true}
	if e := s.Heartbeat(c); e != nil {
		t.Fatal(e)
	}
	channel := Route{Provider: "slack", Account: c.Account, Chat: "C789"}
	conv := func(r Route, name string) Conversation {
		return Conversation{ID: SlackConversationID(r), Service: "Slack", Account: r.Account, Recipient: r.Chat, Thread: r.Thread, Name: name, Route: r}
	}
	root, e := s.Ingest(Ingress{Conversation: conv(channel, "#release"), Message: Message{NativeID: "1795100000.123456", Sender: "Morgan", SenderID: "U111", Body: "Deploy?"}})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: root.ID, Body: "Going now", Thread: true})
	if e != nil {
		t.Fatal(e)
	}
	threadRoute := channel
	threadRoute.Thread = root.NativeID
	if d.Target.ID != SlackConversationID(threadRoute) || d.Target.Route != threadRoute {
		t.Fatalf("thread target: %+v", d.Target)
	}
	if d, e = s.Send("owner", d.ID); e != nil || d.State != "queued" {
		t.Fatalf("queue: %+v %v", d, e)
	}
	job, e := s.Claim(c.Account, c.Worker, "slack")
	if e != nil || job == nil || job.Draft.Target.Route.Thread != root.NativeID {
		t.Fatalf("worker would not post in the thread: %+v %v", job, e)
	}
	if e = s.AuthorizeDelivery(c.Account, d.ID, job.Draft.Attempt, "slack"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Complete(Receipt{Provider: "slack", Account: c.Account, Draft: d.ID, Attempt: job.Draft.Attempt, State: "sent", NativeID: "1795100003.000001"}); e != nil {
		t.Fatal(e)
	}
	// The import then brings in someone else's reply, and our own again.
	for _, m := range []Message{
		{NativeID: "1795100004.000001", Sender: "Morgan", SenderID: "U111", Body: "thanks"},
		{NativeID: "1795100003.000001", Sender: "You", SenderID: "U456", Body: "Going now", Outgoing: true},
	} {
		if _, e = s.Ingest(Ingress{Conversation: conv(threadRoute, "#release"), Message: m}); e != nil {
			t.Fatalf("import into the started thread: %v", e)
		}
	}
	th, e := s.Thread("owner", SlackConversationID(channel))
	if e != nil || th.Replies[root.ID].Count != 2 {
		t.Fatalf("replies after import: %+v %v", th.Replies, e)
	}
}
