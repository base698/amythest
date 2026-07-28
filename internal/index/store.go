package index

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
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
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Excerpt string  `json:"excerpt"` // HTML with <b> highlights
	Score   float64 `json:"-"`
}

// Search runs an FTS5 match with title boosting and snippet extraction.
// The query is user text; it is converted into a prefix-match FTS query.
func (d *DB) Search(q string, limit int) ([]SearchResult, error) {
	ftsQuery := buildFTSQuery(q)
	if ftsQuery == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := d.r.Query(`SELECT slug, title,
			snippet(notes_fts, 2, '<b>', '</b>', '…', 12),
			bm25(notes_fts, 0, 10.0, 1.0, 5.0)
		FROM notes_fts WHERE notes_fts MATCH ?
		ORDER BY bm25(notes_fts, 0, 10.0, 1.0, 5.0) LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("fts: %w", err)
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Slug, &r.Title, &r.Excerpt, &r.Score); err != nil {
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
		t.due, t.scheduled, t.start, t.recurrence, t.priority, t.done_date, t.tags
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
			&due, &sched, &start, &rec, &t.Priority, &done, &tagsJSON); err != nil {
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
