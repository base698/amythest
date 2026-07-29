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

// toggleInFile delegates to the shared task writer so this path and the MCP
// toggle_task tool apply identical recurrence and file-safety semantics.
func toggleInFile(v *vault.Vault, n *vault.Note, line int, done bool) (bool, error) {
	return tasks.ToggleInFile(v.Root, n.Path, line, done, time.Now())
}
