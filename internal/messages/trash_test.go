package messages

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTrashStaysHiddenAcrossNewMessagesAndRestores(t *testing.T) {
	s := testStore(t)
	m, connector := liveFixture(t, s)
	if e := s.SetTrashed(m.Conversation, true); e != nil {
		t.Fatal(e)
	}
	for _, all := range []bool{false, true} {
		items, e := s.Items("owner", all)
		if e != nil || len(items) != 0 {
			t.Fatal("trash leaked into inbox", e)
		}
	}
	trash, e := s.Items("owner", true, true)
	if e != nil || len(trash) != 1 {
		t.Fatal("trash missing", e)
	}
	in := Ingress{Conversation: trash[0].Conversation, Message: Message{NativeID: "77", Sender: "Same sender", Body: "Another message", At: time.Now().Add(time.Minute)}}
	if _, e = s.Ingest(in); e != nil {
		t.Fatal(e)
	}
	items, e := s.Items("owner", true)
	if e != nil || len(items) != 0 {
		t.Fatal("new message untrashed conversation", e)
	}
	trash, e = s.Items("owner", true, true)
	if e != nil || len(trash) != 1 || trash[0].Message.NativeID != "77" {
		t.Fatal("trash did not retain new message", e)
	}
	if e = s.SetTrashed(m.Conversation, false); e != nil {
		t.Fatal(e)
	}
	items, e = s.Items("owner", true)
	if e != nil || len(items) != 1 {
		t.Fatal("restore failed", e)
	}
	server := httptest.NewServer((&Gateway{Store: s, OwnerToken: strings.Repeat("o", 64), AgentToken: strings.Repeat("a", 64), ConnectorToken: strings.Repeat("t", 64)}).Handler())
	defer server.Close()
	for _, token := range []string{strings.Repeat("a", 64), strings.Repeat("t", 64)} {
		api := NewClient(server.URL, token)
		if e = api.Do(context.Background(), "POST", "/trash", map[string]any{"conversation": m.Conversation, "trashed": true}, nil); e == nil {
			t.Fatal("non-owner changed trash")
		}
	}
	// Removing the local view does not change connector scope or provider data.
	cs, e := s.Connectors()
	if e != nil || len(cs) != 1 || cs[0].Account != connector.Account {
		t.Fatal(e)
	}
	thread, e := s.Thread("owner", m.Conversation)
	if e != nil || len(thread.Messages) != 2 {
		t.Fatal("trash erased history")
	}
}

func TestDisplayOmitsDecoderPlaceholdersAndPreservesUnicode(t *testing.T) {
	if got := Plain("\uFFFDHi\uFFFC! 👩🏽‍💻 café\n你好"); got != "Hi! 👩🏽‍💻 café\n你好" {
		t.Fatalf("display sanitization: %q", got)
	}
}
