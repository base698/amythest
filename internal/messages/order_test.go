package messages

import (
	"fmt"
	"testing"
	"time"
)

// A database written before the fixed-width layout stored RFC3339Nano, where
// "09:00:00Z" sorts after "09:00:00.5Z" as text. Reopening rewrites it.
func TestOldTimestampsAreRewrittenAndOrderByTime(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	half := say(t, s, "eng", "1", "Dana", "half past", at.Add(500*time.Millisecond))
	whole := say(t, s, "eng", "2", "Sam", "on the second", at)
	later := say(t, s, "eng", "3", "Sam", "a second later", at.Add(time.Second))
	for _, m := range []Message{half, whole, later} {
		if _, e = s.db.Exec("UPDATE messages SET occurred=? WHERE id=?", m.At.Format(time.RFC3339Nano), m.ID); e != nil {
			t.Fatal(e)
		}
	}
	// Also a database from before the outgoing column.
	if _, e = s.db.Exec("DROP INDEX message_latest; ALTER TABLE messages DROP COLUMN outgoing"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if s, e = Open(dir); e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var odd int
	s.db.QueryRow("SELECT count(*) FROM messages WHERE length(occurred) != ?", len(occurredLayout)).Scan(&odd)
	if odd != 0 {
		t.Fatalf("%d timestamps left in the old layout", odd)
	}
	want := []string{whole.ID, half.ID, later.ID}
	th, e := s.Thread("owner", "eng")
	if e != nil {
		t.Fatal(e)
	}
	page, e := s.ThreadPage("owner", "eng", Page{Limit: 3})
	if e != nil {
		t.Fatal(e)
	}
	for i, id := range want {
		if th.Messages[i].ID != id || page.Messages[i].ID != id {
			t.Fatalf("order at %d: thread %s page %s want %s", i, th.Messages[i].Body, page.Messages[i].Body, id)
		}
	}
	before, e := s.ThreadPage("owner", "eng", Page{Before: later.ID, Limit: 1})
	if e != nil || len(before.Messages) != 1 || before.Messages[0].ID != half.ID || !before.HasBefore {
		t.Fatalf("page before the last: %+v %v", before, e)
	}
	items, e := s.Items("owner", true)
	if e != nil || len(items) != 1 || items[0].Message.ID != later.ID || !items[0].LastActivity.Equal(later.At) {
		t.Fatalf("items: %+v %v", items, e)
	}
}

// Equal times keep arrival order, as the queue did before it used indexes.
func TestQueueLatestAndFocusUseTheIndex(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	say(t, s, "a", "1", "Dana", "first", at)
	tied := say(t, s, "a", "2", "Dana", "same instant, arrived later", at)
	if _, e := s.Ingest(Ingress{Conversation: Conversation{ID: "a", Service: "Telegram", Account: "personal", Name: "Chat a", Recipient: "r-a", Demo: true}, Message: Message{NativeID: "3", Body: "my answer", Outgoing: true, At: at.Add(time.Minute)}}); e != nil {
		t.Fatal(e)
	}
	focus, e := s.Items("owner", false)
	if e != nil || len(focus) != 1 || focus[0].Message.ID != tied.ID || !focus[0].LastActivity.Equal(at.Add(time.Minute)) {
		t.Fatalf("focus keeps the latest incoming message and the outgoing activity time: %+v %v", focus, e)
	}
	all, e := s.Items("owner", true)
	if e != nil || len(all) != 1 || all[0].Message.Body != "my answer" {
		t.Fatalf("all shows the latest activity: %+v %v", all, e)
	}
	var plan string
	rows, _ := s.db.Query("EXPLAIN QUERY PLAN SELECT conversation, max(" + orderKey + ") FROM messages WHERE outgoing = 0 GROUP BY conversation")
	for rows.Next() {
		var a, b, c int
		var d string
		rows.Scan(&a, &b, &c, &d)
		plan += d
	}
	rows.Close()
	if plan != "SCAN messages USING COVERING INDEX message_latest" && plan != "SCAN messages USING INDEX message_latest" {
		t.Fatalf("latest-incoming query plan: %s", plan)
	}
}

func BenchmarkItems(b *testing.B) {
	s, e := Open(b.TempDir())
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for c := range 200 {
		for i := range 100 {
			_, e := s.Ingest(Ingress{Conversation: Conversation{ID: fmt.Sprint("c", c), Service: "Telegram", Account: "a", Name: "n", Recipient: "r", Demo: true},
				Message: Message{NativeID: fmt.Sprint(i), Sender: "x", Body: "a message body of ordinary length for a chat", At: at.Add(time.Duration(c*100+i) * time.Second)}})
			if e != nil {
				b.Fatal(e)
			}
		}
	}
	b.ResetTimer()
	for range b.N {
		if _, e := s.Items("owner", false); e != nil {
			b.Fatal(e)
		}
	}
}
