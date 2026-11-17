package messages

import "testing"

func TestIMessageSubmissionIsNotConfirmedOrRetried(t *testing.T) {
	s := testStore(t)
	r := Route{Provider: "imessage", Account: "00000000-0000-4000-8000-000000000001", Chat: "any;+;chat-test"}
	c := Connector{Provider: r.Provider, Account: r.Account, Worker: "mac-worker", Chats: []string{r.Chat}, Send: true}
	if e := s.Heartbeat(c); e != nil {
		t.Fatal(e)
	}
	in := Ingress{Conversation: Conversation{ID: IMessageConversationID(r), Service: "iMessage", Account: r.Account, Recipient: r.Chat, Name: "Mac chat", Route: r}, Message: Message{NativeID: "TEST-MESSAGE-GUID", Sender: "Colleague", Body: "Please reply"}}
	if e := s.ValidateIngress(in); e != nil {
		t.Fatal(e)
	}
	m, e := s.Ingest(in)
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "Reply"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send("owner", d.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Claim(c.Account, c.Worker, "signal"); e == nil {
		t.Fatal("Signal claimed iMessage")
	}
	job, e := s.Claim(c.Account, c.Worker, "imessage")
	if e != nil || job == nil {
		t.Fatalf("claim: %v", e)
	}
	receipt := Receipt{Provider: "imessage", Account: c.Account, Draft: d.ID, Attempt: job.Draft.Attempt, State: "sent", NativeID: "123"}
	_, e = s.Complete(receipt)
	status(t, e, 400)
	receipt.State = "submitted"
	receipt.NativeID = ""
	result, e := s.Complete(receipt)
	if e != nil || result.State != "submitted" {
		t.Fatal(e)
	}
	if _, e = s.Complete(receipt); e != nil {
		t.Fatal("receipt replay", e)
	}
	result, e = s.Send("owner", d.ID)
	if e != nil || result.State != "submitted" {
		t.Fatal("submitted reply retried", e)
	}
	next, e := s.Claim(c.Account, c.Worker, "imessage")
	if e != nil || next != nil {
		t.Fatal("duplicate delivery", e)
	}
	items, e := s.Items("owner", false)
	if e != nil || len(items) != 1 {
		t.Fatal("unconfirmed submission cleared attention", e)
	}
	// SMS/RCS are not silently added to the iMessage adapter's scope.
	bad := in
	bad.Conversation.Route.Chat = "SMS;+;chat-test"
	status(t, s.ValidateIngress(bad), 400)
}

func TestNativeMessagesShortCodeGUID(t *testing.T) {
	for _, id := range []string{"any;-;1234", "SMS;-;123", "RCS;+;group"} {
		if !imessageChat(id) {
			t.Fatalf("rejected native Messages GUID %q", id)
		}
	}
	for _, id := range []string{"any;-;", "any;;1234", "any;-;123\n", "unknown;-;1234"} {
		if imessageChat(id) {
			t.Fatalf("accepted malformed native GUID %q", id)
		}
	}
}
