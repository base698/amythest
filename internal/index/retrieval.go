package index

import (
	"database/sql"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

type SearchOptions struct {
	Limit                    int
	IncludeArchived          bool
	StartDate, EndDate, Kind string
}

var dateInName = regexp.MustCompile(`(?:^|[/ _-])(\d{4}-\d{2}-\d{2})(?:$|[^0-9])`)

func sourceDate(n *vault.Note) (string, string) {
	for _, key := range []string{"date", "created", "created_at"} {
		value := n.FM.Meta[key]
		if t, ok := value.(time.Time); ok {
			return t.UTC().Format("2006-01-02"), "frontmatter:" + key
		}
		if s, ok := value.(string); ok && len(s) >= 10 {
			if _, err := time.Parse("2006-01-02", s[:10]); err == nil {
				return s[:10], "frontmatter:" + key
			}
		}
	}
	if m := dateInName.FindStringSubmatch(n.Path); len(m) > 1 {
		if _, err := time.Parse("2006-01-02", m[1]); err == nil {
			return m[1], "filename"
		}
	}
	return n.MTime.UTC().Format("2006-01-02"), "file_modified"
}
func relativeNoteURL(slug string) string {
	parts := strings.Split(slug, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
func insertDocument(tx *sql.Tx, n *vault.Note, slug, title, content, tags, kind, date, dateSource, link string, archived bool, reason string) error {
	_, err := tx.Exec(`INSERT INTO retrieval_fts(parent_slug,slug,title,content,tags,kind,source_date,date_source,url,archived,archived_reason) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, n.Slug, slug, title, content, tags, kind, date, dateSource, link, archived, reason)
	return err
}
func indexRetrieval(tx *sql.Tx, v *vault.Vault, n *vault.Note, plain string, tags []string, noteTasks []tasks.Task, archived bool, reason string) error {
	date, dateSource := sourceDate(n)
	if strings.HasPrefix(n.Path, "kanban/") && (path.Base(n.Path) == "board.md" || path.Base(n.Path) == "done.md") {
		body, err := v.ReadBody(n)
		if err != nil {
			return err
		}
		raw := []byte("---\n" + n.FM.Raw + "\n---\n" + string(body))
		b, err := board.ParseSnapshot(raw, n.Path)
		if err != nil {
			return fmt.Errorf("index board %s: %w", n.Path, err)
		}
		for _, c := range b.Cards {
			cd, ds := date, dateSource
			if !c.UpdatedAt.IsZero() {
				cd = c.UpdatedAt.UTC().Format("2006-01-02")
				ds = "card_updated"
			}
			content := c.Description
			for _, comment := range c.Comments {
				content += "\n" + comment.Body
			}
			done := archived || b.Archived || c.Status == board.Done || path.Base(n.Path) == "done.md"
			why := ""
			if done {
				why = "kanban:done-or-archived"
			}
			link := "kanban/" + url.PathEscape(b.Name) + "#card-" + url.PathEscape(c.ID)
			// The note reader provides a stable source link even for archived cards.
			if done {
				link = relativeNoteURL(n.Slug) + "#" + url.PathEscape(c.ID)
			}
			if err := insertDocument(tx, n, n.Slug, c.Title, content, strings.Join(c.Labels, " "), "card", cd, ds, link, done, why); err != nil {
				return err
			}
		}
		return nil
	}
	kind := "note"
	if n.FM.MetaString("job_id") != "" || strings.HasPrefix(strings.ToLower(n.Path), "jobs/") {
		kind = "job"
	}
	content := plain + "\n" + strings.Join(n.FM.MetaStrings("aliases"), " ")
	if err := insertDocument(tx, n, n.Slug, n.Title, content, strings.Join(tags, " "), kind, date, dateSource, relativeNoteURL(n.Slug), archived, reason); err != nil {
		return err
	}
	for _, t := range noteTasks {
		if err := insertDocument(tx, n, n.Slug, t.Text, t.Text, strings.Join(t.Tags, " "), "task", date, dateSource, relativeNoteURL(n.Slug), archived || t.Status == tasks.StatusDone, reason); err != nil {
			return err
		}
	}
	return nil
}

// Expansion is deliberately inspectable and limited to lexical associations;
// it does not infer relationships or manufacture a synthesized memory.
var relatedTerms = [][]string{
	{"robotics", "robot", "ratbot", "rospider", "trace walking", "computer vision"},
	{"vision", "camera"}, {"gatech", "georgia tech"},
}

func retrievalQuery(query string) string {
	parts := []string{}
	for _, word := range strings.Fields(query) {
		tag := strings.HasPrefix(word, "#")
		word = strings.ReplaceAll(strings.TrimPrefix(word, "#"), `"`, "")
		if word == "" {
			continue
		}
		quote := func(w string) string { return `"` + strings.ReplaceAll(w, `"`, "") + `"*` }
		if tag {
			parts = append(parts, "tags:"+quote(word))
			continue
		}
		terms := []string{quote(word)}
		for _, group := range relatedTerms {
			found := false
			for _, term := range group {
				if strings.EqualFold(term, word) {
					found = true
				}
			}
			if found {
				for _, term := range group {
					if !strings.EqualFold(term, word) {
						terms = append(terms, quote(term))
					}
				}
			}
		}
		parts = append(parts, "("+strings.Join(terms, " OR ")+")")
	}
	return strings.Join(parts, " AND ")
}
func (d *DB) SearchWithOptions(q string, o SearchOptions) ([]SearchResult, error) {
	words := []string{}
	for _, word := range strings.Fields(q) {
		switch {
		case strings.HasPrefix(word, "after:"):
			o.StartDate = strings.TrimPrefix(word, "after:")
		case strings.HasPrefix(word, "before:"):
			o.EndDate = strings.TrimPrefix(word, "before:")
		case strings.HasPrefix(word, "type:"):
			o.Kind = strings.TrimPrefix(word, "type:")
		default:
			words = append(words, word)
		}
	}
	for _, date := range []string{o.StartDate, o.EndDate} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return nil, fmt.Errorf("invalid date: use YYYY-MM-DD")
			}
		}
	}
	if o.StartDate != "" && o.EndDate != "" && o.StartDate > o.EndDate {
		return nil, fmt.Errorf("start date is after end date")
	}
	if o.Kind != "" && o.Kind != "note" && o.Kind != "card" && o.Kind != "task" && o.Kind != "job" {
		return nil, fmt.Errorf("invalid source type")
	}
	if o.Limit <= 0 || o.Limit > 50 {
		o.Limit = 10
	}
	where := []string{"1=1"}
	args := []any{}
	query := retrievalQuery(strings.Join(words, " "))
	excerpt := "substr(content,1,240)"
	score := "0.0"
	order := "source_date DESC,title"
	if query != "" {
		where = append(where, "retrieval_fts MATCH ?")
		args = append(args, query)
		excerpt = "snippet(retrieval_fts,3,char(2),char(3),'…',32)"
		score = "bm25(retrieval_fts,0,0,10,1,5)"
		order = score
	}
	if query == "" && o.StartDate == "" && o.EndDate == "" && o.Kind == "" {
		return nil, nil
	}
	if !o.IncludeArchived {
		where = append(where, "archived=0")
	}
	if o.StartDate != "" {
		where = append(where, "source_date>=?")
		args = append(args, o.StartDate)
	}
	if o.EndDate != "" {
		where = append(where, "source_date<=?")
		args = append(args, o.EndDate)
	}
	if o.Kind != "" {
		where = append(where, "kind=?")
		args = append(args, o.Kind)
	}
	args = append(args, o.Limit)
	rows, err := d.r.Query(`SELECT slug,title,`+excerpt+`,`+score+`,archived,archived_reason,kind,source_date,date_source,url FROM retrieval_fts WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchResult{}
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Slug, &r.Title, &r.Excerpt, &r.Score, &r.Archived, &r.ArchivedReason, &r.Kind, &r.Date, &r.DateSource, &r.URL); err != nil {
			return nil, err
		}
		r.MatchMethod = "stemmed lexical with related terms"
		out = append(out, r)
	}
	return out, rows.Err()
}
