package board

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceLinkSurvivesSettingsCardsAndReload(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	b, err := store.CreateBoardWithInput(BoardInput{Name: "robot", Workspace: "robot-workspace"})
	if err != nil || b.Workspace != "robot-workspace" {
		t.Fatalf("create: %#v %v", b, err)
	}
	name := "Robotics"
	if _, err = store.PatchBoardSettings("robot", BoardSettingsPatch{DisplayName: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateCard("robot", CardInput{Title: "Tune gait", Status: Ready}); err != nil {
		t.Fatal(err)
	}
	store = NewStore(root, fixedClock)
	b, err = store.Load("robot")
	if err != nil || b.Workspace != "robot-workspace" {
		t.Fatalf("reload: %#v %v", b, err)
	}
	summaries, err := store.ListBoards()
	if err != nil || len(summaries) != 1 || summaries[0].Workspace != b.Workspace {
		t.Fatalf("summary: %#v %v", summaries, err)
	}
	next := "robot-sim"
	if _, err = store.PatchBoardSettings("robot", BoardSettingsPatch{Workspace: &next}); err != nil {
		t.Fatal(err)
	}
	done, err := readBoard(filepath.Join(root, "robot", "done.md"))
	if err != nil || done.Workspace != next {
		t.Fatalf("archive metadata: %#v %v", done, err)
	}
	for _, invalid := range []string{strings.Repeat("x", 201), "robot\nother", "robot\x00other"} {
		if _, err = store.PatchBoardSettings("robot", BoardSettingsPatch{Workspace: &invalid}); err == nil {
			t.Fatalf("accepted invalid workspace %q", invalid)
		}
	}
	empty := ""
	b, err = store.PatchBoardSettings("robot", BoardSettingsPatch{Workspace: &empty})
	if err != nil || b.Workspace != "" {
		t.Fatalf("unlink: %#v %v", b, err)
	}
}

func TestWorkspaceSettingsPreserveLegacyFavoritesAboveLimit(t *testing.T) {
	store := NewStore(t.TempDir(), fixedClock)
	unpinned, pinned := false, true
	for _, name := range []string{"one", "two", "three", "four"} {
		b, err := store.CreateBoardWithInput(BoardInput{Name: name, Pinned: &unpinned})
		if err != nil {
			t.Fatal(err)
		}
		// Model boards imported from an earlier version with more favorites.
		b.Pinned = true
		if err = atomicWrite(store.boardPath(name), renderBoard(b, false, fixedClock())); err != nil {
			t.Fatal(err)
		}
	}
	workspace := "robot"
	b, err := store.PatchBoardSettings("one", BoardSettingsPatch{Workspace: &workspace, Pinned: &pinned})
	if err != nil || b.Workspace != workspace || !b.Pinned {
		t.Fatal(b, err)
	}
	if _, err = store.CreateBoardWithInput(BoardInput{Name: "five", Pinned: &unpinned}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PatchBoardSettings("five", BoardSettingsPatch{Pinned: &pinned}); !errors.Is(err, ErrFavoriteLimit) {
		t.Fatal("allowed another favorite", err)
	}
}
