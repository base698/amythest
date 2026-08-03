package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
)

func (s *Server) handleTaskMoveToBoard(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
	}
	if s.kanban == nil {
		http.Error(w, "kanban is unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Board           string `json:"board"`
		Slug            string `json:"slug"`
		Line            int    `json:"line"`
		ExpectedText    string `json:"expectedText"`
		ExpectedStatus  string `json:"expectedStatus"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Board == "" || req.Slug == "" || len(req.Slug) > 512 || req.Line < 1 || len(req.ExpectedText) > 10_000 || len(req.ExpectedVersion) != 64 {
		http.Error(w, "board, slug, positive line, expectedText, and expectedVersion are required", http.StatusBadRequest)
		return
	}
	if req.ExpectedStatus != tasks.StatusOpen {
		http.Error(w, "only an expected open task can be moved", http.StatusBadRequest)
		return
	}
	boards, err := s.kanban.ListBoards()
	if err != nil {
		http.Error(w, "list kanban boards: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	boardExists := false
	for _, candidate := range boards {
		if candidate.Name == req.Board {
			boardExists = true
			break
		}
	}
	if !boardExists {
		http.Error(w, "selected board does not exist", http.StatusBadRequest)
		return
	}

	s.taskWriteMu.Lock()
	defer s.taskWriteMu.Unlock()
	v := s.vault.Load()
	n, ok := v.BySlug(req.Slug)
	if !ok {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	if strings.HasPrefix(n.Path, "kanban/") {
		http.Error(w, "tasks in kanban notes are managed through the kanban API", http.StatusForbidden)
		return
	}

	var moved board.Card
	err = s.kanban.WithTaskMoveBoard(req.Board, func(tx *board.TaskMoveBoard) error {
		return tasks.MoveTaskToBoardInFileAndReindex(v.Root, n.Path, tasks.MoveTaskInput{
			Line: req.Line, ExpectedText: req.ExpectedText, ExpectedStatus: req.ExpectedStatus, ExpectedVersion: req.ExpectedVersion,
		}, func(task tasks.Task) (tasks.CreatedTaskReference, error) {
			description := "Source note: " + n.Path
			if !strings.ContainsAny(n.Slug, "[]|\r\n") {
				description = fmt.Sprintf("Source note: [[%s]] (%s)", n.Slug, n.Path)
			}
			title, truncated := cardTitleFromTask(task.Text)
			if truncated {
				// Nothing is lost: the full wording goes in the description,
				// which allows 10000 bytes.
				if full := "Full task: " + task.Text + "\n\n" + description; len(full) <= 10000 {
					description = full
				}
			}
			card, cardCommitted, createErr := tx.Create(board.CardInput{
				Title: title, Description: description, DueDate: task.Due, Status: board.Triage,
			})
			if createErr != nil && !cardCommitted {
				return tasks.CreatedTaskReference{}, createErr
			}
			if !cardCommitted {
				return tasks.CreatedTaskReference{}, fmt.Errorf("kanban card write did not commit")
			}
			moved = card
			// The link text is just the card id: the task description sits
			// right beside it on the same line, so repeating it only made the
			// source line long and hard to read.
			reference := fmt.Sprintf("[[kanban/%s/board#^card-%s|%s]]", req.Board, card.ID, card.ID)
			return tasks.CreatedTaskReference{
				Reference: reference,
				Rollback: func() error {
					_, deleteCommitted, rollbackErr := tx.DeleteExact(card)
					if deleteCommitted {
						return nil
					}
					return rollbackErr
				},
			}, nil
		}, s.rescanWhileVaultLocked)
	})
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "card": moved})
}

// cardTitleLimit mirrors the board store's card-title rule, which counts
// bytes — so accented characters cost more than one. Long task descriptions
// are common, and a move must not fail just because the wording is verbose.
const cardTitleLimit = 200

// cardTitleFromTask fits a task description into a card title: whitespace is
// collapsed to one line, and an over-long title is cut at a word boundary
// without splitting a multi-byte rune. Callers keep the full text elsewhere.
func cardTitleFromTask(text string) (string, bool) {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= cardTitleLimit {
		return text, false
	}
	const ellipsis = "…"
	cut := cardTitleLimit - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	if space := strings.LastIndexByte(text[:cut], ' '); space > cut/2 {
		cut = space
	}
	return strings.TrimRight(text[:cut], " ") + ellipsis, true
}
