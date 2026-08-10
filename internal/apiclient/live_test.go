package apiclient

// Live smoke against a real server, gated like the markdown smoke test:
//
//	AMY_LIVE_ENDPOINT=http://127.0.0.1:8142 go test ./internal/apiclient -run Live
//
// It mutates data (toggles a task back and forth, edits a card description),
// so point it at a scratch server, not a real vault.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/tasks"
)

func TestLiveTaskAndCardToggleRoundTrip(t *testing.T) {
	endpoint := os.Getenv("AMY_LIVE_ENDPOINT")
	if endpoint == "" {
		t.Skip("AMY_LIVE_ENDPOINT not set")
	}
	c := New(Config{Endpoint: strings.TrimRight(endpoint, "/"), Timeout: 30 * time.Second,
		SessionFile: os.Getenv("KANBAN_SESSION_FILE")})
	ctx := context.Background()

	// Vault task: complete, confirm the ✅ done-date, then reopen.
	groups, err := c.ListTasks(ctx, "not done;sort by due")
	if err != nil {
		t.Fatal(err)
	}
	var target *tasks.Task
	for gi := range groups {
		for ti := range groups[gi].Tasks {
			if !strings.HasPrefix(groups[gi].Tasks[ti].Path, "kanban/") {
				target = &groups[gi].Tasks[ti]
				break
			}
		}
	}
	if target == nil {
		t.Fatal("no open non-kanban task on the server")
	}
	if _, err := c.ToggleTask(ctx, target.Slug, target.Line, target.Version, true); err != nil {
		t.Fatal(err)
	}
	// A second toggle with the now-stale version must conflict.
	if _, err := c.ToggleTask(ctx, target.Slug, target.Line, target.Version, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale toggle err = %v, want ErrConflict", err)
	}
	doneGroups, err := c.ListTasks(ctx, "done;description includes "+target.Text)
	if err != nil {
		t.Fatal(err)
	}
	fresh, ok := findLiveTask(doneGroups, target.Slug, target.Text)
	if !ok || fresh.DoneDate == "" {
		t.Fatalf("completed task not found with done-date: %+v ok=%v", fresh, ok)
	}
	if _, err := c.ToggleTask(ctx, fresh.Slug, fresh.Line, fresh.Version, false); err != nil {
		t.Fatal(err)
	}

	// Card checkbox: read-modify-write the description like the TUI does.
	boards, err := c.ListBoards(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, bs := range boards {
		b, err := c.GetBoard(ctx, bs.Name)
		if err != nil {
			t.Fatal(err)
		}
		for _, card := range b.Cards {
			lines := strings.Split(card.Description, "\n")
			for i, line := range lines {
				if !strings.Contains(line, "- [ ]") {
					continue
				}
				newBody, _, err := tasks.ToggleLine([]byte(card.Description), i+1, true, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				desc := string(newBody)
				saved, err := c.PatchCard(ctx, bs.Name, card.ID, CardPatch{Description: &desc})
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(saved.Description, "✅") {
					t.Fatalf("no done-date in saved description: %q", saved.Description)
				}
				// Restore the original description.
				original := card.Description
				if _, err := c.PatchCard(ctx, bs.Name, card.ID, CardPatch{Description: &original}); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
	}
	t.Fatal("no card with an open checkbox found")
}

func findLiveTask(groups []TaskGroup, slug, text string) (tasks.Task, bool) {
	for _, g := range groups {
		for _, task := range g.Tasks {
			if task.Slug == slug && task.Text == text {
				return task, true
			}
		}
	}
	return tasks.Task{}, false
}
