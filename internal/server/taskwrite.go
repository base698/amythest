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

// handleTaskFileHide opts an entire note out of task indexing while retaining
// the note itself in rendering, links, and search.
func (s *Server) handleTaskFileHide(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
	}
	var req struct {
		Slug            string `json:"slug"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || len(req.Slug) > 512 || len(req.ExpectedVersion) != 64 {
		http.Error(w, "slug and expectedVersion are required; refresh and retry", http.StatusBadRequest)
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
	if err := tasks.HideFileFromTasksAndReindex(v.Root, n.Path, req.ExpectedVersion, s.rescanWhileVaultLocked); err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleTaskToggle checks/unchecks a task line in its vault file. This is
// the notes side's only write path; kanban boards are excluded (their store
// owns those files) and, when kanban auth is configured, a valid session is
// required so the write path is never weaker than the kanban API.
func (s *Server) handleTaskToggle(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
	}

	var req struct {
		Slug            string `json:"slug"`
		Line            int    `json:"line"`
		ExpectedVersion string `json:"expectedVersion"`
		Done            bool   `json:"done"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || req.Line < 1 || len(req.ExpectedVersion) != 64 {
		http.Error(w, "slug, positive line, and expectedVersion are required; refresh and retry", http.StatusBadRequest)
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

	recurred, err := toggleInFile(v, n, req.Line, req.ExpectedVersion, req.Done, s.rescanWhileVaultLocked)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}

	s.writeJSON(w, map[string]any{"ok": true, "recurred": recurred})
}

// handleTaskDue sets, changes, or clears the due date on an indexed task.
// The request carries the rendered text, prior due date, and whole-file
// version so stale rows are rejected before the shared atomic writer
// replaces the note.
func (s *Server) handleTaskDue(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
	}

	var req struct {
		Slug            string `json:"slug"`
		Line            int    `json:"line"`
		ExpectedText    string `json:"expectedText"`
		ExpectedStatus  string `json:"expectedStatus"`
		ExpectedDue     string `json:"expectedDue"`
		ExpectedVersion string `json:"expectedVersion"`
		Due             string `json:"due"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || len(req.Slug) > 512 || req.Line < 1 || len(req.ExpectedText) > 10_000 {
		http.Error(w, "slug, positive line, and expectedText of at most 10000 characters are required", http.StatusBadRequest)
		return
	}
	if len(req.ExpectedVersion) != 64 {
		http.Error(w, "expectedVersion is required; refresh and retry", http.StatusBadRequest)
		return
	}
	if req.ExpectedStatus != tasks.StatusOpen && req.ExpectedStatus != tasks.StatusDone && req.ExpectedStatus != tasks.StatusCancelled && req.ExpectedStatus != tasks.StatusOther {
		http.Error(w, "expectedStatus must be open, done, cancelled, or other", http.StatusBadRequest)
		return
	}
	if len(req.ExpectedDue) != 0 && len(req.ExpectedDue) != 10 {
		http.Error(w, "expectedDue must be empty or YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	if len(req.Due) != 0 && len(req.Due) != 10 {
		http.Error(w, "due must be empty or YYYY-MM-DD", http.StatusBadRequest)
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
	if err := tasks.UpdateDueDateInFileAndReindex(v.Root, n.Path, req.Line, req.ExpectedText, req.ExpectedStatus, req.ExpectedDue, req.Due, req.ExpectedVersion, s.rescanWhileVaultLocked); err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "due": req.Due})
}

// handleTaskTriage classifies one open, undated task without inventing a
// deadline. It uses the same vault-note lookup, auth gate, kanban exclusion,
// atomic write, and immediate reindex contract as task toggles.
func (s *Server) handleTaskTriage(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
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
	// File-wide triage can legitimately exceed the generic 1 MiB API limit.
	// Keep this endpoint bounded while allowing realistic 5,000-item payloads.
	if err := jsonDecodeLimit(r, &req, 64<<20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || len(req.Slug) > 512 {
		http.Error(w, "slug is required and must be at most 512 characters", http.StatusBadRequest)
		return
	}
	if len(req.Items) > taskTriageBatchLimit {
		http.Error(w, "at most 5000 tasks may be triaged at once", http.StatusBadRequest)
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

	err := tasks.TriageBatchInFileAndReindex(v.Root, n.Path, items, time.Now(), s.rescanWhileVaultLocked)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleTaskFileDisposition moves an entire triage note to one of two
// recoverable destinations. "archive" keeps it visible under Archive/Deleted;
// "trash" moves it under Obsidian's hidden .trash tree. Neither action unlinks
// content permanently or overwrites an existing destination.
func (s *Server) handleTaskFileDisposition(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
	}

	var req struct {
		Slug            string `json:"slug"`
		Action          string `json:"action"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || len(req.Slug) > 512 {
		http.Error(w, "slug is required and must be at most 512 characters", http.StatusBadRequest)
		return
	}
	if len(req.ExpectedVersion) != 64 {
		http.Error(w, "expectedVersion is required; refresh and retry", http.StatusBadRequest)
		return
	}
	disposition := tasks.FileDisposition(req.Action)
	if disposition != tasks.FileDispositionArchive && disposition != tasks.FileDispositionTrash {
		http.Error(w, "action must be archive or trash", http.StatusBadRequest)
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
	if strings.HasPrefix(n.Path, "kanban/") || isTaskTriageIgnoredPath(n.Path) {
		http.Error(w, "this note is not eligible for triage file actions", http.StatusForbidden)
		return
	}
	destination, err := tasks.MoveFileInVaultAndReindex(v.Root, n.Path, disposition, req.ExpectedVersion, s.rescanWhileVaultLocked)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "destination": destination})
}

// handleTaskPriority sets one task's priority emoji.
func (s *Server) handleTaskPriority(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
	}
	var req struct {
		Slug             string `json:"slug"`
		Line             int    `json:"line"`
		ExpectedText     string `json:"expectedText"`
		ExpectedStatus   string `json:"expectedStatus"`
		ExpectedPriority *int   `json:"expectedPriority"`
		Priority         *int   `json:"priority"`
		ExpectedVersion  string `json:"expectedVersion"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || len(req.Slug) > 512 || req.Line < 1 || len(req.ExpectedText) > 10_000 {
		http.Error(w, "slug, positive line, and expectedText are required", http.StatusBadRequest)
		return
	}
	if len(req.ExpectedVersion) != 64 {
		http.Error(w, "expectedVersion is required; refresh and retry", http.StatusBadRequest)
		return
	}
	if req.Priority == nil || req.ExpectedPriority == nil ||
		!tasks.ValidPriority(*req.Priority) || !tasks.ValidPriority(*req.ExpectedPriority) {
		http.Error(w, "priority and expectedPriority must be between 0 and 5", http.StatusBadRequest)
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
	if err := tasks.UpdatePriorityInFileAndReindex(v.Root, n.Path, req.Line, req.ExpectedText,
		req.ExpectedStatus, *req.ExpectedPriority, *req.Priority, req.ExpectedVersion,
		s.rescanWhileVaultLocked); err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleTaskCancel marks open tasks cancelled ([-] + ❌ date) — the
// recoverable first half of deletion. Single-file batch, whole-file version
// guard.
func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
	}
	var req struct {
		Slug            string `json:"slug"`
		ExpectedVersion string `json:"expectedVersion"`
		Items           []struct {
			Line         int    `json:"line"`
			ExpectedText string `json:"expectedText"`
		} `json:"items"`
	}
	if err := jsonDecodeLimit(r, &req, 8<<20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || len(req.Slug) > 512 {
		http.Error(w, "slug is required and must be at most 512 characters", http.StatusBadRequest)
		return
	}
	if len(req.ExpectedVersion) != 64 {
		http.Error(w, "expectedVersion is required; refresh and retry", http.StatusBadRequest)
		return
	}
	if len(req.Items) == 0 || len(req.Items) > taskTriageBatchLimit {
		http.Error(w, "between 1 and 5000 tasks may be cancelled at once", http.StatusBadRequest)
		return
	}
	items := make([]tasks.CancelItem, 0, len(req.Items))
	for _, item := range req.Items {
		if item.Line < 1 || len(item.ExpectedText) > 10_000 {
			http.Error(w, "each task needs a positive line and expectedText of at most 10000 characters", http.StatusBadRequest)
			return
		}
		items = append(items, tasks.CancelItem{Line: item.Line, ExpectedText: item.ExpectedText})
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
	if err := tasks.CancelTasksInFileAndReindex(v.Root, n.Path, items, req.ExpectedVersion, time.Now(), s.rescanWhileVaultLocked); err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleTaskPurge permanently removes cancelled task lines — the destructive
// second half of deletion. Only cancelled tasks qualify, so nothing open or
// done can be removed in one step.
func (s *Server) handleTaskPurge(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
	}
	var req struct {
		Slug            string `json:"slug"`
		ExpectedVersion string `json:"expectedVersion"`
		Lines           []int  `json:"lines"`
	}
	if err := jsonDecodeLimit(r, &req, 8<<20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || len(req.Slug) > 512 {
		http.Error(w, "slug is required and must be at most 512 characters", http.StatusBadRequest)
		return
	}
	if len(req.ExpectedVersion) != 64 {
		http.Error(w, "expectedVersion is required; refresh and retry", http.StatusBadRequest)
		return
	}
	if len(req.Lines) == 0 || len(req.Lines) > taskTriageBatchLimit {
		http.Error(w, "between 1 and 5000 tasks may be purged at once", http.StatusBadRequest)
		return
	}
	for _, line := range req.Lines {
		if line < 1 {
			http.Error(w, "each line must be positive", http.StatusBadRequest)
			return
		}
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
	if err := tasks.PurgeCancelledInFileAndReindex(v.Root, n.Path, req.Lines, req.ExpectedVersion, s.rescanWhileVaultLocked); err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// toggleInFile delegates to the shared task writer so this path and the MCP
// toggle_task tool apply identical recurrence and file-safety semantics.
func toggleInFile(v *vault.Vault, n *vault.Note, line int, expectedVersion string, done bool, reindex func() error) (bool, error) {
	return tasks.ToggleInFileAndReindex(v.Root, n.Path, line, expectedVersion, done, time.Now(), reindex)
}
