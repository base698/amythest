package tui

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/tasks"
)

// confirm is the shared destructive-action prompt: only "y" proceeds, any
// other key backs out.
type confirm struct {
	active   bool
	question string
}

func (c *confirm) open(question string) { c.active = true; c.question = question }

// handleKey closes the prompt and reports whether the user confirmed.
func (c *confirm) handleKey(msg tea.KeyMsg) (yes bool) {
	c.active = false
	return msg.String() == "y"
}

func (c *confirm) bar() string {
	return dangerStyle.Render(" " + c.question + " (y/N)")
}

// Delete announcements. Cancel is the recoverable stage; purge and card
// deletes are permanent.
type (
	taskCancelledMsg struct{ slug, text string }
	taskPurgedMsg    struct{ slug, text string }
	cardDeletedMsg   struct{ board, cardID, title string }
)

// deleteTaskCmd cancels an open/done task, or permanently purges an
// already-cancelled one. Stale versions retry once by (slug, text), matching
// the toggle flow.
func deleteTaskCmd(client *apiclient.Client, t tasks.Task) tea.Cmd {
	purge := t.Status == tasks.StatusCancelled
	return func() tea.Msg {
		ctx := context.Background()
		run := func(target tasks.Task) error {
			if purge {
				return client.PurgeTask(ctx, target)
			}
			return client.CancelTask(ctx, target)
		}
		err := run(t)
		if errors.Is(err, apiclient.ErrConflict) {
			groups, qerr := client.ListTasks(ctx, "description includes "+t.Text)
			if qerr != nil {
				return fail(qerr)
			}
			fresh, ok := findTask(groups, t.Slug, t.Text, t.Status)
			if !ok {
				return fail(fmt.Errorf("task changed on server; refresh (r) and retry"))
			}
			err = run(fresh)
		}
		if err != nil {
			return fail(err)
		}
		if purge {
			return taskPurgedMsg{slug: t.Slug, text: t.Text}
		}
		return taskCancelledMsg{slug: t.Slug, text: t.Text}
	}
}

func deleteCardCmd(client *apiclient.Client, boardName string, cardID, title string) tea.Cmd {
	return func() tea.Msg {
		if err := client.DeleteCard(context.Background(), boardName, cardID); err != nil {
			return fail(err)
		}
		return cardDeletedMsg{board: boardName, cardID: cardID, title: title}
	}
}
