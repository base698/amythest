package messages

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectorsUploadIncomingFilesBeforeTheMessage(t *testing.T) {
	s := testStore(t)
	acct := "00000000-0000-4000-8000-000000000001"
	r := Route{Provider: "signal", Account: acct, Chat: "user/00000000-0000-4000-8000-000000000002"}
	if e := s.Heartbeat(Connector{Provider: "signal", Account: acct, Worker: "w", AllChats: true}); e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer((&Gateway{Store: s, OwnerToken: strings.Repeat("o", 64), AgentToken: strings.Repeat("a", 64), ConnectorTokens: map[string]string{"signal": strings.Repeat("s", 64)}}).Handler())
	defer srv.Close()
	api := NewClient(srv.URL, strings.Repeat("s", 64))
	ctx := context.Background()
	var a Attachment
	pngHead := []byte("\x89PNG\r\n\x1a\n0000IHDR")
	if e := api.Do(ctx, "POST", "/connector/attachments", map[string]any{"name": "../photo.png", "data": pngHead}, &a); e != nil {
		t.Fatal(e)
	}
	if a.Name != "photo.png" || a.MIME != "image/png" || a.Size != len(pngHead) {
		t.Fatalf("stored as %+v", a)
	}
	conv := Conversation{ID: SignalConversationID(r), Service: "Signal", Account: acct, Recipient: r.Chat, Route: r, Name: "Pat"}
	in := Ingress{Conversation: conv, Message: Message{NativeID: "1700000000000/00000000-0000-4000-8000-000000000002", Sender: "Pat",
		Attachments: []Attachment{{ID: a.ID, Name: "lies.exe", MIME: "application/x-msdownload", Size: 1}}}}
	var m Message
	if e := api.Do(ctx, "POST", "/connector/ingest", in, &m); e != nil {
		t.Fatal(e)
	}
	if len(m.Attachments) != 1 || m.Attachments[0] != a {
		t.Fatalf("the stored record should describe the file: %+v", m.Attachments)
	}
	owner := NewClient(srv.URL, strings.Repeat("o", 64))
	if b, e := owner.Download(ctx, a.ID); e != nil || string(b) != string(pngHead) {
		t.Fatalf("download: %q %v", b, e)
	}
	in.Message.NativeID = "1700000000001/00000000-0000-4000-8000-000000000002"
	in.Message.Attachments = []Attachment{{ID: strings.Repeat("0", 64)}}
	if e := api.Do(ctx, "POST", "/connector/ingest", in, nil); e == nil {
		t.Fatal("a message named a file that was never uploaded")
	}
	in.Message.Attachments = make([]Attachment, 9)
	if _, e := s.Ingest(in); e == nil {
		t.Fatal("nine files accepted")
	}
	// Agents cannot upload as a connector.
	if e := NewClient(srv.URL, strings.Repeat("a", 64)).Do(ctx, "POST", "/connector/attachments", map[string]any{"name": "x", "data": pngHead}, nil); e == nil {
		t.Fatal("an agent used the connector upload")
	}
}

func TestSeedHasPictures(t *testing.T) {
	s := testStore(t)
	if e := s.Seed(); e != nil {
		t.Fatal(e)
	}
	th, e := s.Thread("owner", "demo-telegram-robot")
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, m := range th.Messages {
		for _, a := range m.Attachments {
			found = found || a.MIME == "image/png"
		}
	}
	if !found {
		t.Fatal("no picture in the seeded robot chat")
	}
	if e := s.Seed(); e != nil {
		t.Fatal("seeding twice:", e)
	}
}

func TestConnectorCorrectsOnlyTheTextOfItsOwnMessage(t *testing.T) {
	s := testStore(t)
	acct := "00000000-0000-4000-8000-000000000001"
	r := Route{Provider: "signal", Account: acct, Chat: "user/00000000-0000-4000-8000-000000000002"}
	if e := s.Heartbeat(Connector{Provider: "signal", Account: acct, Worker: "w", AllChats: true}); e != nil {
		t.Fatal(e)
	}
	conv := Conversation{ID: SignalConversationID(r), Service: "Signal", Account: acct, Recipient: r.Chat, Route: r, Name: "Pat"}
	in := Ingress{Conversation: conv, Message: Message{NativeID: "1700000000000/00000000-0000-4000-8000-000000000002", Sender: "Pat", Body: "4Hello there"}}
	first, e := s.Ingest(in)
	if e != nil {
		t.Fatal(e)
	}
	// A plain replay never changes the text.
	in.Message.Body = "Hello there"
	if m, _ := s.Ingest(in); m.Body != "4Hello there" {
		t.Fatal("replay changed content")
	}
	in.Correct = true
	m, e := s.Ingest(in)
	if e != nil || m.Body != "Hello there" || m.ID != first.ID {
		t.Fatalf("correction: %+v %v", m, e)
	}
	if hits, _ := s.Search("owner", "Hello", "", 5); len(hits) != 1 || hits[0].Message.Body != "Hello there" {
		t.Fatalf("search not updated: %+v", hits)
	}
	in.Message.Outgoing = true
	if _, e = s.Ingest(in); e == nil {
		t.Fatal("a correction changed direction")
	}
	in.Message.Outgoing, in.Message.NativeID = false, "1700000000009/00000000-0000-4000-8000-000000000002"
	if _, e = s.Ingest(in); e == nil {
		t.Fatal("a correction imported a new message")
	}
}
