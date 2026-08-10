package server

import (
	"net/http"
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
	body, err := v.ReadBody(note)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]string{
		"slug":     note.Slug,
		"title":    note.Title,
		"path":     note.Path,
		"markdown": string(body),
	})
}
