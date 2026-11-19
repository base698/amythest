package messages

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJumpWaitsForTheNextViewWhenNoneIsOpen(t *testing.T) {
	s := testStore(t)
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	m := say(t, s, "eng", "1", "Dana", "deploy blocked", now)
	g := &Gateway{Store: s}
	res, e := g.Jump(Jump{Message: m.ID})
	if e != nil || len(res.Listeners) != 0 || res.Jump.Conversation != "eng" {
		t.Fatalf("jump: %+v %v", res, e)
	}
	view := Listener{Machine: "here", Pane: "t1"}
	got := g.events().poll(context.Background(), view, -1, 0)
	if len(got.Events) != 1 || got.Events[0].Jump.Message != m.ID || got.Seq != res.Seq {
		t.Fatalf("opening view missed the waiting jump: %+v", got)
	}
	if again := g.events().poll(context.Background(), view, -1, 0); len(again.Events) != 0 {
		t.Fatal("a delivered jump was handed out twice")
	}
	// A jump nobody took goes stale rather than surprising a view opened later.
	if _, e = g.Jump(Jump{Conversation: "eng"}); e != nil {
		t.Fatal(e)
	}
	now = now.Add(pendingJump + time.Second)
	if late := g.events().poll(context.Background(), Listener{Pane: "t2"}, -1, 0); len(late.Events) != 0 {
		t.Fatalf("stale jump delivered: %+v", late)
	}
}

func TestJumpReachesAWaitingViewAndNamesItsPane(t *testing.T) {
	s := testStore(t)
	m := say(t, s, "eng", "1", "Dana", "deploy blocked", time.Now())
	g := &Gateway{Store: s}
	view := Listener{Machine: "here", Pane: "w1:p3"}
	start := g.events().poll(context.Background(), view, -1, 0)
	got := make(chan Events, 1)
	go func() { got <- g.events().poll(context.Background(), view, start.Seq, 5*time.Second) }()
	// Publish only once the view is waiting.
	deadline := time.Now().Add(2 * time.Second)
	for {
		g.events().mu.Lock()
		waiting := g.events().waiting[view]
		g.events().mu.Unlock()
		if waiting > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	res, e := g.Jump(Jump{Message: m.ID})
	if e != nil || len(res.Listeners) != 1 || res.Listeners[0] != view {
		t.Fatalf("jump should name the waiting view: %+v %v", res, e)
	}
	select {
	case ev := <-got:
		if len(ev.Events) != 1 || ev.Events[0].Jump.Conversation != "eng" || ev.Seq != res.Seq {
			t.Fatalf("waiting view got %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waiting view was not woken")
	}
	// A view that stopped polling long ago is no longer listed.
	g.events().now = func() time.Time { return time.Now().Add(listenerLife + time.Second) }
	if res, _ = g.Jump(Jump{Section: "drafts"}); len(res.Listeners) != 0 {
		t.Fatalf("closed view still listed: %+v", res.Listeners)
	}
}

func TestJumpValidatesItsTarget(t *testing.T) {
	s := testStore(t)
	m := say(t, s, "eng", "1", "Dana", "deploy blocked", time.Now())
	say(t, s, "ops", "1", "Sam", "elsewhere", time.Now())
	g := &Gateway{Store: s}
	_, e := g.Jump(Jump{})
	status(t, e, 400)
	_, e = g.Jump(Jump{Section: "settings"})
	status(t, e, 400)
	_, e = g.Jump(Jump{Message: "missing"})
	status(t, e, 404)
	_, e = g.Jump(Jump{Conversation: "missing"})
	status(t, e, 404)
	_, e = g.Jump(Jump{Conversation: "ops", Message: m.ID})
	status(t, e, 400)
}

func TestHTTPJumpAndEventsAreOwnerOnly(t *testing.T) {
	s := testStore(t)
	m := say(t, s, "eng", "1", "Dana", "deploy blocked", time.Now())
	owner, agent := strings.Repeat("o", 64), strings.Repeat("a", 64)
	srv := httptest.NewServer((&Gateway{Store: s, OwnerToken: owner, AgentToken: agent}).Handler())
	defer srv.Close()
	ctx := context.Background()
	c := NewClient(srv.URL, owner)
	got, e := c.Message(ctx, m.ID)
	if e != nil || got.Conversation != "eng" {
		t.Fatalf("message lookup: %+v %v", got, e)
	}
	res, e := c.Jump(ctx, Jump{Message: m.ID})
	if e != nil || res.Jump.Conversation != "eng" {
		t.Fatalf("jump over HTTP: %+v %v", res, e)
	}
	ev, e := c.Events(ctx, -1, Listener{Machine: "here", Pane: "t1"}, 0)
	if e != nil || len(ev.Events) != 1 {
		t.Fatalf("events over HTTP: %+v %v", ev, e)
	}
	a := NewClient(srv.URL, agent)
	_, e = a.Jump(ctx, Jump{Message: m.ID})
	status(t, e, 403)
	_, e = a.Events(ctx, -1, Listener{}, 0)
	status(t, e, 403)
	_, e = a.Message(ctx, m.ID)
	status(t, e, 403) // no grant for this conversation
	e = c.Do(ctx, "GET", "/events?since=-4", nil, nil)
	status(t, e, 400)
}
