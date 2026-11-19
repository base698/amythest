package taskreview

import (
	"context"
	"encoding/json"
	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/tasks"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReviewWritesAllFindingsInSameNoteAndRejectsStaleOrInvalid(t *testing.T) {
	for _, scenario := range []string{"saved", "concurrent-edit", "duplicate-id", "missing-result", "stale-start"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("HOME", t.TempDir())
			original := "# Work\n- [ ] First\n- [ ] Second\n"
			body := original
			version := strings.Repeat("a", 64)
			writes := 0
			var mu sync.Mutex
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method == "GET" {
					json.NewEncoder(w).Encode(apiclient.Note{Slug: "work", Path: "work.md", Markdown: body, Version: version})
					return
				}
				var in struct{ Markdown, ExpectedVersion string }
				json.NewDecoder(r.Body).Decode(&in)
				if in.ExpectedVersion != version {
					http.Error(w, "changed", 409)
					return
				}
				if r.Header.Get("X-CSRF-Token") != "test-csrf" {
					http.Error(w, "csrf", 400)
					return
				}
				writes++
				body = in.Markdown
				json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			}))
			defer srv.Close()
			session := filepath.Join(t.TempDir(), "session.json")
			b, _ := json.Marshal(map[string]any{"base": srv.URL + "/kanban", "cookie": "test=1", "csrf": "test-csrf", "exp": time.Now().Add(time.Hour).Unix()})
			os.WriteFile(session, b, 0600)
			c := apiclient.New(apiclient.Config{Endpoint: srv.URL, SessionFile: session, Timeout: time.Second})
			selected, _, _ := tasks.ParseFile("work", "work.md", []byte(body))
			for i := range selected {
				selected[i].Version = version
			}
			if scenario == "stale-start" {
				selected[0].Version = "old"
			}
			called := false
			r, e := Review(context.Background(), c, selected, "claude", "", func(ctx context.Context, agent, prompt string) ([]byte, error) {
				called = true
				if !strings.Contains(prompt, "First") || !strings.Contains(prompt, "Do not edit") {
					t.Error("missing bounded context")
				}
				if scenario == "concurrent-edit" {
					mu.Lock()
					body = "User edit\n" + body
					version = strings.Repeat("b", 64)
					mu.Unlock()
				}
				if scenario == "missing-result" {
					return []byte(`{"findings":[]}`), nil
				}
				id := 1
				if scenario == "duplicate-id" {
					id = 0
				}
				raw, _ := json.Marshal(Result{Findings: []Finding{{ID: 0, Summary: "Evidence for first", Sources: []string{"https://example.test/pr/123"}}, {ID: id, Summary: "No evidence for second"}}})
				return raw, nil
			})
			if scenario == "saved" {
				if e != nil || r.Saved != 2 || writes != 1 || !strings.Contains(body, "First\n  > <!-- amy-context -->") || !strings.Contains(body, "Second\n  > <!-- amy-context -->") || strings.Contains(body, "[x]") {
					t.Fatalf("save: %v %+v %s", e, r, body)
				}
			} else if scenario == "concurrent-edit" {
				if e != nil || len(r.Errors) != 1 || writes != 0 || !strings.HasPrefix(body, "User edit") {
					t.Fatal("overwrote concurrent edit", e, r)
				}
			} else {
				if e == nil || writes != 0 {
					t.Fatal("invalid response wrote notes", e, r)
				}
				if scenario == "stale-start" && called {
					t.Fatal("launched agent for stale selection")
				}
			}
		})
	}
}
