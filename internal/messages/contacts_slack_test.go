package messages

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContactNameSurvivesReingestWithoutRedirecting(t *testing.T) {
	s := testStore(t)
	if e := s.Seed(); e != nil {
		t.Fatal(e)
	}
	items, e := s.Items("owner", true)
	if e != nil {
		t.Fatal(e)
	}
	it := items[0]
	route := it.Conversation
	if e = s.NameContact(route.ID, "Morgan Example"); e != nil {
		t.Fatal(e)
	}
	in := Ingress{Conversation: route, Message: Message{NativeID: "second", Sender: "provider name", Body: "Another incoming message"}}
	in.Conversation.Name = "provider changed name"
	m, e := s.Ingest(in)
	if e != nil {
		t.Fatal(e)
	}
	th, e := s.Thread("owner", route.ID)
	if e != nil || th.Conversation.Name != "Morgan Example" || th.Conversation.Route != route.Route {
		t.Fatal("name or route", e, th.Conversation)
	}
	d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "draft only"})
	if e != nil || d.Target.Recipient != route.Recipient {
		t.Fatal("destination changed", e)
	}
	srv := httptest.NewServer((&Gateway{Store: s, OwnerToken: strings.Repeat("o", 64), AgentToken: strings.Repeat("a", 64)}).Handler())
	defer srv.Close()
	e = NewClient(srv.URL, strings.Repeat("a", 64)).Do(context.Background(), "PUT", "/contacts", map[string]string{"conversation": route.ID, "name": "No"}, nil)
	if e == nil {
		t.Fatal("agent changed contact")
	}
	if e = s.NameContact(route.ID, ""); e != nil {
		t.Fatal(e)
	}
	th, e = s.Thread("owner", route.ID)
	if e != nil || th.Conversation.Name != "provider changed name" {
		t.Fatal("reset failed")
	}
}
func TestSlackStringIDsScopeReplayAndReadOnly(t *testing.T) {
	s := testStore(t)
	r := Route{Provider: "slack", Account: "T123:U456", Chat: "C789", Thread: "1795100000.123456"}
	c := Connector{Provider: "slack", Account: r.Account, Worker: "worker", Chats: []string{r.Chat}}
	if e := s.Heartbeat(c); e != nil {
		t.Fatal(e)
	}
	in := Ingress{Conversation: Conversation{ID: SlackConversationID(r), Service: "Slack", Account: r.Account, Name: "Thread", Thread: r.Thread, Recipient: r.Chat, Route: r}, Message: Message{NativeID: "1795100001.999999", Sender: "Morgan", Body: "A request?", SourceURL: "https://example.slack.com/archives/C789/p1795100001999999"}}
	if e := s.ValidateIngress(in); e != nil {
		t.Fatal(e)
	}
	one, e := s.Ingest(in)
	if e != nil {
		t.Fatal(e)
	}
	two, e := s.Ingest(in)
	if e != nil || one.ID != two.ID {
		t.Fatal("replay", e)
	}
	d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: one.ID, Body: "must stay draft"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send("owner", d.ID); e == nil {
		t.Fatal("read-only Slack sent")
	}
	c.Send = true
	if e = s.Heartbeat(c); e == nil {
		t.Fatal("enabled Slack sends")
	}
	in.Message.NativeID = "1795100001123456"
	if e = s.ValidateIngress(in); e == nil {
		t.Fatal("accepted numeric timestamp")
	}
	other := r
	other.Account = "T999:U456"
	if SlackConversationID(other) == SlackConversationID(r) {
		t.Fatal("account collision")
	}
}

func TestBrowserSourcesCannotSend(t *testing.T) {
	for provider, service := range browserServices {
		s := testStore(t)
		r := Route{Provider: provider, Account: "reader@example.test", Chat: strings.Repeat("c", 64)}
		c := Connector{Provider: provider, Transport: "chrome", Account: r.Account, Worker: "browser", AllChats: true}
		if e := s.Heartbeat(c); e != nil {
			t.Fatal(e)
		}
		in := Ingress{Conversation: Conversation{ID: MailConversationID(r), Service: service, Account: r.Account, Recipient: r.Chat, Name: "Visible snapshot", Route: r}, Message: Message{NativeID: strings.Repeat("d", 64), Body: "Calendar snapshot", SourceURL: "https://outlook.cloud.microsoft/calendar/view/workweek"}}
		if e := s.ValidateIngress(in); e != nil {
			t.Fatal(e)
		}
		m, e := s.Ingest(in)
		if e != nil {
			t.Fatal(e)
		}
		d, e := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "must stay draft"})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Send("owner", d.ID); e == nil {
			t.Fatal("browser source sent")
		}
		c.Send = true
		if e = s.Heartbeat(c); e == nil {
			t.Fatal("enabled sends")
		}
	}
}
