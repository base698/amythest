package server

import (
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/base698/amythest/internal/tasks"
)

// dailyNotesFolder is the vault folder the daily-note quick-add writes into,
// matching the Obsidian daily-notes convention.
const dailyNotesFolder = "Daily Notes"

// handleTaskAdd appends a new "- [ ] " task line to a note. The destination
// is either {"daily":true} — today's daily note, created when missing — or
// {"slug":"..."} resolved like /api/note (slug, basename, or alias).
func (s *Server) handleTaskAdd(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit tasks") {
		return
	}
	var req struct {
		Slug  string `json:"slug"`
		Daily bool   `json:"daily"`
		Text  string `json:"text"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" || len(req.Text) > 2000 {
		http.Error(w, "text is required (max 2000 chars)", http.StatusBadRequest)
		return
	}
	if strings.ContainsAny(req.Text, "\r\n") {
		http.Error(w, "text must be a single line", http.StatusBadRequest)
		return
	}
	if req.Daily == (req.Slug != "") {
		http.Error(w, "exactly one of daily or slug is required", http.StatusBadRequest)
		return
	}

	v := s.vault.Load()
	var relPath string
	if req.Daily {
		relPath = path.Join(dailyNotesFolder, time.Now().Format("2006-01-02")+".md")
	} else {
		note, ok := v.BySlug(req.Slug)
		if !ok {
			note, ok = v.Resolve(req.Slug)
		}
		if !ok {
			http.Error(w, "note not found", http.StatusNotFound)
			return
		}
		if strings.HasPrefix(note.Path, "kanban/") {
			http.Error(w, "tasks in kanban notes are managed through the kanban API", http.StatusForbidden)
			return
		}
		relPath = note.Path
	}
	if err := tasks.AppendTaskAndReindex(v.Root, relPath, req.Text, s.rescanWhileVaultLocked); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "path": relPath})
}
