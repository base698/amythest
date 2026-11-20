package messages

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTagsPutMessagesOnAProjectWall(t *testing.T) {
	s := testStore(t)
	if e := s.Seed(); e != nil {
		t.Fatal(e)
	}
	th, e := s.Thread("owner", "demo-slack-release")
	if e != nil || len(th.Messages) == 0 {
		t.Fatal(e)
	}
	msg := th.Messages[0]
	srv := httptest.NewServer((&Gateway{Store: s, OwnerToken: strings.Repeat("o", 64), AgentToken: strings.Repeat("a", 64)}).Handler())
	defer srv.Close()
	owner, agent := NewClient(srv.URL, strings.Repeat("o", 64)), NewClient(srv.URL, strings.Repeat("a", 64))
	ctx := context.Background()
	tag, e := owner.Tag(ctx, msg.ID, "amythest")
	if e != nil || tag.Conversation != "demo-slack-release" || tag.At.IsZero() {
		t.Fatalf("tag: %+v %v", tag, e)
	}
	if again, e := owner.Tag(ctx, msg.ID, "amythest"); e != nil || !again.At.Equal(tag.At) {
		t.Fatalf("retag moved the time: %+v %v", again, e)
	}
	if _, e = owner.Tag(ctx, msg.ID, " bad "); e == nil {
		t.Fatal("blank-edged project accepted")
	}
	if _, e = owner.Tag(ctx, "no-such-message", "amythest"); e == nil {
		t.Fatal("tagged a missing message")
	}
	list, e := owner.Tagged(ctx, "amythest", 0)
	if e != nil || len(list) != 1 || list[0].Message.ID != msg.ID || list[0].Conversation.Name != "#release" {
		t.Fatalf("tagged: %+v %v", list, e)
	}
	if other, _ := owner.Tagged(ctx, "elsewhere", 0); len(other) != 0 {
		t.Fatal("tag leaked to another project")
	}
	th, _ = s.Thread("owner", "demo-slack-release")
	if got := th.Tags[msg.ID]; len(got) != 1 || got[0] != "amythest" {
		t.Fatalf("thread tags: %v", th.Tags)
	}
	if _, e = agent.Tagged(ctx, "amythest", 0); e == nil {
		t.Fatal("agent read tags")
	}
	if _, e = agent.Tag(ctx, msg.ID, "amythest"); e == nil {
		t.Fatal("agent tagged")
	}
	if e = owner.Untag(ctx, msg.ID, "amythest"); e != nil {
		t.Fatal(e)
	}
	if list, _ = owner.Tagged(ctx, "amythest", 0); len(list) != 0 {
		t.Fatal("untag kept the message")
	}
}
