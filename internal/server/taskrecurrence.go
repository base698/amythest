package server

import (
	"net/http"
	"strings"

	"github.com/base698/amythest/internal/tasks"
)

// handleTaskRecurrence sets, changes, or clears a task's 🔁 rule, with the
// same optimistic-lock contract as due-date edits.
func (s *Server) handleTaskRecurrence(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
	}
	var req struct {
		Slug               string `json:"slug"`
		Line               int    `json:"line"`
		ExpectedText       string `json:"expectedText"`
		ExpectedStatus     string `json:"expectedStatus"`
		ExpectedRecurrence string `json:"expectedRecurrence"`
		ExpectedVersion    string `json:"expectedVersion"`
		Recurrence         string `json:"recurrence"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Recurrence = strings.TrimSpace(req.Recurrence)
	if req.Slug == "" || req.Line < 1 || len(req.ExpectedVersion) != 64 {
		http.Error(w, "slug, positive line, and expectedVersion are required; refresh and retry", http.StatusBadRequest)
		return
	}
	if len(req.ExpectedText) > 10000 || len(req.Recurrence) > 200 || len(req.ExpectedRecurrence) > 200 {
		http.Error(w, "field too long", http.StatusBadRequest)
		return
	}
	switch req.ExpectedStatus {
	case tasks.StatusOpen, tasks.StatusDone, tasks.StatusCancelled, tasks.StatusOther:
	default:
		http.Error(w, "expectedStatus must be a task status", http.StatusBadRequest)
		return
	}
	if req.Recurrence != "" && !tasks.ValidRecurrence(req.Recurrence) {
		http.Error(w, "unsupported recurrence rule (try: every 4 days, every week on wed,sat, every 2 weeks when done)", http.StatusBadRequest)
		return
	}

	v := s.vault.Load()
	n, ok := v.BySlug(req.Slug)
	if !ok {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if strings.HasPrefix(n.Path, "kanban/") {
		http.Error(w, "tasks in kanban notes are managed through the kanban API", http.StatusForbidden)
		return
	}
	if err := tasks.UpdateRecurrenceInFileAndReindex(v.Root, n.Path, req.Line, req.ExpectedText,
		req.ExpectedStatus, req.ExpectedRecurrence, req.Recurrence, req.ExpectedVersion, s.rescanWhileVaultLocked); err != nil {
		status := http.StatusConflict
		if strings.Contains(err.Error(), "unsupported recurrence") {
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "recurrence": req.Recurrence})
}
