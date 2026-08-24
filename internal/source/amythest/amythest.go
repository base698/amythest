// Package amythest adapts the amythest server (via apiclient) to the source
// interface. It is the built-in, always-registered source.
package amythest

import (
	"context"
	"fmt"
	"strings"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/source"
	"github.com/base698/amythest/internal/tasks"
)

// TaskPayload and CardPayload are pointers so the today view's in-place
// mutations (toggle marking, done/restore flags) survive without a reload —
// same semantics the pre-source todayItem struct had.
type TaskPayload struct{ Task *tasks.Task }

type CardPayload struct {
	Card       *board.Card
	Board      string
	PrevStatus board.Status
}

type Source struct {
	client *apiclient.Client
}

func New(client *apiclient.Client) *Source { return &Source{client: client} }

func (s *Source) Name() string { return "amythest" }

func (s *Source) Client() *apiclient.Client { return s.client }

func (s *Source) Health(ctx context.Context) source.Health {
	boards, err := s.client.ListBoards(ctx)
	if err != nil {
		return source.Health{State: source.StateMisconfigured, Detail: err.Error()}
	}
	return source.Health{State: source.StateOK, Detail: fmt.Sprintf("%s · %d boards", s.client.Endpoint(), len(boards))}
}

// DueItems mirrors the original today-view loader: overdue/today vault tasks
// (kanban-backed checkbox tasks excluded), each board's focused card and
// due cards, and — with includeDone — tasks completed today plus cards
// archived today.
func (s *Source) DueItems(ctx context.Context, day string, includeDone bool) ([]source.Item, error) {
	var items []source.Item

	groups, err := s.client.ListTasks(ctx, "not done;due before tomorrow;sort by due")
	if err != nil {
		return nil, err
	}
	for gi := range groups {
		for ti := range groups[gi].Tasks {
			t := &groups[gi].Tasks[ti]
			if strings.HasPrefix(t.Path, "kanban/") {
				continue // card checkboxes surface through their card
			}
			items = append(items, s.taskItem(t, false))
		}
	}
	if includeDone {
		doneGroups, err := s.client.ListTasks(ctx, "done;done on today")
		if err != nil {
			return nil, err
		}
		for gi := range doneGroups {
			for ti := range doneGroups[gi].Tasks {
				t := &doneGroups[gi].Tasks[ti]
				if strings.HasPrefix(t.Path, "kanban/") {
					continue
				}
				items = append(items, s.taskItem(t, true))
			}
		}
	}

	boards, err := s.client.ListBoards(ctx)
	if err != nil {
		return nil, err
	}
	for _, bs := range boards {
		if bs.Archived {
			continue
		}
		b, err := s.client.GetBoard(ctx, bs.Name)
		if err != nil {
			return nil, err
		}
		for i := range b.Cards {
			card := &b.Cards[i]
			focused := card.ID == b.FocusCardID
			if !focused && (card.DueDate == "" || card.DueDate > day) {
				continue
			}
			items = append(items, s.cardItem(card, b.Name, focused, false))
		}
		if includeDone {
			archived, err := s.client.ListArchive(ctx, bs.Name, 50)
			if err != nil {
				return nil, err
			}
			for i := range archived {
				card := &archived[i]
				if card.DoneAt == nil || card.DoneAt.Local().Format("2006-01-02") != day {
					continue
				}
				items = append(items, s.cardItem(card, bs.Name, false, true))
			}
		}
	}
	return items, nil
}

func (s *Source) taskItem(t *tasks.Task, done bool) source.Item {
	meta := t.Path
	if t.Recurrence != "" {
		meta = "🔁 " + t.Recurrence + "  " + t.Path
	}
	return source.Item{
		Source:  s.Name(),
		ID:      fmt.Sprintf("%s#%d", t.Slug, t.Line),
		Kind:    "task",
		Title:   t.Text,
		Due:     t.Due,
		Done:    done,
		Meta:    meta,
		Payload: TaskPayload{Task: t},
	}
}

func (s *Source) cardItem(card *board.Card, boardName string, focused, done bool) source.Item {
	var badges []string
	if card.Blocked {
		badges = append(badges, "blocked")
	}
	prev := card.Status
	if done {
		prev = board.Triage // archive listing loses the pre-done column
	}
	return source.Item{
		Source:  s.Name(),
		ID:      card.ID,
		Kind:    "card",
		Title:   card.Title,
		Due:     card.DueDate,
		Done:    done,
		Focused: focused,
		Badges:  badges,
		Meta:    boardName,
		Payload: &CardPayload{Card: card, Board: boardName, PrevStatus: prev},
	}
}

// AgentContext frames a task or card for a herdr hand-off.
func (s *Source) AgentContext(it source.Item) (string, string, error) {
	switch p := it.Payload.(type) {
	case *CardPayload:
		return it.Title, cardContext(*p.Card, p.Board, s.client.Endpoint()), nil
	case TaskPayload:
		body := fmt.Sprintf("Context from my amythest task (%s):\n\n- [ ] %s", p.Task.Path, p.Task.Text)
		if p.Task.Due != "" {
			body += " 📅 " + p.Task.Due
		}
		return p.Task.Text, body, nil
	default:
		return "", "", fmt.Errorf("no agent context for item %s", it.ID)
	}
}

// cardContext mirrors the card view's prompt framing.
func cardContext(card board.Card, boardName, endpoint string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Context from my amythest kanban card %q (board %s, %s/kanban/):\n", card.Title, boardName, endpoint)
	fmt.Fprintf(&b, "status: %s", card.Status)
	if card.Priority != "" {
		fmt.Fprintf(&b, " · priority: %s", card.Priority)
	}
	if card.DueDate != "" {
		fmt.Fprintf(&b, " · due: %s", card.DueDate)
	}
	if card.Blocked {
		b.WriteString(" · BLOCKED")
	}
	b.WriteString("\n\n")
	b.WriteString(card.Description)
	return b.String()
}
