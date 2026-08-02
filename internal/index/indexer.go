package index

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

// renderVersion participates in change detection: bump it whenever the
// renderer's HTML output changes shape, and every note re-renders on the
// next reconcile without anyone having to delete the data dir.
const renderVersion = "9"

// versionedHash is a note's change-detection identity: its own content hash
// plus a fingerprint of the notes it directly transcludes, so a note's
// cached HTML is invalidated when an embedded note changes. Only direct
// embeds participate — cheap, deterministic, and covering what almost every
// transclusion inlines; deeper chains stay bounded by maxEmbedDepth.
func versionedHash(v *vault.Vault, n *vault.Note, salt string) string {
	base := n.Hash + "/" + renderVersion
	if salt != "" {
		base += "/" + salt
	}
	if len(n.Embeds) == 0 {
		return base
	}
	seen := map[string]bool{}
	var deps strings.Builder
	for _, target := range n.Embeds {
		dep, ok := v.Resolve(target)
		if !ok || dep.Slug == n.Slug || seen[dep.Slug] {
			continue
		}
		seen[dep.Slug] = true
		deps.WriteString(dep.Hash)
	}
	if deps.Len() == 0 {
		return base
	}
	sum := sha256.Sum256([]byte(deps.String()))
	return base + "+" + hex.EncodeToString(sum[:8])
}

// Reconcile brings the database in line with a vault snapshot. Notes whose
// hash is unchanged are skipped; new, changed, and deleted notes are
// (re)indexed. When the slug set changes (adds/renames/deletes), link
// resolution is global, so everything is re-rendered — the full corpus
// renders in under a second per thousand notes, serially, to bound memory.
func (d *DB) Reconcile(v *vault.Vault, e *markdown.Engine) error {
	start := time.Now()
	// Render inputs outside the notes themselves (kanban board names) take
	// part in change detection so cached HTML cannot go stale.
	salt := e.RenderSalt()

	type dbNote struct{ hash string }
	existing := map[string]dbNote{}
	rows, err := d.r.Query(`SELECT slug, hash FROM notes`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var slug, hash string
		if err := rows.Scan(&slug, &hash); err != nil {
			rows.Close()
			return err
		}
		existing[slug] = dbNote{hash: hash}
	}
	rows.Close()

	current := map[string]*vault.Note{}
	for _, n := range v.Notes {
		current[n.Slug] = n
	}

	var deleted []string
	for slug := range existing {
		if _, ok := current[slug]; !ok {
			deleted = append(deleted, slug)
		}
	}
	slugSetChanged := len(deleted) > 0
	var work []*vault.Note
	for _, n := range v.Notes {
		old, ok := existing[n.Slug]
		if !ok {
			slugSetChanged = true
			work = append(work, n)
		} else if old.hash != versionedHash(v, n, salt) {
			work = append(work, n)
		}
	}
	if slugSetChanged {
		work = v.Notes // re-render everything: resolution is global
	}
	if len(work) == 0 && len(deleted) == 0 {
		return nil
	}

	tx, err := d.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, slug := range deleted {
		if err := deleteNote(tx, slug); err != nil {
			return err
		}
	}
	for _, n := range work {
		body, err := v.ReadBody(n)
		if err != nil {
			slog.Warn("index read", "path", n.Path, "err", err)
			continue
		}
		res, err := e.RenderBody(v, n, body)
		if err != nil {
			slog.Warn("index render", "path", n.Path, "err", err)
			continue
		}
		noteTasks, fields := parseNoteTasks(n, body)
		if err := upsertNote(tx, v, n, res, noteTasks, fields, salt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	slog.Info("index reconcile", "rendered", len(work), "deleted", len(deleted),
		"full", slugSetChanged, "took", time.Since(start))
	return nil
}

func deleteNote(tx *sql.Tx, slug string) error {
	for _, q := range []string{
		`DELETE FROM notes WHERE slug=?`,
		`DELETE FROM links WHERE from_slug=?`,
		`DELETE FROM tags WHERE slug=?`,
		`DELETE FROM tasks WHERE slug=?`,
		`DELETE FROM inline_fields WHERE slug=?`,
		`DELETE FROM render_cache WHERE slug=?`,
		`DELETE FROM notes_fts WHERE slug=?`,
	} {
		if _, err := tx.Exec(q, slug); err != nil {
			return err
		}
	}
	return nil
}

func upsertNote(tx *sql.Tx, v *vault.Vault, n *vault.Note, res *markdown.Result, noteTasks []tasks.Task, fields []tasks.InlineField, salt string) error {
	if err := deleteNote(tx, n.Slug); err != nil {
		return err
	}

	fmJSON := frontmatterJSON(n.FM)
	tags := collectTags(n, res)
	tagsJSON, _ := json.Marshal(tags)
	aliasesJSON, _ := json.Marshal(orEmpty(n.FM.MetaStrings("aliases")))
	tocJSON, _ := json.Marshal(res.TOC)
	archived, archivedReason := archivedStatus(n, tags)

	if _, err := tx.Exec(`INSERT INTO notes(slug,path,title,frontmatter,tags,aliases,mtime,ctime,hash,wordcount,archived,archived_reason)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.Slug, n.Path, n.Title, fmJSON, string(tagsJSON), string(aliasesJSON),
		n.MTime.Unix(), ctimeUnix(n), versionedHash(v, n, salt), len(strings.Fields(res.Plain)),
		archived, archivedReason); err != nil {
		return err
	}

	seen := map[string]bool{}
	for _, l := range res.Links {
		key := l.Slug + "\x00" + l.Kind
		if seen[key] || l.Slug == n.Slug {
			continue
		}
		seen[key] = true
		if _, err := tx.Exec(`INSERT OR IGNORE INTO links(from_slug,to_slug,kind) VALUES(?,?,?)`,
			n.Slug, l.Slug, l.Kind); err != nil {
			return err
		}
	}
	for _, t := range tags {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tags(tag,slug) VALUES(?,?)`, t, n.Slug); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO render_cache(slug,hash,html,toc,rendered_at) VALUES(?,?,?,?,?)`,
		n.Slug, n.Hash, []byte(res.HTML), string(tocJSON), time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO notes_fts(slug,title,content,tags) VALUES(?,?,?,?)`,
		n.Slug, n.Title, res.Plain, strings.Join(tags, " ")); err != nil {
		return err
	}
	for _, t := range noteTasks {
		t.Version = n.Hash
		tagsJSON, _ := json.Marshal(orEmpty(t.Tags))
		if _, err := tx.Exec(`INSERT INTO tasks(slug,line,text,status,due,scheduled,start,recurrence,priority,done_date,tags,version)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			t.Slug, t.Line, t.Text, t.Status, t.Due, t.Scheduled, t.Start,
			t.Recurrence, t.Priority, t.DoneDate, string(tagsJSON), t.Version); err != nil {
			return err
		}
	}
	for _, f := range fields {
		if _, err := tx.Exec(`INSERT INTO inline_fields(slug,line,key,value) VALUES(?,?,?,?)`,
			n.Slug, f.Line, f.Key, f.Value); err != nil {
			return err
		}
	}
	return nil
}

// ctimeUnix prefers the frontmatter `created` date, falling back to mtime.
func ctimeUnix(n *vault.Note) int64 {
	if c := n.FM.MetaString("created"); c != "" {
		for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04"} {
			if t, err := time.Parse(layout, c); err == nil {
				return t.Unix()
			}
		}
	}
	return n.MTime.Unix()
}

func frontmatterJSON(fm vault.Frontmatter) string {
	if fm.Err != nil {
		b, _ := json.Marshal(map[string]any{"_raw": fm.Raw, "_error": fm.Err.Error()})
		return string(b)
	}
	if len(fm.Meta) == 0 {
		return "{}"
	}
	b, err := json.Marshal(fm.Meta)
	if err != nil {
		// YAML can produce map[any]any nests JSON can't encode.
		b, _ = json.Marshal(map[string]any{"_raw": fm.Raw})
	}
	return string(b)
}

func collectTags(n *vault.Note, res *markdown.Result) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range n.FM.MetaStrings("tags") {
		add(t)
	}
	for _, t := range n.FM.MetaStrings("tag") {
		add(t)
	}
	for _, t := range res.Tags {
		add(t)
	}
	return out
}

// archivedStatus reports whether a note counts as archived and why, so
// search results can show provenance. Checked in priority order: an
// explicit frontmatter `archived: true`, a frontmatter `status: archived`,
// then an `archive`/`archived` tag — which also covers kanban `done.md`
// boards, whose renderer tags completed cards `archive` (board/store.go).
func archivedStatus(n *vault.Note, tags []string) (archived bool, reason string) {
	if b, ok := n.FM.Meta["archived"].(bool); ok && b {
		return true, "frontmatter:archived"
	}
	if status := strings.ToLower(strings.TrimSpace(n.FM.MetaString("status"))); status == "archived" || status == "archive" {
		return true, "frontmatter:status"
	}
	for _, t := range tags {
		if t == "archive" || t == "archived" {
			return true, "tag:archive"
		}
	}
	return false, ""
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
