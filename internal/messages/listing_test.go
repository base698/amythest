package messages

import "testing"

func TestConversationsListsDirectoryAndKinds(t *testing.T) {
	s := testStore(t)
	acct := "00000000-0000-4000-8000-000000000001"
	group := Route{Provider: "signal", Account: acct, Chat: "group/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
	person := Route{Provider: "signal", Account: acct, Chat: "user/00000000-0000-4000-8000-000000000002"}
	if e := s.Heartbeat(Connector{Provider: "signal", Account: acct, Worker: "w", AllChats: true}); e != nil {
		t.Fatal(e)
	}
	conv := func(r Route, name string) Conversation {
		return Conversation{ID: SignalConversationID(r), Service: "Signal", Account: acct, Recipient: r.Chat, Route: r, Name: name}
	}
	if e := s.RegisterConversations("signal", acct, []Conversation{conv(group, "Team"), conv(person, "Pat")}); e != nil {
		t.Fatal(e)
	}
	in := Ingress{Conversation: conv(person, "Pat"), Message: Message{NativeID: "1700000000000/00000000-0000-4000-8000-000000000002", Sender: "Pat", Body: "hi"}}
	if _, e := s.Ingest(in); e != nil {
		t.Fatal(e)
	}
	list, e := s.Conversations("owner")
	if e != nil || len(list) != 2 {
		t.Fatalf("list: %+v %v", list, e)
	}
	if list[0].Conversation.Name != "Pat" || list[0].Kind != "direct" || list[0].LastActivity.IsZero() {
		t.Fatalf("first should be the active direct chat: %+v", list[0])
	}
	if list[1].Conversation.Name != "Team" || list[1].Kind != "group" || !list[1].LastActivity.IsZero() {
		t.Fatalf("second should be the quiet group: %+v", list[1])
	}
	agent, e := s.Conversations("agent")
	if e != nil || len(agent) != 0 {
		t.Fatal("agent saw ungranted conversations")
	}
}

func TestListedSlackChannelsMeetTheirImportedMessages(t *testing.T) {
	s := testStore(t)
	acct := "T123:U456"
	if e := s.Heartbeat(Connector{Provider: "slack", Account: acct, Worker: "w", AllChats: true}); e != nil {
		t.Fatal(e)
	}
	ch := Route{Provider: "slack", Account: acct, Chat: "C789"}
	dm := Route{Provider: "slack", Account: acct, Chat: "D123"}
	conv := func(r Route, name string) Conversation {
		return Conversation{ID: SlackConversationID(r), Service: "Slack", Account: acct, Recipient: r.Chat, Route: r, Name: name}
	}
	if e := s.RegisterConversations("slack", acct, []Conversation{conv(ch, "release"), conv(dm, "Morgan")}); e != nil {
		t.Fatal(e)
	}
	in := Ingress{Conversation: conv(ch, "release"), Message: Message{NativeID: "1795100001.999999", Sender: "Morgan", SenderID: "U999", Body: "shipping today"}}
	if _, e := s.Ingest(in); e != nil {
		t.Fatal(e)
	}
	// Listing again after messages arrived changes nothing it should not.
	if e := s.RegisterConversations("slack", acct, []Conversation{conv(ch, "release-renamed")}); e != nil {
		t.Fatal(e)
	}
	list, e := s.Conversations("owner")
	if e != nil || len(list) != 2 {
		t.Fatalf("%+v %v", list, e)
	}
	if list[0].Conversation.Route.Chat != "C789" || list[0].Kind != "channel" || list[0].LastActivity.IsZero() {
		t.Fatalf("channel: %+v", list[0])
	}
	if list[1].Kind != "direct" {
		t.Fatalf("dm: %+v", list[1])
	}
}
