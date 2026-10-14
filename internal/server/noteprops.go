package server

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/base698/amythest/internal/tasks"
)

var propKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var noteVersionRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
var editablePropTypes = map[string]bool{"": true, "string": true, "bool": true, "number": true, "date": true, "null": true}

// handleNoteProperty sets one top-level frontmatter key on a note to a
// scalar value (typed by YAML inference). Same auth gate, vault lookup,
// kanban exclusion, locked write, and immediate reindex as task mutations.
func (s *Server) handleNoteProperty(w http.ResponseWriter, r *http.Request) {
	if !s.requireKanbanSessionCSRF(w, r, "edit note properties") {
		return
	}

	var req struct {
		Slug            string `json:"slug"`
		Key             string `json:"key"`
		Value           string `json:"value"`
		Type            string `json:"type"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || len(req.Slug) > 512 || !propKeyRe.MatchString(req.Key) || len(req.Value) > 10_000 || !editablePropTypes[req.Type] || (req.Type == "null" && req.Value != "") || !noteVersionRe.MatchString(req.ExpectedVersion) {
		http.Error(w, "slug, expectedVersion, a simple property key, an editable scalar type, and a value of at most 10000 characters are required", http.StatusBadRequest)
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
	if err := tasks.SetNotePropertyTypedAtVersionAndReindex(v.Root, n.Path, req.Key, req.Value, req.Type, req.ExpectedVersion, s.rescanWhileVaultLocked); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "key": req.Key, "value": req.Value})
}
