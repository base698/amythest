package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
)

func (s *Server) handleTaskMoveToBoard(w http.ResponseWriter, r *http.Request) {
	if s.kanban == nil {
		http.Error(w, "kanban is unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.kanbanAuth != nil {
		cookie, err := r.Cookie(kanbanSessionCookie)
		if err != nil {
			http.Error(w, "sign in to the kanban to edit tasks", http.StatusUnauthorized)
			return
		}
		if _, err := s.kanbanAuth.Verify(cookie.Value, time.Now()); err != nil {
			http.Error(w, "session expired: sign in to the kanban again", http.StatusUnauthorized)
			return
		}
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
			card, cardCommitted, createErr := tx.Create(board.CardInput{
				Title: task.Text, Description: description, DueDate: task.Due, Status: board.Triage,
			})
			if createErr != nil && !cardCommitted {
				return tasks.CreatedTaskReference{}, createErr
			}
			if !cardCommitted {
				return tasks.CreatedTaskReference{}, fmt.Errorf("kanban card write did not commit")
			}
			moved = card
			alias := sanitizeWikilinkAlias(task.Text)
			reference := fmt.Sprintf("[[kanban/%s/board#^card-%s|%s (%s)]]", req.Board, card.ID, alias, card.ID)
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

func sanitizeWikilinkAlias(value string) string {
	value = strings.NewReplacer("[", "(", "]", ")", "|", "-", "\r", " ", "\n", " ").Replace(value)
	value = strings.TrimSpace(value)
	if value == "" {
		return "Moved task"
	}
	return value
}
