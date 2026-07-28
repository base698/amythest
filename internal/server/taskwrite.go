package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
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

// toggleInFile applies the toggle to the note's file with an atomic
// write (temp + rename), preserving the frontmatter block untouched.
func toggleInFile(v *vault.Vault, n *vault.Note, line int, done bool) (bool, error) {
	abs := filepath.Join(v.Root, filepath.FromSlash(n.Path))
	src, err := os.ReadFile(abs)
	if err != nil {
		return false, err
	}
	_, body := vault.ParseFrontmatter(src)
	prefix := src[:len(src)-len(body)]

	newBody, recurred, err := tasks.ToggleLine(body, line, done, time.Now())
	if err != nil {
		return false, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return false, err
	}
	tmp := abs + ".amythest-tmp"
	out := append(append([]byte{}, prefix...), newBody...)
	if err := os.WriteFile(tmp, out, info.Mode().Perm()); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return recurred, nil
}
