package messages

import (
	"strings"
	"testing"
	"time"
)

func slackFixture(t *testing.T, s *Store) (Message, Connector) {
	t.Helper()
	c := Connector{Provider: "slack", Transport: "agent", Account: "T123:U456", Worker: "slack-worker", Chats: []string{"C789"}, Send: true}
	if e := s.Heartbeat(c); e != nil {
		t.Fatal(e)
	}
	r := Route{Provider: "slack", Account: c.Account, Chat: c.Chats[0], Thread: "1795100000.123456"}
	m, e := s.Ingest(Ingress{Conversation: Conversation{ID: SlackConversationID(r), Service: "Slack", Account: r.Account, Recipient: r.Chat, Thread: r.Thread, Name: "Example", Route: r}, Message: Message{NativeID: "1795100001.999999", Sender: "Morgan", Body: "Review?"}})
	if e != nil {
		t.Fatal(e)
	}
	return m, c
}

func TestOfflineQueueSurvivesRestartAndSlackReceiptDeduplicates(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	s.now = func() time.Time { return now }
	m, c := slackFixture(t, s)
	now = now.Add(time.Hour)
	d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "Reviewed"})
	if e != nil {
		t.Fatal(e)
	}
	d, e = s.Send("owner", d.ID)
	if e != nil || d.State != "queued" {
		t.Fatal("offline send did not queue", e)
	}
	if _, e = s.Claim(c.Account, c.Worker, "slack"); e == nil {
		t.Fatal("stale worker claimed")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.now = func() time.Time { return now }
	if e = s.Heartbeat(c); e != nil {
		t.Fatal(e)
	}
	job, e := s.Claim(c.Account, c.Worker, "slack")
	if e != nil || job == nil || job.Draft.Target.Route.Thread != "1795100000.123456" {
		t.Fatal("lost queued thread", e)
	}
	if e = s.AuthorizeDelivery(c.Account, d.ID, job.Draft.Attempt, "slack"); e != nil {
		t.Fatal(e)
	}
	r := Receipt{Provider: "slack", Account: c.Account, Draft: d.ID, Attempt: job.Draft.Attempt, State: "sent", NativeID: "1795100002.000003"}
	for i := 0; i < 2; i++ {
		if _, e = s.Complete(r); e != nil {
			t.Fatal(e)
		}
	}
	th, e := s.Thread("owner", m.Conversation)
	if e != nil || len(th.Messages) != 2 || th.Messages[1].NativeID != r.NativeID {
		t.Fatal("duplicate or lost receipt", e)
	}
	// A subsequent provider import enriches the outgoing placeholder without duplication.
	_, e = s.Ingest(Ingress{Conversation: th.Conversation, Message: Message{NativeID: r.NativeID, Sender: "Casey", SenderID: "U456", Body: "Reviewed", Outgoing: true}})
	if e != nil {
		t.Fatal(e)
	}
	th, e = s.Thread("owner", m.Conversation)
	if e != nil || len(th.Messages) != 2 || th.Messages[1].SenderID != "U456" {
		t.Fatal("outgoing identity was not enriched", e)
	}
}

func TestSlackLimitsScopeAndRevocation(t *testing.T) {
	s := testStore(t)
	m, c := slackFixture(t, s)
	a, e := s.AddAttachment("file.txt", []byte("text"))
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.AddAttachment("second.txt", []byte("more"))
	if e != nil {
		t.Fatal(e)
	}
	// One file per Slack message.
	one, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "file", Attachments: []string{a.ID}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send("owner", one.ID); e != nil {
		t.Fatal("one file refused:", e)
	}
	for _, input := range []DraftInput{
		{RequestID: ID(), ReplyTo: m.ID, Body: strings.Repeat("a", 5001)},
		{RequestID: ID(), ReplyTo: m.ID, Body: "files", Attachments: []string{a.ID, b.ID}},
	} {
		d, e := s.Draft("owner", input)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Send("owner", d.ID); e == nil {
			t.Fatal("unsupported payload queued")
		}
	}
	g := Grant{Conversation: m.Conversation, Expires: time.Now().Add(time.Hour), Send: true}
	if e = s.Grant(g); e != nil {
		t.Fatal(e)
	}
	d, e := s.Draft("agent", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "Reply"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send("agent", d.ID); e != nil {
		t.Fatal(e)
	}
	job, e := s.Claim(c.Account, c.Worker, "slack")
	if e != nil || job == nil {
		t.Fatal(e)
	}
	if e = s.Revoke(m.Conversation); e != nil {
		t.Fatal(e)
	}
	status(t, s.AuthorizeDelivery(c.Account, d.ID, job.Draft.Attempt, "slack"), 403)
	c.Chats = []string{"C999"}
	if e = s.Heartbeat(c); e != nil {
		t.Fatal(e)
	}
	d, e = s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "Outside scope"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send("owner", d.ID); e == nil {
		t.Fatal("outside-scope send queued")
	}
}

func TestSlackFileShareIsSubmittedNotSent(t *testing.T) {
	s := testStore(t)
	m, c := slackFixture(t, s)
	a, e := s.AddAttachment("chart.png", []byte("\x89PNG\r\n\x1a\nchart"))
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Attachments: []string{a.ID}})
	if e != nil {
		t.Fatal(e)
	}
	if d, e = s.Send("owner", d.ID); e != nil || d.State != "queued" {
		t.Fatal("file share not queued", e)
	}
	job, e := s.Claim(c.Account, c.Worker, "slack")
	if e != nil || job == nil || len(job.Draft.Attachments) != 1 {
		t.Fatal("claim", e)
	}
	if e = s.AuthorizeDelivery(c.Account, d.ID, job.Draft.Attempt, "slack"); e != nil {
		t.Fatal(e)
	}
	r := Receipt{Provider: "slack", Account: c.Account, Draft: d.ID, Attempt: job.Draft.Attempt, State: "submitted"}
	if d, e = s.Complete(r); e != nil || d.State != "submitted" {
		t.Fatalf("submitted file share: %+v %v", d.State, e)
	}
}
