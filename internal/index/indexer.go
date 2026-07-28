package index

import (
	"database/sql"
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
const renderVersion = "6"

func versionedHash(n *vault.Note) string { return n.Hash + "/" + renderVersion }

// Reconcile brings the database in line with a vault snapshot. Notes whose
// hash is unchanged are skipped; new, changed, and deleted notes are
// (re)indexed. When the slug set changes (adds/renames/deletes), link
// resolution is global, so everything is re-rendered — the full corpus
// renders in under a second per thousand notes, serially, to bound memory.
func (d *DB) Reconcile(v *vault.Vault, e *markdown.Engine) error {
	start := time.Now()

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
		} else if old.hash != versionedHash(n) {
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
		var noteTasks []tasks.Task
		var fields []tasks.InlineField
		// Kanban boards render their cards as checkboxes; those are reachable
		// through the kanban API/tools and would double-count as tasks.
		if !strings.HasPrefix(n.Path, "kanban/") {
			noteTasks, fields = tasks.ParseFile(n.Slug, n.Path, body)
		}
		if err := upsertNote(tx, n, res, noteTasks, fields); err != nil {
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

func upsertNote(tx *sql.Tx, n *vault.Note, res *markdown.Result, noteTasks []tasks.Task, fields []tasks.InlineField) error {
	if err := deleteNote(tx, n.Slug); err != nil {
		return err
	}

	fmJSON := frontmatterJSON(n.FM)
	tags := collectTags(n, res)
	tagsJSON, _ := json.Marshal(tags)
	aliasesJSON, _ := json.Marshal(orEmpty(n.FM.MetaStrings("aliases")))
	tocJSON, _ := json.Marshal(res.TOC)

	if _, err := tx.Exec(`INSERT INTO notes(slug,path,title,frontmatter,tags,aliases,mtime,ctime,hash,wordcount)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		n.Slug, n.Path, n.Title, fmJSON, string(tagsJSON), string(aliasesJSON),
		n.MTime.Unix(), ctimeUnix(n), versionedHash(n), len(strings.Fields(res.Plain))); err != nil {
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
		tagsJSON, _ := json.Marshal(orEmpty(t.Tags))
		if _, err := tx.Exec(`INSERT INTO tasks(slug,line,text,status,due,scheduled,start,recurrence,priority,done_date,tags)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			t.Slug, t.Line, t.Text, t.Status, t.Due, t.Scheduled, t.Start,
			t.Recurrence, t.Priority, t.DoneDate, string(tagsJSON)); err != nil {
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

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
