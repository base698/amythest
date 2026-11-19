package messages

import (
	"strings"
	"testing"
	"time"
)

func TestSlackReplayEnrichesNamesWithoutChangingMessage(t *testing.T) {
	s := testStore(t)
	r := Route{Provider: "slack", Account: "T123:U456", Chat: "C789"}
	in := Ingress{Conversation: Conversation{ID: SlackConversationID(r), Service: "Slack", Account: r.Account, Name: "general", Recipient: r.Chat, Route: r}, Message: Message{NativeID: "1795100001.999999", Sender: "U789", Body: "Original", At: time.Now()}}
	one, e := s.Ingest(in)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Attention(one.ID, "handled"); e != nil {
		t.Fatal(e)
	}
	in.Message.Sender, in.Message.SenderID, in.Message.Body = "Morgan", "U789", "Must not replace original"
	two, e := s.Ingest(in)
	if e != nil {
		t.Fatal(e)
	}
	if two.ID != one.ID || two.Sender != "Morgan" || two.SenderID != "U789" || two.Body != "Original" || !two.Handled || !two.At.Equal(one.At) {
		t.Fatalf("incorrect enrichment: %+v", two)
	}
	in.Message.SenderID = "U999"
	if _, e = s.Ingest(in); e == nil {
		t.Fatal("replaced sender identity")
	}
	thread, e := s.Thread("owner", in.Conversation.ID)
	if e != nil || len(thread.Messages) != 1 {
		t.Fatal("replay duplicated the message", e)
	}
}

func TestSlackPersonLinkUsesCorrectWorkspace(t *testing.T) {
	c := Conversation{Service: "Slack", Account: "T123:U456", Name: "general"}
	m := Message{Sender: "Morgan", SenderID: "U789", SourceURL: "https://example.slack.com/archives/C789/p1795100001999999"}
	if got := SlackPersonURL(c, m); got != "slack://user?id=U789&team=T123" {
		t.Fatal(got)
	}
	c.Account = "profile/" + strings.Repeat("a", 64)
	if got := SlackPersonURL(c, m); got != "https://example.slack.com/team/U789" {
		t.Fatal(got)
	}
	for _, raw := range []string{"https://example.slack.com.evil.test/archives/C789/p1", "https://app.slack.com/archives/C789/p1", "https://example.slack.com:443/archives/C789/p1", "https://example.slack.com@evil.test/archives/C789/p1"} {
		m.SourceURL = raw
		if SlackPersonURL(c, m) != "" {
			t.Fatal("accepted unrelated workspace", raw)
		}
	}
	if got := Destination(c); strings.Contains(got, "profile/") || got != "Slack → general" {
		t.Fatal(got)
	}
}
