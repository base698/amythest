package messages

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func liveFixture(t *testing.T, s *Store) (Message, Connector) {
	t.Helper()
	c := Connector{Account: "123", Worker: "worker-one", Chats: []string{"-100456"}, Send: true}
	if e := s.Heartbeat(c); e != nil {
		t.Fatal(e)
	}
	r := Route{Provider: "telegram", Account: c.Account, Chat: c.Chats[0], Thread: "7"}
	in := Ingress{Conversation: Conversation{ID: TelegramConversationID(r), Service: "Telegram", Account: r.Account, Recipient: r.Chat, Thread: r.Thread, Name: "Build group", Route: r}, Message: Message{NativeID: "55", Sender: "Colleague", Body: "Please share the image"}}
	if e := s.ValidateIngress(in); e != nil {
		t.Fatal(e)
	}
	m, e := s.Ingest(in)
	if e != nil {
		t.Fatal(e)
	}
	return m, c
}
func TestLiveOutboxReceiptRestartAndDuplicateSend(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	m, c := liveFixture(t, s)
	a, e := s.AddAttachment("wall.png", []byte("exact image snapshot"))
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "Here it is", Attachments: []string{a.ID}})
	if e != nil {
		t.Fatal(e)
	}
	d, e = s.Send("owner", d.ID)
	if e != nil || d.State != "queued" {
		t.Fatalf("queue %+v %v", d, e)
	}
	d, e = s.Send("owner", d.ID)
	if e != nil || d.State != "queued" {
		t.Fatal(e)
	}
	job, e := s.Claim(c.Account, c.Worker)
	if e != nil || job == nil {
		t.Fatalf("claim %v", e)
	}
	if job.Parent.NativeID != "55" || job.Draft.Target.Route.Chat != "-100456" || job.Draft.Target.Route.Thread != "7" {
		t.Fatal("lost Telegram routing")
	}
	next, e := s.Claim(c.Account, c.Worker)
	if e != nil || next != nil {
		t.Fatal("double claim")
	}
	_, data, e := s.DeliveryAttachment(c.Account, d.ID, job.Draft.Attempt, a.ID)
	if e != nil || string(data) != "exact image snapshot" {
		t.Fatal("changed attachment")
	}
	_, _, e = s.DeliveryAttachment(c.Account, d.ID, "wrong", a.ID)
	status(t, e, 403)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	next, e = s.Claim(c.Account, c.Worker)
	if e != nil || next != nil {
		t.Fatal("restart retried a send")
	}
	receipt := Receipt{Account: c.Account, Draft: d.ID, Attempt: job.Draft.Attempt, State: "sent", NativeID: "56"}
	got, e := s.Complete(receipt)
	if e != nil || got.State != "sent" {
		t.Fatalf("complete %+v %v", got, e)
	}
	if _, e = s.Complete(receipt); e != nil {
		t.Fatal("receipt retry", e)
	}
	if _, e = s.Send("owner", d.ID); e != nil {
		t.Fatal("send retry", e)
	}
	th, e := s.Thread("owner", m.Conversation)
	if e != nil || len(th.Messages) != 2 || !th.Messages[0].Handled || th.Messages[1].ReplyTo != m.ID {
		t.Fatalf("receipt not atomic %+v %v", th, e)
	}
}
func TestLiveGrantsCheckedAtQueueClaimAndProviderBoundary(t *testing.T) {
	s := testStore(t)
	m, c := liveFixture(t, s)
	g := Grant{Conversation: m.Conversation, Expires: time.Now().Add(time.Hour), Send: true}
	if e := s.Grant(g); e != nil {
		t.Fatal(e)
	}
	d, e := s.Draft("agent", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "Reply"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send("agent", d.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Revoke(m.Conversation); e != nil {
		t.Fatal(e)
	}
	job, e := s.Claim(c.Account, c.Worker)
	if e != nil || job != nil {
		t.Fatal("revoked queued reply sent")
	}
	if _, e = s.Send("owner", d.ID); e != nil {
		t.Fatal(e)
	}
	job, e = s.Claim(c.Account, c.Worker)
	if e != nil || job == nil {
		t.Fatal(e)
	}
	// Owner authorization replaces the revoked agent's grant.
	if e = s.AuthorizeDelivery(c.Account, d.ID, job.Draft.Attempt); e != nil {
		t.Fatal(e)
	}
	if e = s.Grant(g); e != nil {
		t.Fatal(e)
	}
	d, e = s.Draft("agent", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "Another reply"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send("agent", d.ID); e != nil {
		t.Fatal(e)
	}
	job, e = s.Claim(c.Account, c.Worker)
	if e != nil || job == nil {
		t.Fatal(e)
	}
	if e = s.Revoke(m.Conversation); e != nil {
		t.Fatal(e)
	}
	status(t, s.AuthorizeDelivery(c.Account, d.ID, job.Draft.Attempt), 403)
}
func TestInterruptedDeliveryIsNeverAutomaticallyRetried(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	m, c := liveFixture(t, s)
	d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "Reply"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send("owner", d.ID); e != nil {
		t.Fatal(e)
	}
	job, e := s.Claim(c.Account, c.Worker)
	if e != nil || job == nil {
		t.Fatal(e)
	}
	now = now.Add(3 * time.Minute)
	if e = s.Heartbeat(c); e != nil {
		t.Fatal(e)
	}
	next, e := s.Claim(c.Account, c.Worker)
	if e != nil || next != nil {
		t.Fatal("ambiguous send retried")
	}
	_, e = s.Send("owner", d.ID)
	status(t, e, 409)
	ds, e := s.Drafts("owner")
	if e != nil || ds[0].State != "uncertain" {
		t.Fatal("missing uncertainty")
	}
	// A durable provider receipt can still reconcile after the timeout.
	if _, e = s.Complete(Receipt{Account: c.Account, Draft: d.ID, Attempt: job.Draft.Attempt, State: "sent", NativeID: "99"}); e != nil {
		t.Fatal(e)
	}
}
func TestConnectorCredentialCannotReadInboxOrSendWithoutClaim(t *testing.T) {
	s := testStore(t)
	_, c := liveFixture(t, s)
	token := strings.Repeat("c", 64)
	srv := httptest.NewServer((&Gateway{Store: s, OwnerToken: strings.Repeat("o", 64), ConnectorToken: token}).Handler())
	defer srv.Close()
	client := NewClient(srv.URL, token)
	ctx := context.Background()
	_, e := client.Items(ctx, true)
	status(t, e, 403)
	_, e = client.Send(ctx, "anything")
	status(t, e, 403)
	_, e = client.Attach(ctx, "file.txt", []byte("arbitrary"))
	status(t, e, 403)
	var result map[string]any
	e = client.Do(ctx, "POST", "/connector/claim", map[string]string{"account": c.Account, "worker": c.Worker}, &result)
	if e != nil || result["delivery"] != nil {
		t.Fatalf("empty claim %+v %v", result, e)
	}
}
