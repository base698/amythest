package messages

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPAuthorizationAndSharedReplyFlow(t *testing.T) {
	s := testStore(t)
	m := incoming(t, s, "chat", "personal", true)
	owner, agent := strings.Repeat("o", 64), strings.Repeat("a", 64)
	g := &Gateway{Store: s, OwnerToken: owner, AgentToken: agent}
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()
	resp, e := http.Get(srv.URL + "/items")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unauthenticated read: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/items", nil)
	req.Header.Set("Authorization", "Bearer "+owner)
	req.Header.Set("Origin", "https://other.example")
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("browser origin was accepted")
	}
	c := NewClient(srv.URL, agent)
	ctx := context.Background()
	items, e := c.Items(ctx, true)
	if e != nil || len(items) != 0 {
		t.Fatal("ungranted agent saw messages")
	}
	_, e = c.Attach(ctx, "leak.txt", []byte("should not upload"))
	status(t, e, 403)
	own := NewClient(srv.URL, owner)
	e = own.Do(ctx, "POST", "/grants", Grant{Conversation: m.Conversation, Expires: time.Now().Add(time.Hour)}, nil)
	if e != nil {
		t.Fatal(e)
	}
	d, e := c.Draft(ctx, DraftInput{RequestID: "agent-http", ReplyTo: m.ID, Body: "Proposed reply"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Send(ctx, d.ID)
	status(t, e, 403)
	got, e := own.Send(ctx, d.ID)
	if e != nil || got.State != "simulated" {
		t.Fatalf("owner review failed: %+v %v", got, e)
	}
	req, _ = http.NewRequest("POST", srv.URL+"/send", strings.NewReader(`{"draft":"`+d.ID+`","recipient":"someone else"}`))
	req.Header.Set("Authorization", "Bearer "+owner)
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("send accepted a destination override: %d %s", resp.StatusCode, b)
	}
}
