package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/source"
	"github.com/base698/amythest/internal/source/jira"
)

func stubJiraView(t *testing.T) *sourceView {
	t.Helper()
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	src := jira.New(jira.Config{Stub: true}, t.TempDir()+"/no-env")
	v := newSourceView(client, src)
	cmd := v.Init()
	msg := cmd()
	items, ok := msg.(sourceItemsMsg)
	if !ok {
		t.Fatalf("init msg = %#v", msg)
	}
	v.Update(items)
	return v
}

func TestSourceViewListsStubIssues(t *testing.T) {
	v := stubJiraView(t)
	out := v.View(120, 40)
	for _, want := range []string{"DEMO-101", "Fix login redirect loop", "DEMO-198", "Backlog"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestSourceViewCommentFlowMutatesStub(t *testing.T) {
	v := stubJiraView(t)
	v.Update(keyMsg("c"))
	if !v.Capturing() {
		t.Fatal("comment input should capture")
	}
	for _, r := range "on it" {
		v.Update(keyMsg(string(r)))
	}
	_, cmd := v.Update(enterMsg())
	if cmd == nil || !v.Busy() {
		t.Fatal("enter must post the comment")
	}
	msg := cmd()
	commented, ok := msg.(sourceCommentedMsg)
	if !ok || commented.id != "DEMO-101" {
		t.Fatalf("msg = %#v", msg)
	}
	// The reload after the comment shows it in the agent context.
	next, _ := v.Update(commented)
	sv := next.(*sourceView)
	reload := sv.loadCmd()()
	sv.Update(reload)
	subject, body, err := sv.src.AgentContext(*sv.current())
	if err != nil || subject != "DEMO-101" || !strings.Contains(body, "on it") {
		t.Fatalf("agent context after comment: subject=%q err=%v\n%s", subject, err, body)
	}
}

func TestSourceViewPullCreatesCardWithReference(t *testing.T) {
	srv := newFakePullServer(t)
	client := srv.client(t)
	src := jira.New(jira.Config{Stub: true}, t.TempDir()+"/no-env")
	v := newSourceView(client, src)
	v.Update(v.Init()().(sourceItemsMsg))

	_, cmd := v.Update(keyMsg("p"))
	if cmd == nil {
		t.Fatal("p must fetch boards")
	}
	v.Update(cmd().(sourcePullBoardsMsg))
	if !v.pulling || !v.Capturing() {
		t.Fatal("board picker should be open")
	}
	_, cmd = v.Update(enterMsg())
	if cmd == nil {
		t.Fatal("enter must pull")
	}
	created, ok := cmd().(cardCreatedMsg)
	if !ok {
		t.Fatalf("msg = %#v", cmd())
	}
	if created.board != "personal" {
		t.Fatalf("board = %q", created.board)
	}
	if !strings.Contains(srv.lastDescription, "Source: jira:DEMO-101") ||
		!strings.Contains(srv.lastDescription, "/browse/DEMO-101") ||
		!strings.Contains(srv.lastDescription, "session cookie is stale") {
		t.Fatalf("pulled description = %q", srv.lastDescription)
	}
}

func TestSourceViewReadOnlyKeysAndTodayFallback(t *testing.T) {
	// Today view: space/e/D on a jira item flashes read-only.
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	src := jira.New(jira.Config{Stub: true}, t.TempDir()+"/no-env")
	items, err := src.DueItems(nil, time.Now().Format("2006-01-02"), false)
	if err != nil || len(items) == 0 {
		t.Fatalf("stub due items: %v", err)
	}
	tv := newTodayView(client, source.NewRegistry(src))
	tv.Update(todayLoadedMsg{items: []todayItem{{section: "Overdue", item: items[0]}}})
	_, cmd := tv.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if tv.Busy() || cmd == nil {
		t.Fatal("space on a jira item must flash, not mutate")
	}
	if msg, ok := cmd().(flashMsg); !ok || !strings.Contains(msg.text, "press 5") {
		t.Fatalf("flash = %#v", cmd())
	}
}

// fakePullServer is a minimal amythest stand-in for the pull flow.
type fakePullServer struct {
	*httptest.Server
	lastDescription string
}

func newFakePullServer(t *testing.T) *fakePullServer {
	t.Helper()
	fs := &fakePullServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /kanban/api/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "amythest_kanban_session", Value: "tok", Path: "/"})
		json.NewEncoder(w).Encode(map[string]string{"user": "smoke", "csrf": "csrf-token"})
	})
	mux.HandleFunc("GET /kanban/api/boards", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"name": "personal", "displayName": "Personal"}})
	})
	mux.HandleFunc("POST /kanban/api/boards/{board}/cards", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": "n1", "title": "x", "status": "triage"})
	})
	mux.HandleFunc("PUT /kanban/api/boards/{board}/cards/{card}", func(w http.ResponseWriter, r *http.Request) {
		var patch struct {
			Description *string `json:"description"`
		}
		json.NewDecoder(r.Body).Decode(&patch)
		if patch.Description != nil {
			fs.lastDescription = *patch.Description
		}
		json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("card")})
	})
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)
	return fs
}

func (f *fakePullServer) client(t *testing.T) *apiclient.Client {
	t.Helper()
	t.Setenv("KANBAN_USERNAME", "smoke")
	t.Setenv("KANBAN_PASSWORD", "smoketest123")
	return apiclient.New(apiclient.Config{
		Endpoint:    f.URL,
		Timeout:     5 * time.Second,
		SessionFile: filepath.Join(t.TempDir(), "session.json"),
	})
}
