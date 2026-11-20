package server

import (
	"encoding/json"
	"github.com/base698/amythest/internal/config"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkPrivateRouteCapabilities(t *testing.T) {
	t.Setenv("AMYTHEST_USERNAME", "owner")
	t.Setenv("AMYTHEST_PASSWORD", "correct horse")
	t.Setenv("AMYTHEST_SESSION_SECRET", "01234567890123456789012345678901")
	t.Setenv("KANBAN_USERNAME", "")
	t.Setenv("KANBAN_PASSWORD", "")
	t.Setenv("KANBAN_SESSION_SECRET", "")
	s := hardenedServer(t, func(c *config.Config) {
		c.AuthMode = "private"
		c.AuthPublicURL = "https://notes.example.com"
		c.Work.Enabled = true
	})
	collector, cred, e := s.kanbanAuth.CreateCredential("laptop", "collector", time.Now(), time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	reader, _, _ := s.kanbanAuth.CreateCredential("reader", "reader", time.Now(), time.Hour)
	req := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, test := range []struct {
		method, path, token, body string
		status                    int
	}{{"GET", "/api/work/snapshot?after=invalid", reader, "", 422}, {"GET", "/api/work/snapshot?after=2026-11-13T00:00:00Z&before=2026-11-17T00:00:00Z", reader, "", 200}, {"POST", "/api/work/dismiss", collector, `{}`, 403}, {"POST", "/api/work/dismiss", reader, `{}`, 403}, {"GET", "/api/work/snapshot?run_id=missing", reader, "", 200}, {"POST", "/api/work/project", collector, `{}`, 403}, {"POST", "/api/work/project", reader, `{}`, 403}, {"POST", "/api/work/streams", collector, `{"generation":"g"}`, 200}, {"GET", "/api/work/snapshot", collector, "", 403}, {"GET", "/api/note", collector, "", 403}, {"GET", "/mcp", collector, "", 403}, {"POST", "/api/work/streams", reader, `{"generation":"g"}`, 403}, {"POST", "/api/work/review", reader, `{}`, 403}, {"GET", "/api/work/snapshot", reader, "", 200}, {"GET", "/api/work/milestones", reader, "", 200}, {"POST", "/api/work/milestones", reader, `{"project":"p","name":"n"}`, 403}, {"POST", "/api/work/milestones/end", collector, `{}`, 403}, {"GET", "/api/work/milestones/collect?id=missing", reader, "", 404}, {"POST", "/api/work/milestones/update", reader, `{"id":"x"}`, 403}, {"GET", "/api/work/milestones/links?id=missing", reader, "", 200}, {"POST", "/api/work/milestones/links", reader, `{}`, 403}, {"DELETE", "/api/work/milestones/links", collector, `{}`, 403}, {"POST", "/api/work/events", collector, `{"generation":"g","events":[{"seq":1,"at":"2026-01-01T00:00:00Z","kind":"slot.seen","pane":"p","session_id":"s"}]}`, 200}} {
		w := req(test.method, test.path, test.token, test.body)
		if w.Code != test.status {
			t.Fatalf("%s %s: %d %s", test.method, test.path, w.Code, w.Body.String())
		}
	}
	token, next, e := s.kanbanAuth.ReplaceCredential(cred.ID, time.Now())
	if e != nil || next.ClientID != cred.ID {
		t.Fatal(e)
	}
	s.kanbanAuth.Revoke(cred.ID, time.Now())
	w := req("POST", "/api/work/streams", token, `{"generation":"g"}`)
	var ack struct {
		Acked int `json:"acked"`
	}
	json.Unmarshal(w.Body.Bytes(), &ack)
	if w.Code != 200 || ack.Acked != 1 {
		t.Fatal("rotation lost stream", w.Code, w.Body.String())
	}
	if w = req("POST", "/api/work/streams", collector, `{"generation":"g"}`); w.Code != 401 {
		t.Fatal("revoked collector allowed", w.Code)
	}
}
