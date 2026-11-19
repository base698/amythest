package messageui

import (
	"strings"
	"testing"

	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSlackPeopleSearchPinsPersonAndOpensOnlyOnEnter(t *testing.T) {
	m, _, _ := harness(t)
	c := messages.Conversation{ID: "slack-test", Service: "Slack", Account: "profile/" + strings.Repeat("a", 64), Name: "general"}
	a := messages.Message{ID: "one", Sender: "Morgan", SenderID: "U123", SourceURL: "https://example.slack.com/archives/C123/p123"}
	b := messages.Message{ID: "two", Sender: "Casey", SenderID: "U456", SourceURL: a.SourceURL}
	m.items, m.cursor = []messages.Item{{Conversation: c, Message: a}}, 0
	m.thread = messages.Thread{Conversation: c, Messages: []messages.Message{a, b, a}}
	var opened string
	m.opts.Open = func(url string) error { opened = url; return nil }
	m.startPeople()
	if m.mode != "people" || len(m.people) != 2 || opened != "" {
		t.Fatal("person picker did not deduplicate or opened early")
	}
	m.peopleInput.SetValue("casey")
	if len(m.peopleMatches()) != 1 {
		t.Fatal("search did not narrow people")
	}
	// A live inbox refresh cannot change the selected recipient.
	m.items[0].Message = messages.Message{SenderID: "U999"}
	cmd := m.peopleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("missing open action")
	}
	m.Update(cmd())
	if opened != "https://example.slack.com/team/U456" || m.mode != "focus" {
		t.Fatal("wrong person", opened)
	}
	if strings.Contains(strings.Join(m.detailLines(100), "\n"), "profile/") {
		t.Fatal("internal account leaked into header")
	}
}

func TestSlackReplyPinsThreadAndSourceRejectsUnsafeURL(t *testing.T) {
	m, _, _ := harness(t)
	m.items = []messages.Item{{Conversation: messages.Conversation{ID: "slack", Service: "Slack", Route: messages.Route{Provider: "slack", Chat: "C123", Thread: "1795100000.123456"}}, Message: messages.Message{ID: "parent", Conversation: "slack"}}}
	m.cursor = 0
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.mode != "compose" || m.target == nil || m.target.Message.ID != "parent" || m.target.Conversation.Route.Thread != "1795100000.123456" {
		t.Fatal("Slack composer lost the selected destination")
	}
	if cmd := m.openSource("file:///etc/passwd"); cmd != nil {
		t.Fatal("opened non-web source")
	}
}
