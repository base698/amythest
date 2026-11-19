package messageui

import (
	"context"
	"encoding/json"
	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/messages"
	"github.com/base698/amythest/internal/tasks"
	tea "github.com/charmbracelet/bubbletea"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAttachSnapshotUnderTaskWithConflictAndReplayGuard(t *testing.T) {
	body := "- [ ] Keep this task\n- [ ] Other\n"
	version := strings.Repeat("a", 64)
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(apiclient.Note{Slug: "note", Markdown: body, Version: version})
			return
		}
		var in struct{ Markdown, ExpectedVersion string }
		json.NewDecoder(r.Body).Decode(&in)
		if r.Header.Get("X-CSRF-Token") != "csrf" || in.ExpectedVersion != version {
			http.Error(w, "conflict", 409)
			return
		}
		body = in.Markdown
		writes++
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()
	session := filepath.Join(t.TempDir(), "session.json")
	raw, _ := json.Marshal(map[string]any{"base": srv.URL + "/kanban", "cookie": "test=1", "csrf": "csrf", "exp": time.Now().Add(time.Hour).Unix()})
	os.WriteFile(session, raw, 0600)
	client := apiclient.New(apiclient.Config{Endpoint: srv.URL, SessionFile: session, Timeout: time.Second})
	task := tasks.Task{Slug: "note", Line: 1, Text: "Keep this task", Status: tasks.StatusOpen, Version: version}
	target := noteTarget{slug: "note", task: &task}
	it := messages.Item{Conversation: messages.Conversation{Name: "Morgan", Service: "Slack"}, Message: messages.Message{ID: "message-1", Body: "Found evidence\n- [x] this must remain quoted", At: time.Now()}}
	if e := attachMessage(context.Background(), client, it, target); e != nil {
		t.Fatal(e)
	}
	parsed, _, _ := tasks.ParseFile("note", "note.md", []byte(body))
	if writes != 1 || len(parsed) != 2 || len(parsed[0].Context) == 0 || len(parsed[1].Context) != 0 {
		t.Fatal("wrong annotation", body)
	}
	if e := attachMessage(context.Background(), client, it, target); e == nil || writes != 1 {
		t.Fatal("duplicate appended")
	}
	it.Message.ID = "new-message"
	task.Version = "stale"
	if e := attachMessage(context.Background(), client, it, target); e == nil || writes != 1 {
		t.Fatal("stale task edited")
	}
}
func TestLinkSourcePinnedAndTypeFilter(t *testing.T) {
	m, _, _ := harness(t)
	m.opts.Notes = apiclient.New(apiclient.Config{})
	original := *m.current()
	m.startLink()
	m.move(1)
	if m.link.message.Message.ID != original.Message.ID {
		t.Fatal("link source moved with list")
	}
	m.link.targets = []noteTarget{{kind: "task", label: "Review release", slug: "one"}, {kind: "note", label: "Release notes", slug: "two"}, {kind: "note", label: "Other", slug: "three"}}
	m.link.input.SetValue("release")
	if len(m.linkMatches()) != 2 {
		t.Fatal("search failed")
	}
	m.linkKey(tea.KeyMsg{Type: tea.KeyTab})
	if len(m.linkMatches()) != 1 || m.linkMatches()[0].kind != "task" {
		t.Fatal("task filter failed")
	}
	m.linkKey(tea.KeyMsg{Type: tea.KeyTab})
	if len(m.linkMatches()) != 1 || m.linkMatches()[0].kind != "note" {
		t.Fatal("note filter failed")
	}
}
func TestServicePickerPreservesFocusAndContactIsPinned(t *testing.T) {
	m, _, _ := harness(t)
	original := *m.current()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m.move(1)
	if m.link.message.Conversation.ID != original.Conversation.ID {
		t.Fatal("contact moved")
	}
	m.utilityKey(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m.serviceCursor = 6
	m.utilityKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.provider != "Slack" || m.all {
		t.Fatal("service picker lost focus mode")
	}
}
