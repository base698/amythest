package server

import (
	"net/http"
	"strings"

	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

// handleNoteRaw returns a note's raw markdown body for API clients (the amy
// TUI's note view). Same visibility as the rendered note pages — read-only,
// no auth — and the same slug resolution, so links copied from pages work.
func (s *Server) handleNoteRaw(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	if slug == "" {
		http.Error(w, "missing slug", http.StatusBadRequest)
		return
	}
	v := s.vault.Load()
	note, ok := v.BySlug(slug)
	if !ok {
		// Fall back to Obsidian wikilink resolution so a client can pass a
		// raw [[target]] (basename, alias, or path) straight through.
		note, ok = v.Resolve(slug)
	}
	if !ok {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	raw, err := tasks.ReadNoteFile(v.Root, note.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, body := vault.ParseFrontmatter(raw)
	s.writeJSON(w, map[string]string{
		"slug":     note.Slug,
		"title":    note.Title,
		"path":     note.Path,
		"markdown": string(body),
		// version is the whole-file hash — the optimistic lock for PUT.
		"version": tasks.FileVersion(raw),
	})
}

// handleNoteWrite replaces a note's markdown body (frontmatter preserved)
// under the vault write lock with the same optimistic-lock contract as task
// writes.
func (s *Server) handleNoteWrite(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSession(w, r, "edit notes") {
		return
	}
	var req struct {
		Slug            string `json:"slug"`
		Markdown        string `json:"markdown"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || len(req.ExpectedVersion) != 64 {
		http.Error(w, "slug and expectedVersion are required", http.StatusBadRequest)
		return
	}
	if len(req.Markdown) > 4<<20 {
		http.Error(w, "note too large", http.StatusBadRequest)
		return
	}
	v := s.vault.Load()
	note, ok := v.BySlug(req.Slug)
	if !ok {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	if strings.HasPrefix(note.Path, "kanban/") {
		http.Error(w, "kanban notes are managed through the kanban API", http.StatusForbidden)
		return
	}
	if err := tasks.ReplaceNoteBodyAndReindex(v.Root, note.Path, []byte(req.Markdown), req.ExpectedVersion, s.rescanWhileVaultLocked); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}
