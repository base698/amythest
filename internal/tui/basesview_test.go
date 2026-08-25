package tui

import (
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
	// e opens the property prompt; enter with key=value emits the write.
	v.Update(keyMsg("e"))
	if !v.propping || !v.Capturing() {
		t.Fatal("e must open the property prompt")
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
