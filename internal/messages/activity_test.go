package messages

import (
	"testing"
	"time"
)

func TestConversationsSortByLatestInteraction(t *testing.T) {
	s := testStore(t)
	base := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	older := Conversation{ID: "older", Service: "Telegram", Account: "test", Recipient: "one", Demo: true}
	newer := Conversation{ID: "newer", Service: "Telegram", Account: "test", Recipient: "two", Demo: true}
	add := func(c Conversation, id, body string, at time.Time, outgoing bool) Message {
		t.Helper()
		m, err := s.Ingest(Ingress{Conversation: c, Message: Message{NativeID: id, Body: body, At: at, Outgoing: outgoing}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	question := add(older, "question", "Can you review?", base, false)
	add(newer, "update", "Hello", base.Add(time.Minute), false)
	for _, all := range []bool{false, true} {
		items, err := s.Items("owner", all)
		if err != nil || len(items) != 2 || items[0].Conversation.ID != newer.ID {
			t.Fatal("category overrode recency", items, err)
		}
	}
	reply := add(older, "reply", "I replied in the native app", base.Add(2*time.Minute), true)
	for _, all := range []bool{false, true} {
		items, err := s.Items("owner", all)
		if err != nil || len(items) != 2 || items[0].Conversation.ID != older.ID || !items[0].LastActivity.Equal(reply.At) {
			t.Fatal("reply did not update recency", items, err)
		}
		want := question.ID
		if all {
			want = reply.ID
		}
		if items[0].Message.ID != want {
			t.Fatal("wrong focus/preview message", items[0])
		}
	}
	// A delayed historical event must not move the conversation above a newer one.
	add(newer, "late", "Please answer?", base.Add(-time.Hour), false)
	items, _ := s.Items("owner", true)
	if items[0].Conversation.ID != older.ID {
		t.Fatal("arrival order replaced provider time")
	}
	onlySent := Conversation{ID: "sent-only", Service: "Telegram", Account: "test", Recipient: "three", Demo: true}
	add(onlySent, "sent", "Hello?", base.Add(3*time.Minute), true)
	items, _ = s.Items("owner", false)
	if len(items) != 2 {
		t.Fatal("outgoing-only conversation created attention")
	}
	items, _ = s.Items("owner", true)
	if len(items) != 3 || items[0].Conversation.ID != onlySent.ID || !items[0].Message.Outgoing {
		t.Fatal("outgoing conversation missing in All")
	}
}

func TestOutgoingSyncBeforeReceiptDoesNotDuplicateOrRedate(t *testing.T) {
	s := testStore(t)
	parent, connector := liveFixture(t, s)
	d, err := s.Draft("owner", DraftInput{RequestID: "sync-race", ReplyTo: parent.ID, Body: "Reply"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Send("owner", d.ID); err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(connector.Account, connector.Worker)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	observed, err := s.Ingest(Ingress{Conversation: job.Draft.Target, Message: Message{NativeID: "99", Body: d.Body, At: s.now().Add(-time.Second), Outgoing: true}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Complete(Receipt{Account: connector.Account, Draft: d.ID, Attempt: job.Draft.Attempt, NativeID: "99", State: "sent"})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := s.Thread("owner", parent.Conversation)
	if err != nil || len(thread.Messages) != 2 {
		t.Fatal("duplicate outgoing message", thread, err)
	}
	for _, m := range thread.Messages {
		if m.Outgoing && (m.ID != observed.ID || !m.At.Equal(observed.At)) {
			t.Fatal("receipt changed observed identity or time")
		}
	}
}
