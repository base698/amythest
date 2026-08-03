package index

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/base698/amythest/internal/bases"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/tasks"
)

type NoteRef struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

type Page struct {
	Slug  string
	Title string
	HTML  template.HTML
	TOC   []markdown.Heading
	Tags  []string
}

var ErrNotFound = errors.New("not found")

// Page returns a cached rendered page.
func (d *DB) Page(slug string) (*Page, error) {
	var p Page
	var html []byte
	var tocJSON, tagsJSON string
	err := d.r.QueryRow(`SELECT n.slug, n.title, n.tags, c.html, c.toc
		FROM notes n JOIN render_cache c ON c.slug = n.slug
		WHERE n.slug = ?`, slug).
		Scan(&p.Slug, &p.Title, &tagsJSON, &html, &tocJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.HTML = template.HTML(html)
	_ = json.Unmarshal([]byte(tocJSON), &p.TOC)
	_ = json.Unmarshal([]byte(tagsJSON), &p.Tags)
	return &p, nil
}

// Backlinks returns notes that link (or embed) to slug.
func (d *DB) Backlinks(slug string) ([]NoteRef, error) {
	return d.refs(`SELECT DISTINCT n.slug, n.title FROM links l
		JOIN notes n ON n.slug = l.from_slug
		WHERE l.to_slug = ? ORDER BY n.title`, slug)
}

// TagNotes returns notes carrying a tag (or any nested tag under it).
func (d *DB) TagNotes(tag string) ([]NoteRef, error) {
	return d.refs(`SELECT DISTINCT n.slug, n.title FROM tags t
		JOIN notes n ON n.slug = t.slug
		WHERE t.tag = ? OR t.tag LIKE ? ORDER BY n.title`, tag, tag+"/%")
}

// Tags returns all tags with their note counts.
func (d *DB) Tags() (map[string]int, error) {
	rows, err := d.r.Query(`SELECT tag, COUNT(*) FROM tags GROUP BY tag ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var c int
		if err := rows.Scan(&t, &c); err != nil {
			return nil, err
		}
		out[t] = c
	}
	return out, rows.Err()
}

func (d *DB) refs(query string, args ...any) ([]NoteRef, error) {
	rows, err := d.r.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NoteRef
	for rows.Next() {
		var r NoteRef
		if err := rows.Scan(&r.Slug, &r.Title); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- content index (graph + explorer feed) ----

type ContentEntry struct {
	Title string   `json:"title"`
	Links []string `json:"links"`
	Tags  []string `json:"tags"`
}

// ContentIndex returns the slim map that feeds the client graph. No note
// text is included — search is server-side.
func (d *DB) ContentIndex() (map[string]*ContentEntry, error) {
	out := map[string]*ContentEntry{}
	rows, err := d.r.Query(`SELECT slug, title, tags FROM notes`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var slug, title, tagsJSON string
		if err := rows.Scan(&slug, &title, &tagsJSON); err != nil {
			rows.Close()
			return nil, err
		}
		e := &ContentEntry{Title: title, Links: []string{}}
		_ = json.Unmarshal([]byte(tagsJSON), &e.Tags)
		out[slug] = e
	}
	rows.Close()

	lrows, err := d.r.Query(`SELECT from_slug, to_slug FROM links`)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var from, to string
		if err := lrows.Scan(&from, &to); err != nil {
			return nil, err
		}
		if e, ok := out[from]; ok {
			if _, exists := out[to]; exists {
				e.Links = append(e.Links, to)
			}
		}
	}
	return out, lrows.Err()
}

// ---- search ----

type SearchResult struct {
	Slug           string  `json:"slug"`
	Title          string  `json:"title"`
	Excerpt        string  `json:"excerpt"` // plain text; \x02/\x03 bracket each match highlight
	Archived       bool    `json:"archived"`
	ArchivedReason string  `json:"archived_reason,omitempty"` // provenance, e.g. "tag:archive"
	Score          float64 `json:"-"`
}

// Search runs an FTS5 match with title boosting and snippet extraction.
// The query is user text; it is converted into a prefix-match FTS query.
// Archived notes are excluded unless includeArchived is set, so the
// default result set matches today's behavior.
func (d *DB) Search(q string, limit int, includeArchived bool) ([]SearchResult, error) {
	ftsQuery := buildFTSQuery(q)
	if ftsQuery == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	where := `notes_fts MATCH ?`
	if !includeArchived {
		where += ` AND n.archived = 0`
	}
	rows, err := d.r.Query(`SELECT notes_fts.slug, notes_fts.title,
			snippet(notes_fts, 2, char(2), char(3), '…', 12),
			bm25(notes_fts, 0, 10.0, 1.0, 5.0),
			n.archived, n.archived_reason
		FROM notes_fts JOIN notes n ON n.slug = notes_fts.slug
		WHERE `+where+`
		ORDER BY bm25(notes_fts, 0, 10.0, 1.0, 5.0) LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("fts: %w", err)
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Slug, &r.Title, &r.Excerpt, &r.Score, &r.Archived, &r.ArchivedReason); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// buildFTSQuery quotes each term and adds prefix matching to the last one,
// so typing feels incremental. `#tag` restricts to the tags column.
func buildFTSQuery(q string) string {
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return ""
	}
	var parts []string
	for i, f := range fields {
		col := ""
		if strings.HasPrefix(f, "#") {
			col = "tags:"
			f = strings.TrimPrefix(f, "#")
		}
		f = strings.ReplaceAll(f, `"`, "")
		if f == "" {
			continue
		}
		if i == len(fields)-1 {
			parts = append(parts, col+`"`+f+`"*`)
		} else {
			parts = append(parts, col+`"`+f+`"`)
		}
	}
	return strings.Join(parts, " ")
}

// ---- tasks ----

// AllTasks returns every indexed task with its source path.
func (d *DB) AllTasks() ([]tasks.Task, error) {
	rows, err := d.r.Query(`SELECT t.slug, n.path, t.line, t.text, t.status,
		t.due, t.scheduled, t.start, t.recurrence, t.priority, t.done_date, t.tags, t.version
		FROM tasks t JOIN notes n ON n.slug = t.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tasks.Task
	for rows.Next() {
		var t tasks.Task
		var tagsJSON string
		var due, sched, start, rec, done sql.NullString
		if err := rows.Scan(&t.Slug, &t.Path, &t.Line, &t.Text, &t.Status,
			&due, &sched, &start, &rec, &t.Priority, &done, &tagsJSON, &t.Version); err != nil {
			return nil, err
		}
		t.Due, t.Scheduled, t.Start, t.Recurrence, t.DoneDate =
			due.String, sched.String, start.String, rec.String, done.String
		_ = json.Unmarshal([]byte(tagsJSON), &t.Tags)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- bases rows ----

// RowsForSource loads bases rows for a base's declared source.
func (d *DB) RowsForSource(source string) ([]*bases.Row, error) {
	switch source {
	case "", "notes":
		return d.AllRows()
	case "tasks":
		return d.TaskRows()
	case "items":
		return d.ItemRows()
	}
	return nil, fmt.Errorf("unknown base source %q (want notes, tasks, or items)", source)
}

// TaskRows exposes every indexed task as a bases row: task metadata becomes
// note.* properties, file.* comes from the source note.
func (d *DB) TaskRows() ([]*bases.Row, error) {
	rows, err := d.r.Query(`SELECT t.slug, n.path, t.line, t.text, t.status,
		t.due, t.scheduled, t.start, t.recurrence, t.priority, t.done_date, t.tags,
		n.mtime, n.ctime
		FROM tasks t JOIN notes n ON n.slug = t.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*bases.Row
	for rows.Next() {
		var slug, path, text, status, tagsJSON string
		var line, priority int
		var due, sched, start, rec, done sql.NullString
		var mtime, ctime sql.NullInt64
		if err := rows.Scan(&slug, &path, &line, &text, &status,
			&due, &sched, &start, &rec, &priority, &done, &tagsJSON, &mtime, &ctime); err != nil {
			return nil, err
		}
		r := &bases.Row{
			Slug: slug, Path: path, Title: text,
			Frontmatter: map[string]any{
				"text": text, "status": status, "line": line,
				"due": due.String, "scheduled": sched.String, "start": start.String,
				"recurrence": rec.String, "priority": priority, "done": done.String,
			},
		}
		_ = json.Unmarshal([]byte(tagsJSON), &r.Tags)
		if mtime.Valid {
			r.MTime = time.Unix(mtime.Int64, 0)
		}
		if ctime.Valid && ctime.Int64 > 0 {
			r.CTime = time.Unix(ctime.Int64, 0)
		} else {
			r.CTime = r.MTime
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ItemRows exposes each list item as a bases row: note.text is the bullet
// text, note.task the checkbox status ("" for plain bullets), and any
// [Key:: value] inline fields on the item (including its indented
// continuation lines) become note.* properties — numeric when they parse
// as numbers. file.* and tags come from the note; Title is the item text.
func (d *DB) ItemRows() ([]*bases.Row, error) {
	type key struct {
		slug string
		line int
	}
	index := map[key]*bases.Row{}
	var out []*bases.Row

	rows, err := d.r.Query(`SELECT i.slug, i.line, i.text, i.status,
		n.path, n.title, n.tags, n.mtime, n.ctime
		FROM list_items i JOIN notes n ON n.slug = i.slug
		ORDER BY i.slug, i.line`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var slug, text, status, path, title, tagsJSON string
		var line int
		var mtime, ctime sql.NullInt64
		if err := rows.Scan(&slug, &line, &text, &status, &path, &title, &tagsJSON, &mtime, &ctime); err != nil {
			return nil, err
		}
		r := &bases.Row{
			Slug: slug, Path: path, Title: text,
			Frontmatter: map[string]any{"line": line, "text": text, "task": status, "note_title": title},
		}
		if text == "" {
			r.Title = title
		}
		_ = json.Unmarshal([]byte(tagsJSON), &r.Tags)
		if mtime.Valid {
			r.MTime = time.Unix(mtime.Int64, 0)
		}
		if ctime.Valid && ctime.Int64 > 0 {
			r.CTime = time.Unix(ctime.Int64, 0)
		} else {
			r.CTime = r.MTime
		}
		index[key{slug, line}] = r
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	frows, err := d.r.Query(`SELECT f.slug, f.line, f.key, f.value,
		n.path, n.title, n.tags, n.mtime, n.ctime
		FROM inline_fields f JOIN notes n ON n.slug = f.slug
		ORDER BY f.slug, f.line`)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var slug, fkey, value, path, title, tagsJSON string
		var line int
		var mtime, ctime sql.NullInt64
		if err := frows.Scan(&slug, &line, &fkey, &value, &path, &title, &tagsJSON, &mtime, &ctime); err != nil {
			return nil, err
		}
		r, ok := index[key{slug, line}]
		if !ok {
			// fields outside any list item (paragraph lines) still get a row
			r = &bases.Row{
				Slug: slug, Path: path, Title: title,
				Frontmatter: map[string]any{"line": line, "text": "", "task": "", "note_title": title},
			}
			_ = json.Unmarshal([]byte(tagsJSON), &r.Tags)
			if mtime.Valid {
				r.MTime = time.Unix(mtime.Int64, 0)
			}
			if ctime.Valid && ctime.Int64 > 0 {
				r.CTime = time.Unix(ctime.Int64, 0)
			} else {
				r.CTime = r.MTime
			}
			index[key{slug, line}] = r
			out = append(out, r)
		}
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			r.Frontmatter[fkey] = f
		} else {
			r.Frontmatter[fkey] = value
		}
	}
	return out, frows.Err()
}

// AllRows loads every note's metadata for the bases engine.
func (d *DB) AllRows() ([]*bases.Row, error) {
	rows, err := d.r.Query(`SELECT slug, path, title, frontmatter, tags, mtime, ctime FROM notes`)
	if err != nil {
		return nil, err
	}
	var out []*bases.Row
	index := map[string]*bases.Row{}
	for rows.Next() {
		r := &bases.Row{}
		var fmJSON, tagsJSON string
		var mtime, ctime sql.NullInt64
		if err := rows.Scan(&r.Slug, &r.Path, &r.Title, &fmJSON, &tagsJSON, &mtime, &ctime); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(fmJSON), &r.Frontmatter)
		_ = json.Unmarshal([]byte(tagsJSON), &r.Tags)
		if mtime.Valid {
			r.MTime = time.Unix(mtime.Int64, 0)
		}
		if ctime.Valid && ctime.Int64 > 0 {
			r.CTime = time.Unix(ctime.Int64, 0)
		} else {
			r.CTime = r.MTime
		}
		out = append(out, r)
		index[r.Slug] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	lrows, err := d.r.Query(`SELECT from_slug, to_slug FROM links`)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var from, to string
		if err := lrows.Scan(&from, &to); err != nil {
			return nil, err
		}
		if r, ok := index[from]; ok {
			r.Links = append(r.Links, to)
		}
	}
	return out, lrows.Err()
}
