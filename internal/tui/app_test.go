package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/source"
)

// drive the real App model without a terminal: size it, feed data and keys,
// and assert on rendered frames.
func TestAppRendersTasksNavigatesToBoardsAndBack(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client, source.NewRegistry())
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Home is the today view.
	frame := app.View()
	if !strings.Contains(frame, "amy · today") {
		t.Fatalf("home frame:\n%s", frame)
	}

	// "2" swaps to the tasks view.
	app.Update(keyMsg("2"))
	app.Update(loadedTasks())
	frame = app.View()
	if !strings.Contains(frame, "amy · tasks") || !strings.Contains(frame, "water the ferns") {
		t.Fatalf("tasks frame:\n%s", frame)
	}
	if !strings.Contains(frame, "http://test.example") {
		t.Fatalf("status bar missing endpoint:\n%s", frame)
	}

	// "3" swaps to the boards view and kicks off its load.
	_, cmd := app.Update(keyMsg("3"))
	if cmd == nil {
		t.Fatal("expected boards load command")
	}
	app.Update(boardsLoadedMsg{boards: []board.BoardSummary{{
		Name: "personal", DisplayName: "Personal",
		Counts: map[board.Status]int{board.Triage: 2, board.Done: 5},
	}}})
	frame = app.View()
	if !strings.Contains(frame, "amy · boards") || !strings.Contains(frame, "Personal") {
		t.Fatalf("boards frame:\n%s", frame)
	}

	// enter pushes the board view; the push command must be executed by the
	// runtime, so simulate that dispatch loop here.
	_, cmd = app.Update(enterMsg())
	if cmd == nil {
		t.Fatal("expected push command")
	}
	if _, isPush := cmd().(pushMsg); !isPush {
		t.Fatalf("expected pushMsg, got %#v", cmd())
	}
	app.Update(cmd())
	app.Update(loadedBoard())
	frame = app.View()
	if !strings.Contains(frame, "amy · boards › personal") || !strings.Contains(frame, "First") {
		t.Fatalf("board frame:\n%s", frame)
	}

	// esc pops back to boards.
	app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	frame = app.View()
	if !strings.Contains(frame, "amy · boards") || strings.Contains(frame, "› personal") {
		t.Fatalf("after esc:\n%s", frame)
	}

	// q quits.
	_, cmd = app.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("expected quit command")
	}
}

func TestMissingCredentialsRenderPersistentSafeInBodyPanel(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client, source.NewRegistry())
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	secret := "do-not-render-this-secret"
	app.Update(errMsg{err: fmt.Errorf("%w: %s", apiclient.ErrMissingCredentials, secret)})

	frame := app.View()
	if !strings.Contains(frame, "AMYTHEST SETUP REQUIRED") ||
		!strings.Contains(frame, "Configure the Amythest login credentials") ||
		!strings.Contains(frame, "r to retry") {
		t.Fatalf("missing prominent credential guidance:\n%s", frame)
	}
	if strings.Contains(frame, secret) {
		t.Fatalf("credential error leaked raw details:\n%s", frame)
	}
	if !strings.Contains(frame, "http://test.example") {
		t.Fatalf("normal status bar was replaced:\n%s", frame)
	}

	app.Update(keyMsg("j"))
	if !strings.Contains(app.View(), "AMYTHEST SETUP REQUIRED") {
		t.Fatal("credential panel must persist across keypresses")
	}
}

func TestUnavailableServiceRendersDistinctSafeInBodyPanel(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client, source.NewRegistry())
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	secret := "private-transport-detail"
	app.Update(errMsg{err: fmt.Errorf("%w: %s", apiclient.ErrUnavailable, secret)})

	frame := app.View()
	if !strings.Contains(frame, "AMYTHEST SERVICE UNAVAILABLE") ||
		!strings.Contains(frame, "Check the configured endpoint and Amythest service") ||
		!strings.Contains(frame, "r to retry") {
		t.Fatalf("missing prominent availability guidance:\n%s", frame)
	}
	if strings.Contains(frame, "AMYTHEST SETUP REQUIRED") || strings.Contains(frame, secret) {
		t.Fatalf("availability panel is misclassified or leaks details:\n%s", frame)
	}
	app.Update(keyMsg("j"))
	if !strings.Contains(app.View(), "AMYTHEST SERVICE UNAVAILABLE") {
		t.Fatal("availability panel must persist across keypresses")
	}
}

func TestUnclassifiedErrorClearsStaleClassifiedAlert(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client, source.NewRegistry())
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	app.Update(errMsg{err: apiclient.ErrUnavailable})
	if !strings.Contains(app.View(), "AMYTHEST SERVICE UNAVAILABLE") {
		t.Fatal("precondition: availability panel not rendered")
	}

	app.Update(errMsg{err: errors.New("new unclassified failure")})
	frame := app.View()
	if strings.Contains(frame, "AMYTHEST SERVICE UNAVAILABLE") {
		t.Fatalf("stale classified alert remained after a newer unclassified error:\n%s", frame)
	}
	if !strings.Contains(frame, "error: new unclassified failure") {
		t.Fatalf("new unclassified error missing from status:\n%s", frame)
	}
}

func TestClassifiedErrorsReplacePreviousClassifiedAlert(t *testing.T) {
	tests := []struct {
		name      string
		firstErr  error
		firstText string
		secondErr error
		wantText  string
	}{
		{
			name:      "unavailable to missing credentials",
			firstErr:  apiclient.ErrUnavailable,
			firstText: "AMYTHEST SERVICE UNAVAILABLE",
			secondErr: apiclient.ErrMissingCredentials,
			wantText:  "AMYTHEST SETUP REQUIRED",
		},
		{
			name:      "missing credentials to unavailable",
			firstErr:  apiclient.ErrMissingCredentials,
			firstText: "AMYTHEST SETUP REQUIRED",
			secondErr: apiclient.ErrUnavailable,
			wantText:  "AMYTHEST SERVICE UNAVAILABLE",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
			app := NewApp(client, source.NewRegistry())
			app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			app.Update(errMsg{err: tt.firstErr})
			if !strings.Contains(app.View(), tt.firstText) {
				t.Fatalf("precondition: first classified alert %q not rendered", tt.firstText)
			}

			app.Update(errMsg{err: tt.secondErr})
			frame := app.View()
			if strings.Contains(frame, tt.firstText) {
				t.Fatalf("previous classified alert %q remained:\n%s", tt.firstText, frame)
			}
			if !strings.Contains(frame, tt.wantText) {
				t.Fatalf("replacement classified alert %q missing:\n%s", tt.wantText, frame)
			}
		})
	}
}

func TestSuccessfulReloadClearsPersistentAmythestPanel(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client, source.NewRegistry())
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	app.Update(errMsg{err: apiclient.ErrUnavailable})
	if !strings.Contains(app.View(), "AMYTHEST SERVICE UNAVAILABLE") {
		t.Fatal("precondition: availability panel not rendered")
	}

	app.Update(todayLoadedMsg{})
	if strings.Contains(app.View(), "AMYTHEST SERVICE UNAVAILABLE") {
		t.Fatal("successful retry must clear the stale availability panel")
	}
}

func TestHelpOverlayClosesOnEscape(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client, source.NewRegistry())
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	app.Update(keyMsg("?"))
	if !strings.Contains(app.View(), "Keys") {
		t.Fatal("help should be open")
	}
	app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(app.View(), "j/k or arrows") {
		t.Fatal("esc must close help")
	}
	// Any other key closes it too, without triggering its normal action.
	app.Update(keyMsg("?"))
	_, cmd := app.Update(keyMsg("q"))
	if cmd != nil {
		t.Fatal("q while help is open must close help, not quit")
	}
	if strings.Contains(app.View(), "j/k or arrows") {
		t.Fatal("q must close help")
	}
}

func TestCoalescedRunesReplayAsIndividualKeys(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client, source.NewRegistry())
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	app.Update(keyMsg("2")) // tasks view
	app.Update(loadedTasks())
	// One coalesced "jj" must move the cursor two rows, not zero.
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jj")})
	tv := app.top().(*tasksView)
	if tv.current() == nil || tv.current().Text != "card checkbox" {
		t.Fatalf("cursor after coalesced jj: %+v", tv.current())
	}
}

func TestCompletionFeedbackAppearsInStatusBar(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client, source.NewRegistry())
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	app.Update(cardArchivedMsg{board: "personal", card: &board.Card{ID: "c1"}})
	if !strings.Contains(app.View(), "card archived ✓") {
		t.Fatalf("no archive feedback in:\n%s", app.View())
	}
	app.Update(taskToggledMsg{slug: "s", text: "x", done: true})
	if !strings.Contains(app.View(), "task completed ✓") {
		t.Fatal("no toggle feedback")
	}
}
