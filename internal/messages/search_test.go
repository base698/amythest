package messages

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func say(t *testing.T, s *Store, conversation, native, sender, body string, at time.Time) Message {
	t.Helper()
	m, e := s.Ingest(Ingress{
		Conversation: Conversation{ID: conversation, Service: "Telegram", Account: "personal", Name: "Chat " + conversation, Recipient: "r-" + conversation, Demo: true},
		Message:      Message{NativeID: native, Sender: sender, Body: body, At: at},
	})
	if e != nil {
		t.Fatal(e)
	}
	return m
}

func ids(hits []Hit) []string {
	out := []string{}
	for _, h := range hits {
		out = append(out, h.Message.ID)
	}
	return out
}

func TestSearchMatchesBodySenderAndPrefixes(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	deploy := say(t, s, "eng", "1", "Dana", "deploy is blocked on the migration", at)
	say(t, s, "eng", "2", "Sam", "lunch at noon?", at.Add(time.Minute))
	review := say(t, s, "ops", "1", "Migrator Bot", "nightly report ready", at.Add(2*time.Minute))

	hits, e := s.Search("owner", "deploy migration", "", 0)
	if e != nil || len(hits) != 1 || hits[0].Message.ID != deploy.ID {
		t.Fatalf("two-word search: %v %v", ids(hits), e)
	}
	if hits[0].Conversation.Name != "Chat eng" || !strings.Contains(hits[0].Snippet, "migration") {
		t.Fatalf("hit lacks conversation or snippet: %+v", hits[0])
	}
	// The last word is a prefix, and senders are searched too.
	hits, e = s.Search("owner", "migrat", "", 0)
	if e != nil || len(hits) != 2 {
		t.Fatalf("prefix search: %v %v", ids(hits), e)
	}
	hits, e = s.Search("owner", "migrat", "ops", 0)
	if e != nil || len(hits) != 1 || hits[0].Message.ID != review.ID {
		t.Fatalf("conversation filter: %v %v", ids(hits), e)
	}
	// FTS syntax in what people type is treated as text, never an error.
	for _, q := range []string{`"unbalanced`, "NEAR(deploy", "a OR b -c", "*", "col:umn", `deploy"`} {
		if _, e := s.Search("owner", q, "", 0); e != nil {
			t.Fatalf("query %q: %v", q, e)
		}
	}
	_, e = s.Search("owner", "   ", "", 0)
	status(t, e, 400)
}

func TestSearchExcludesTrashAndLimitsAgentsToGrants(t *testing.T) {
	s := testStore(t)
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	granted := say(t, s, "granted", "1", "Dana", "release notes draft", now)
	say(t, s, "private", "1", "Sam", "release salary details", now)
	say(t, s, "spam", "1", "Spammer", "release your prize", now)
	if e := s.SetTrashed("spam", true); e != nil {
		t.Fatal(e)
	}
	hits, e := s.Search("owner", "release", "", 0)
	if e != nil || len(hits) != 2 {
		t.Fatalf("owner should see two untrashed hits: %v %v", ids(hits), e)
	}
	hits, e = s.Search("agent", "release", "", 0)
	if e != nil || len(hits) != 0 {
		t.Fatalf("ungranted agent saw %v %v", ids(hits), e)
	}
	_, e = s.Search("agent", "release", "private", 0)
	status(t, e, 403)
	if e = s.Grant(Grant{Conversation: "granted", Expires: now.Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	hits, e = s.Search("agent", "release", "", 0)
	if e != nil || len(hits) != 1 || hits[0].Message.ID != granted.ID {
		t.Fatalf("granted agent: %v %v", ids(hits), e)
	}
	now = now.Add(2 * time.Hour)
	hits, e = s.Search("agent", "release", "", 0)
	if e != nil || len(hits) != 0 {
		t.Fatalf("expired grant still searchable: %v %v", ids(hits), e)
	}
	_, e = s.Search("connector", "release", "", 0)
	status(t, e, 403)
}

func TestSearchIndexFollowsUpdatesAndBackfills(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	m := say(t, s, "eng", "1", "Dana", "original wording", time.Now())
	// Rewrite the payload directly, as delivery and attention updates do.
	m.Body = "replacement wording"
	if _, e = s.db.Exec("UPDATE messages SET payload=? WHERE id=?", encode(m), m.ID); e != nil {
		t.Fatal(e)
	}
	if hits, _ := s.Search("owner", "original", "", 0); len(hits) != 0 {
		t.Fatal("stale body still indexed")
	}
	if hits, _ := s.Search("owner", "replacement", "", 0); len(hits) != 1 {
		t.Fatal("updated body not indexed")
	}
	// A database from before search existed has messages but no index rows.
	if _, e = s.db.Exec("DROP TABLE messages_fts; DROP TRIGGER messages_fts_insert; DROP TRIGGER messages_fts_update; DROP TRIGGER messages_fts_delete"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if s, e = Open(dir); e != nil {
		t.Fatal(e)
	}
	if hits, _ := s.Search("owner", "replacement", "", 0); len(hits) != 1 {
		t.Fatal("existing messages were not backfilled")
	}
	s.Close()
	if s, e = Open(dir); e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if hits, _ := s.Search("owner", "replacement", "", 0); len(hits) != 1 {
		t.Fatalf("reopening duplicated the index: %d hits", len(hits))
	}
}

func TestThreadPageAnchorsAndScrolls(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	var msgs []Message
	for i := range 10 {
		msgs = append(msgs, say(t, s, "eng", fmt.Sprint(i), "Dana", fmt.Sprint("line ", i), at.Add(time.Duration(i)*time.Minute)))
	}
	page := func(p Page) []string {
		t.Helper()
		th, e := s.ThreadPage("owner", "eng", p)
		if e != nil {
			t.Fatal(e)
		}
		out := []string{}
		for _, m := range th.Messages {
			out = append(out, strings.TrimPrefix(m.Body, "line "))
		}
		return append(out, fmt.Sprint(th.HasBefore, th.HasAfter))
	}
	cases := []struct {
		p    Page
		want string
	}{
		{Page{Limit: 3}, "7 8 9 true false"},
		{Page{Around: msgs[5].ID, Limit: 3}, "4 5 6 true true"},
		{Page{Around: msgs[0].ID, Limit: 3}, "0 1 2 false true"},
		{Page{Around: msgs[9].ID, Limit: 4}, "6 7 8 9 true false"},
		{Page{Before: msgs[3].ID, Limit: 5}, "0 1 2 false true"},
		{Page{After: msgs[7].ID, Limit: 5}, "8 9 true false"},
		{Page{After: msgs[9].ID}, "true false"},
	}
	for _, c := range cases {
		if got := strings.Join(page(c.p), " "); got != c.want {
			t.Errorf("%+v: got %q want %q", c.p, got, c.want)
		}
	}
	other := say(t, s, "ops", "1", "Sam", "elsewhere", at)
	_, e := s.ThreadPage("owner", "eng", Page{Around: other.ID})
	status(t, e, 404)
	_, e = s.ThreadPage("owner", "eng", Page{Around: msgs[1].ID, After: msgs[2].ID})
	status(t, e, 400)
	_, e = s.ThreadPage("agent", "eng", Page{})
	status(t, e, 403)
}

func TestHTTPSearchAndThreadPage(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	first := say(t, s, "eng", "1", "Dana", "deploy blocked", at)
	say(t, s, "eng", "2", "Sam", "unblocked now", at.Add(time.Minute))
	owner, agent := strings.Repeat("o", 64), strings.Repeat("a", 64)
	srv := httptest.NewServer((&Gateway{Store: s, OwnerToken: owner, AgentToken: agent}).Handler())
	defer srv.Close()
	ctx := context.Background()
	c := NewClient(srv.URL, owner)
	hits, e := c.Search(ctx, "deploy", "", 5)
	if e != nil || len(hits) != 1 || hits[0].Message.ID != first.ID {
		t.Fatalf("search over HTTP: %v %v", ids(hits), e)
	}
	th, e := c.ThreadPage(ctx, "eng", Page{After: first.ID})
	if e != nil || len(th.Messages) != 1 || !th.HasBefore || th.HasAfter {
		t.Fatalf("page over HTTP: %+v %v", th, e)
	}
	full, e := c.Thread(ctx, "eng")
	if e != nil || len(full.Messages) != 2 || full.HasBefore || full.HasAfter {
		t.Fatalf("unpaged thread changed: %+v %v", full, e)
	}
	// Agents may search (scoped to grants); connectors may not.
	hits, e = NewClient(srv.URL, agent).Search(ctx, "deploy", "", 0)
	if e != nil || len(hits) != 0 {
		t.Fatalf("ungranted agent over HTTP: %v %v", ids(hits), e)
	}
	e = c.Do(ctx, "GET", "/search?q=x&limit=nope", nil, nil)
	status(t, e, 400)
}
