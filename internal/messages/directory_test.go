package messages

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectoryContactsWithoutInventedMessages(t *testing.T) {
	s := testStore(t)
	r := Route{Provider: "signal", Account: "00000000-0000-4000-8000-000000000001", Chat: "user/00000000-0000-4000-8000-000000000002"}
	conn := Connector{Provider: "signal", Account: r.Account, Worker: "directory-worker", Chats: []string{r.Chat}, Send: true}
	if e := s.Heartbeat(conn); e != nil {
		t.Fatal(e)
	}
	contact := Conversation{ID: SignalConversationID(r), Service: "Signal", Account: r.Account, Recipient: r.Chat, Route: r, Name: "Test contact"}
	server := httptest.NewServer((&Gateway{Store: s, OwnerToken: strings.Repeat("o", 64), ConnectorToken: strings.Repeat("t", 64), ConnectorTokens: map[string]string{"signal": strings.Repeat("s", 64)}}).Handler())
	defer server.Close()
	api := NewClient(server.URL, strings.Repeat("s", 64))
	other := NewClient(server.URL, strings.Repeat("t", 64))
	ctx := context.Background()
	payload := map[string]any{"account": r.Account, "conversations": []Conversation{contact}}
	if e := other.Do(ctx, "POST", "/connector/conversations", payload, nil); e == nil {
		t.Fatal("wrong provider imported contacts")
	}
	if e := api.Do(ctx, "POST", "/connector/conversations", payload, nil); e != nil {
		t.Fatal(e)
	}
	all, e := s.Items("owner", true)
	if e != nil || len(all) != 1 || all[0].Message.ID != "" || all[0].Message.Category != "Contact" {
		t.Fatalf("directory: %+v %v", all, e)
	}
	focus, e := s.Items("owner", false)
	if e != nil || len(focus) != 0 {
		t.Fatal("quiet contact entered attention queue")
	}
	thread, e := s.Thread("owner", contact.ID)
	if e != nil || len(thread.Messages) != 0 {
		t.Fatal("invented history")
	}
	agent, e := s.Items("agent", true)
	if e != nil || len(agent) != 0 {
		t.Fatal("agent can enumerate ungranted contacts")
	}
	contact.Name = "Renamed contact"
	if e = s.RegisterConversations("signal", r.Account, []Conversation{contact}); e != nil {
		t.Fatal(e)
	}
	in := Ingress{Conversation: contact, Message: Message{NativeID: "1700000000000/00000000-0000-4000-8000-000000000002", Sender: "Colleague", Body: "Please reply"}}
	if e = s.ValidateIngress(in); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Ingest(in); e != nil {
		t.Fatal(e)
	}
	all, e = s.Items("owner", true)
	if e != nil || len(all) != 1 || all[0].Message.ID == "" || all[0].Conversation.Name != "Renamed contact" {
		t.Fatal("real message did not replace empty state")
	}
	bad := contact
	bad.Route.Chat = "user/00000000-0000-4000-8000-000000000003"
	status(t, s.RegisterConversations("signal", r.Account, []Conversation{bad}), 403)
}
