package messages

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignalNativeRoutingAndProviderCredentialIsolation(t *testing.T) {
	s := testStore(t)
	signalToken := strings.Repeat("s", 64)
	telegramToken := strings.Repeat("t", 64)
	ownerToken := strings.Repeat("o", 64)
	server := httptest.NewServer((&Gateway{Store: s, OwnerToken: ownerToken, ConnectorToken: telegramToken, ConnectorTokens: map[string]string{"signal": signalToken}}).Handler())
	defer server.Close()
	sc := NewClient(server.URL, signalToken)
	tc := NewClient(server.URL, telegramToken)
	ctx := context.Background()
	account := "00000000-0000-4000-8000-000000000001"
	author := "00000000-0000-4000-8000-000000000002"
	c := Connector{Provider: "signal", Account: account, Worker: "test-worker", Chats: []string{"user/" + author}, Send: true}
	if e := tc.Do(ctx, "POST", "/connector/heartbeat", c, nil); e == nil {
		t.Fatal("Telegram registered a Signal connector")
	}
	if e := sc.Do(ctx, "POST", "/connector/heartbeat", c, nil); e != nil {
		t.Fatal(e)
	}
	route := Route{Provider: "signal", Account: account, Chat: c.Chats[0]}
	in := Ingress{Conversation: Conversation{ID: SignalConversationID(route), Service: "Signal", Account: account, Recipient: route.Chat, Name: "Test contact", Route: route}, Message: Message{NativeID: "1700000000000/" + author, Sender: "Test contact", Body: "Please send the patch"}}
	if e := tc.Do(ctx, "POST", "/connector/ingest", in, nil); e == nil {
		t.Fatal("cross-provider ingestion accepted")
	}
	var m Message
	if e := sc.Do(ctx, "POST", "/connector/ingest", in, &m); e != nil {
		t.Fatal(e)
	}
	a, e := s.AddAttachment("patch.txt", []byte("exact bytes"))
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "Here", Attachments: []string{a.ID}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send("owner", d.ID); e != nil {
		t.Fatal(e)
	}
	var response struct {
		Delivery *Delivery `json:"delivery"`
	}
	claim := map[string]string{"account": account, "worker": c.Worker}
	if e = tc.Do(ctx, "POST", "/connector/claim", claim, &response); e == nil {
		t.Fatal("cross-provider claim accepted")
	}
	if e = sc.Do(ctx, "POST", "/connector/claim", claim, &response); e != nil || response.Delivery == nil {
		t.Fatalf("claim: %v", e)
	}
	job := response.Delivery
	if job.Parent.NativeID != in.Message.NativeID || job.Draft.Target.Route != route {
		t.Fatal("native reply parent or destination changed")
	}
	authorization := map[string]string{"account": account, "draft": d.ID, "attempt": job.Draft.Attempt}
	if e = tc.Do(ctx, "POST", "/connector/authorize", authorization, nil); e == nil {
		t.Fatal("cross-provider authorization")
	}
	if e = sc.Do(ctx, "POST", "/connector/authorize", authorization, nil); e != nil {
		t.Fatal(e)
	}
	_, _, e = s.DeliveryAttachment(account, d.ID, job.Draft.Attempt, a.ID, "telegram")
	status(t, e, 403)
	_, bytes, e := s.DeliveryAttachment(account, d.ID, job.Draft.Attempt, a.ID, "signal")
	if e != nil || string(bytes) != "exact bytes" {
		t.Fatal(e)
	}
	receipt := Receipt{Provider: "signal", Account: account, Draft: d.ID, Attempt: job.Draft.Attempt, State: "sent", NativeID: "1700000000001"}
	if e = tc.Do(ctx, "POST", "/connector/complete", receipt, nil); e == nil {
		t.Fatal("cross-provider receipt accepted")
	}
	if e = sc.Do(ctx, "POST", "/connector/complete", receipt, nil); e != nil {
		t.Fatal(e)
	}
	if e = sc.Do(ctx, "POST", "/connector/complete", receipt, nil); e != nil {
		t.Fatal("receipt replay failed", e)
	}
	items, e := s.Items("owner", false)
	if e != nil || len(items) != 0 {
		t.Fatal("delivered parent not handled", e)
	}
	// Reverse direction is equally isolated.
	telegramMessage, telegramConnector := liveFixture(t, s)
	td, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: telegramMessage.ID, Body: "test"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send("owner", td.ID); e != nil {
		t.Fatal(e)
	}
	if e = sc.Do(ctx, "POST", "/connector/claim", map[string]string{"account": telegramConnector.Account, "worker": telegramConnector.Worker}, nil); e == nil {
		t.Fatal("Signal claimed Telegram work")
	}
}

func TestSignalRejectsAmbiguousSourceIDs(t *testing.T) {
	s := testStore(t)
	a := "00000000-0000-4000-8000-000000000001"
	if e := s.Heartbeat(Connector{Provider: "signal", Account: a, Worker: "worker", AllChats: true}); e != nil {
		t.Fatal(e)
	}
	r := Route{Provider: "signal", Account: a, Chat: "group/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
	in := Ingress{Conversation: Conversation{ID: SignalConversationID(r), Service: "Signal", Account: a, Recipient: r.Chat, Route: r}, Message: Message{NativeID: "123/00000000-0000-4000-8000-000000000002"}}
	if e := s.ValidateIngress(in); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"123", "123/display-name", "-1/00000000-0000-4000-8000-000000000002"} {
		bad := in
		bad.Message.NativeID = id
		status(t, s.ValidateIngress(bad), 400)
	}
	bad := in
	bad.Conversation.Route.Thread = "topic"
	status(t, s.ValidateIngress(bad), 400)
	bad = in
	bad.Conversation.ID = "signal/friendly-name"
	status(t, s.ValidateIngress(bad), 400)
}
