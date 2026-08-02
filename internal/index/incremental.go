package index

import (
	"strings"

	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

// IndexNote renders and (re)indexes exactly one note against the given vault
// snapshot, without walking the vault or touching any other note.
//
// It exists for writers that create a note and must serve it on the very next
// request — share uploads return a noteURL the client navigates to
// immediately. Reconcile is the general path and is deliberately global: a new
// slug can change link resolution everywhere, so it re-renders the whole
// corpus. That is the right answer for freshness and the wrong answer for
// latency, so mutations that know their own blast radius index their note here
// and leave the global pass to a background Reconcile.
func (d *DB) IndexNote(v *vault.Vault, e *markdown.Engine, n *vault.Note) error {
	body, err := v.ReadBody(n)
	if err != nil {
		return err
	}
	res, err := e.RenderBody(v, n, body)
	if err != nil {
		return err
	}
	noteTasks, fields := parseNoteTasks(n, body)

	tx, err := d.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertNote(tx, v, n, res, noteTasks, fields, e.RenderSalt()); err != nil {
		return err
	}
	return tx.Commit()
}

// InvalidateRenderCache clears every note's stored change-detection hash, so
// the next Reconcile re-renders the whole corpus. IndexNote writes a
// fully-valid row for its note, which would otherwise let a later Reconcile
// conclude nothing changed and skip the global re-render that a new slug
// demands (a pre-existing [[Link]] to the new note stays broken in cached
// HTML). Callers pair the two: index the note now, invalidate + reconcile in
// the background.
func (d *DB) InvalidateRenderCache() error {
	_, err := d.w.Exec(`UPDATE notes SET hash = ''`)
	return err
}

// parseNoteTasks extracts a note's tasks and inline fields, honouring the two
// opt-outs. Kanban boards render their cards as checkboxes; those are
// reachable through the kanban API/tools and would double-count as tasks.
// Notes may also opt out with the case-sensitive canonical boolean
// `tasks: false`; the note itself is still rendered, linked, and indexed for
// search.
func parseNoteTasks(n *vault.Note, body []byte) ([]tasks.Task, []tasks.InlineField) {
	enabled, isBool := n.FM.Meta["tasks"].(bool)
	if (isBool && !enabled) || strings.HasPrefix(n.Path, "kanban/") {
		return nil, nil
	}
	return tasks.ParseFile(n.Slug, n.Path, body)
}
