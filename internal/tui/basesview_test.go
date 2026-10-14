package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/bases"
)

func loadedBase() baseDataMsg {
	return baseDataMsg{data: &apiclient.BaseData{
		Name:  "Projects",
		Views: []string{"All", "Active"},
		Data: bases.ViewData{
			View:    "All",
			Columns: []string{"Name", "Status", "Due"},
			Groups: []bases.ViewDataGroup{
				{Name: "active", Rows: [][]string{{"Garden", "active", "2026-09-01"}}, Slugs: []string{"Projects/Garden"}},
				{Name: "done", Rows: [][]string{{"Shed", "done", ""}}, Slugs: []string{"Projects/Shed"}},
			},
		},
	}}
}

func TestBasesViewTableNavigationAndActions(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	v := newBasesView(client)
	v.Update(basesListMsg{names: []string{"Projects"}})
	if !v.loaded {
		t.Fatal("names not loaded")
	}
	_, cmd := v.Update(enterMsg())
	if cmd == nil || !v.Busy() {
		t.Fatal("enter must open the base")
	}
	v.Update(loadedBase())
	out := v.tableView(100, 30)
	for _, want := range []string{"Name", "Status", "active", "Garden", "done", "Shed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
	// Cursor starts on the first data row (headers skipped).
	if row := v.current(); row == nil || row.slug != "Projects/Garden" {
		t.Fatalf("current = %+v", v.current())
	}
	// j skips the "done" group header onto Shed.
	v.Update(keyMsg("j"))
	if row := v.current(); row == nil || row.slug != "Projects/Shed" {
		t.Fatalf("after j = %+v", v.current())
	}
	// v cycles to the next view.
	_, cmd = v.Update(keyMsg("v"))
	if cmd == nil || !v.Busy() {
		t.Fatal("v must reload with the next view")
	}
	v.Update(loadedBase())
	// e fetches the note version, then opens the property prompt.
	_, cmd = v.Update(keyMsg("e"))
	if cmd == nil || !v.Busy() {
		t.Fatal("e must fetch the note version")
	}
	v.Update(basePropEditorMsg{version: strings.Repeat("a", 64)})
	if !v.propping || !v.Capturing() {
		t.Fatal("version fetch must open the property prompt")
	}
	for _, r := range "status=archived" {
		v.Update(keyMsg(string(r)))
	}
	_, cmd = v.Update(enterMsg())
	if cmd == nil || !v.Busy() {
		t.Fatal("enter must save the property")
	}
	// enter on a row opens the note.
	v.busy = false
	_, cmd = v.Update(enterMsg())
	if cmd == nil || !v.Busy() {
		t.Fatal("enter on a row must open its note")
	}
	// esc returns to the base list.
	v.busy = false
	v.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.base != nil {
		t.Fatal("esc must return to the list")
	}
}

func TestBasesViewBusyGatesNavigationAndPropertyEditor(t *testing.T) {
	v := newBasesView(nil)
	v.Update(loadedBase())
	before := v.cursor
	v.busy = true
	v.Update(keyMsg("j"))
	v.Update(keyMsg("e"))
	if v.cursor != before || v.propping {
		t.Fatalf("busy base changed state: cursor=%d want=%d propping=%v", v.cursor, before, v.propping)
	}
}

func TestBasesViewPropertyEditorRejectsChangeAfterOpening(t *testing.T) {
	openedVersion := strings.Repeat("a", 64)
	currentVersion := openedVersion
	postedVersion := ""
	writes := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /kanban/api/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "amythest_kanban_session", Value: "token", Path: "/"})
		_ = json.NewEncoder(w).Encode(map[string]string{"user": "test", "csrf": "csrf-token"})
	})
	mux.HandleFunc("GET /api/note", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"slug": "Projects/Garden", "version": currentVersion})
	})
	mux.HandleFunc("POST /api/notes/property", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ExpectedVersion string `json:"expectedVersion"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		postedVersion = body.ExpectedVersion
		if postedVersion != currentVersion {
			http.Error(w, "note changed; refresh and retry", http.StatusConflict)
			return
		}
		writes++
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	t.Setenv("KANBAN_USERNAME", "test")
	t.Setenv("KANBAN_PASSWORD", "test-password")
	client := apiclient.New(apiclient.Config{Endpoint: server.URL, SessionFile: filepath.Join(t.TempDir(), "session.json")})
	v := newBasesView(client)
	v.Update(loadedBase())

	_, openCmd := v.Update(keyMsg("e"))
	if openCmd == nil || !v.Busy() {
		t.Fatal("opening the property editor must fetch and pin the note version")
	}
	v.Update(openCmd())
	if !v.propping || v.Busy() {
		t.Fatalf("editor did not open after version fetch: propping=%v busy=%v", v.propping, v.Busy())
	}

	// Simulate another writer changing the note after this editor opened.
	currentVersion = strings.Repeat("b", 64)
	v.prop.SetValue("status=done")
	_, saveCmd := v.Update(enterMsg())
	if saveCmd == nil {
		t.Fatal("property submit did not issue a request")
	}
	if _, ok := saveCmd().(errMsg); !ok {
		t.Fatal("stale property submit was not rejected")
	}
	if postedVersion != openedVersion {
		t.Fatalf("posted version = %q, want editor-open version %q", postedVersion, openedVersion)
	}
	if writes != 0 {
		t.Fatalf("stale editor performed %d writes", writes)
	}
}
