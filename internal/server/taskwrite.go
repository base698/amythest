package server

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

// handleTaskToggle checks/unchecks a task line in its vault file. This is
// the notes side's only write path; kanban boards are excluded (their store
// owns those files) and, when kanban auth is configured, a valid session is
// required so the write path is never weaker than the kanban API.
func (s *Server) handleTaskToggle(w http.ResponseWriter, r *http.Request) {
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
		Slug string `json:"slug"`
		Line int    `json:"line"`
		Done bool   `json:"done"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
		http.Error(w, "kanban boards are managed through the kanban API", http.StatusForbidden)
		return
	}

	recurred, err := toggleInFile(v, n, req.Line, req.Done)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}

	// Reindex now so the response's follow-up page fetch sees fresh HTML.
	if err := s.Rescan(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "recurred": recurred})
}

// handleTaskTriage classifies one open, undated task without inventing a
// deadline. It uses the same vault-note lookup, auth gate, kanban exclusion,
// atomic write, and immediate reindex contract as task toggles.
func (s *Server) handleTaskTriage(w http.ResponseWriter, r *http.Request) {
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

	type requestItem struct {
		Line         int    `json:"line"`
		Action       string `json:"action"`
		Due          string `json:"due"`
		ExpectedText string `json:"expectedText"`
	}
	var req struct {
		Slug         string        `json:"slug"`
		Line         int           `json:"line"`
		Action       string        `json:"action"`
		Due          string        `json:"due"`
		ExpectedText string        `json:"expectedText"`
		Items        []requestItem `json:"items"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || len(req.Slug) > 512 {
		http.Error(w, "slug is required and must be at most 512 characters", http.StatusBadRequest)
		return
	}
	if len(req.Items) > 500 {
		http.Error(w, "at most 500 tasks may be triaged at once", http.StatusBadRequest)
		return
	}
	items := make([]tasks.TriageItem, 0, max(1, len(req.Items)))
	appendItem := func(line int, action, due, expectedText string) bool {
		if line < 1 || len(expectedText) > 10_000 {
			http.Error(w, "each task needs a positive line and expectedText of at most 10000 characters", http.StatusBadRequest)
			return false
		}
		triageAction := tasks.TriageAction(action)
		switch triageAction {
		case tasks.TriageBacklog, tasks.TriageDue, tasks.TriageReference, tasks.TriageCancel:
		default:
			http.Error(w, "action must be backlog, due, reference, or cancel", http.StatusBadRequest)
			return false
		}
		if (triageAction == tasks.TriageDue && len(due) != 10) || (triageAction != tasks.TriageDue && due != "") {
			http.Error(w, "due must be an ISO date only for the due action", http.StatusBadRequest)
			return false
		}
		items = append(items, tasks.TriageItem{Line: line, Mutation: tasks.TriageMutation{
			Action: triageAction, ExpectedText: expectedText, Due: due,
		}})
		return true
	}
	if len(req.Items) > 0 {
		for _, item := range req.Items {
			if !appendItem(item.Line, item.Action, item.Due, item.ExpectedText) {
				return
			}
		}
	} else if !appendItem(req.Line, req.Action, req.Due, req.ExpectedText) {
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
		http.Error(w, "kanban boards are managed through the kanban API", http.StatusForbidden)
		return
	}

	err := tasks.TriageBatchInFile(v.Root, n.Path, items, time.Now())
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	if err := s.Rescan(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// toggleInFile delegates to the shared task writer so this path and the MCP
// toggle_task tool apply identical recurrence and file-safety semantics.
func toggleInFile(v *vault.Vault, n *vault.Note, line int, done bool) (bool, error) {
	return tasks.ToggleInFile(v.Root, n.Path, line, done, time.Now())
}
