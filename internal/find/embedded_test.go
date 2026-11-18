package find

import (
	tea "github.com/charmbracelet/bubbletea"
	"testing"
)

func TestEmbeddedSearchClearsSelectionBeforeCancel(t *testing.T) {
	type finished struct{ choice *Choice }
	m := New(&fakeAPI{}, Options{Finish: func(c *Choice) tea.Cmd { return func() tea.Msg { return finished{c} } }}).(*model)
	_, cmd := m.choose(Result{URL: "https://example.test/note"}, true)
	if got := cmd().(finished).choice; got == nil || got.URL != "https://example.test/note" {
		t.Fatal("selection lost")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := cmd().(finished).choice; got != nil {
		t.Fatal("Escape reopened previous selection")
	}
	standalone := New(&fakeAPI{}, Options{}).(*model)
	_, cmd = standalone.choose(Result{URL: "https://example.test/note"}, true)
	if _, ok := cmd().(tea.QuitMsg); !ok || Chosen(standalone) == nil {
		t.Fatal("standalone search no longer returns its choice")
	}
}
