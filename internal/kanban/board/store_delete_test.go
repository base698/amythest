package board

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deleteTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir(), func() time.Time { return time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC) })
}

func TestDeleteBoardRemovesAndAllowsRecreationOfEmptyBoard(t *testing.T) {
	store := deleteTestStore(t)
	if _, err := store.CreateBoard("temporary"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteBoard("temporary"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.boardPath("temporary")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("board still exists after deletion: %v", err)
	}
	if _, err := store.CreateBoard("temporary"); err != nil {
		t.Fatalf("recreate deleted board: %v", err)
	}
}

func TestDeleteBoardRejectsActiveAndArchivedCards(t *testing.T) {
	for _, archived := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "archived"}[archived], func(t *testing.T) {
			store := deleteTestStore(t)
			if _, err := store.CreateBoard("kept"); err != nil {
				t.Fatal(err)
			}
			card, err := store.CreateCard("kept", CardInput{Title: "Must survive"})
			if err != nil {
				t.Fatal(err)
			}
			if archived {
				if _, err := store.MoveCard("kept", card.ID, Done, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.DeleteBoard("kept"); !errors.Is(err, ErrBoardNotEmpty) {
				t.Fatalf("DeleteBoard error = %v, want ErrBoardNotEmpty", err)
			}
			if _, err := store.Load("kept"); err != nil {
				t.Fatalf("board was changed after rejected deletion: %v", err)
			}
		})
	}
}

func TestDeleteBoardRejectsUnexpectedPaths(t *testing.T) {
	store := deleteTestStore(t)
	if _, err := store.CreateBoard("guarded"); err != nil {
		t.Fatal(err)
	}
	unexpected := filepath.Join(store.root, "guarded", "keep-me.txt")
	if err := os.WriteFile(unexpected, []byte("do not discard"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteBoard("guarded"); err == nil {
		t.Fatal("DeleteBoard succeeded despite unexpected content")
	}
	if got, err := os.ReadFile(unexpected); err != nil || string(got) != "do not discard" {
		t.Fatalf("unexpected content was not preserved: %q, %v", got, err)
	}
}

func TestCreateBoardRejectsReservedFocusRouteName(t *testing.T) {
	store := deleteTestStore(t)
	if _, err := store.CreateBoard("focus"); err == nil {
		t.Fatal("CreateBoard accepted reserved focus route name")
	}
}

func TestRenderBoardHasNoTrailingWhitespaceAndOneFinalNewline(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	value := Board{Version: 2, Name: "example", DisplayName: "Example", Description: "Board line  ", Cards: []Card{{
		ID: "card-1", Title: "Card", Description: "first  \nsecond 	", Priority: P2, Status: Done, UpdatedAt: now,
	}}}
	rendered := string(renderBoard(value, true, now))
	for index, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n") {
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "	") {
			t.Fatalf("line %d has trailing whitespace: %q", index+1, line)
		}
	}
	if !strings.HasSuffix(rendered, "\n") || strings.HasSuffix(rendered, "\n\n") {
		t.Fatal("rendered board must end in exactly one newline")
	}
}
