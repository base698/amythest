package tui

import (
	"strings"
	"testing"

	"github.com/base698/amythest/internal/taskreview"
	"github.com/base698/amythest/internal/tasks"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestFinishedReviewAllowsDashboardNavigation(t *testing.T) {
	w := workspaceFixture()
	r := newTaskReviewView(w.client(), nil)
	w.app.stack = []view{newTasksView(w.client()), r}
	r.input.SetValue("Find evidence")
	w.Update(tea.KeyMsg{Type: tea.KeyF1})
	if w.app.top() != r || r.input.Value() != "Find evidence" {
		t.Fatal("navigation discarded a review prompt")
	}
	r.Update(taskReviewDone{owner: r})
	if r.Capturing() || !w.tabSwitchesPanels() {
		t.Fatal("finished review still captures navigation keys")
	}
	w.Update(tea.KeyMsg{Type: tea.KeyF1})
	if _, ok := w.app.top().(*todayView); !ok {
		t.Fatal("F1 did not leave the completed review")
	}
}

func TestReviewEscapeHintStaysVisibleWhileScrolled(t *testing.T) {
	r := newTaskReviewView(nil, []tasks.Task{{Text: "Check the build"}})
	r.Update(taskReviewDone{owner: r, result: taskreview.Result{Saved: 1, Findings: []taskreview.Finding{{ID: 0, Summary: strings.Repeat("Evidence from the build. ", 100)}}}})
	for _, size := range [][2]int{{80, 12}, {28, 6}, {20, 1}} {
		for _, offset := range []int{0, 20, 10000} {
			r.scroll = offset
			lines := strings.Split(ansi.Strip(r.View(size[0], size[1])), "\n")
			if len(lines) != size[1] || !strings.Contains(lines[len(lines)-1], "Esc tasks") {
				t.Fatalf("lost fixed escape hint at %v, offset %d", size, offset)
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > size[0] {
					t.Fatal("review overflowed its panel")
				}
			}
		}
	}
}
