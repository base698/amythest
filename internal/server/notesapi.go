package server

import (
	"net/http"
	"os"
	"sort"
	"strconv"

	"github.com/base698/amythest/internal/bases"
)

// handleNotesList returns the vault's note listing for API clients (the amy
// browser view): identity plus the sort keys the UI needs. Read-only and
// unauthenticated, like note pages and contentIndex.
func (s *Server) handleNotesList(w http.ResponseWriter, r *http.Request) {
	v := s.vault.Load()
	type entry struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
		Path  string `json:"path"`
		MTime int64  `json:"mtime"` // unix seconds
		Size  int64  `json:"size"`
	}
	out := make([]entry, 0, len(v.Notes))
	for _, n := range v.Notes {
		out = append(out, entry{Slug: n.Slug, Title: n.Title, Path: n.Path, MTime: n.MTime.Unix(), Size: n.Size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.writeJSON(w, out)
}

// handleBasesList returns the vault's .base files by name.
func (s *Server) handleBasesList(w http.ResponseWriter, r *http.Request) {
	v := s.vault.Load()
	names := make([]string, 0, len(v.Bases))
	for name := range v.Bases {
		names = append(names, name)
	}
	sort.Strings(names)
	s.writeJSON(w, names)
}

// handleBaseData evaluates one view of a base to JSON — the same ViewData
// the MCP query_base tool returns, plus the base's view names so a client
// can cycle views.
func (s *Server) handleBaseData(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}
	viewIdx := 0
	if raw := r.URL.Query().Get("view"); raw != "" {
		idx, err := strconv.Atoi(raw)
		if err != nil || idx < 0 {
			http.Error(w, "view must be a non-negative index", http.StatusBadRequest)
			return
		}
		viewIdx = idx
	}

	v := s.vault.Load()
	rel, ok := v.Bases[name]
	if !ok {
		http.Error(w, "base not found", http.StatusNotFound)
		return
	}
	abs, ok := v.AssetPath(rel)
	if !ok {
		http.Error(w, "base not found", http.StatusNotFound)
		return
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	base, err := bases.ParseBase(raw)
	if err != nil {
		http.Error(w, "parse base: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	rows, err := s.db.RowsForSource(base.Source)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := base.Data(rows, viewIdx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	views := make([]string, 0, len(base.Views))
	for _, view := range base.Views {
		label := view.Name
		if label == "" {
			label = view.Type
		}
		views = append(views, label)
	}
	s.writeJSON(w, map[string]any{"name": name, "views": views, "data": data})
}
