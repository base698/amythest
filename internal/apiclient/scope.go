package apiclient

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
)

// Scope is a local browsing filter, not an authorization boundary. Explicit
// note links and mutations retain their native identities.
type Scope struct {
	Name    string   `yaml:"-"`
	Boards  []string `yaml:"boards"`
	Folders []string `yaml:"folders"`
	Tags    []string `yaml:"tags"`
	Project string   `yaml:"project"`
}

func (cfg Config) Workspace(name string) Scope {
	s := Scope{Name: strings.TrimSpace(name)}
	for key, value := range cfg.Workspaces {
		if strings.EqualFold(key, s.Name) {
			value.Name = s.Name
			s = value
			break
		}
	}
	if s.Project == "" {
		s.Project = s.Name
	}
	if len(s.Tags) == 0 {
		s.Tags = []string{strings.ReplaceAll(strings.ToLower(s.Name), " ", "-")}
	}
	return s
}

// Scoped makes an independent client so in-flight loads keep their original
// scope when the dashboard switches between this workspace and All.
func (c *Client) Scoped(s Scope) *Client {
	out := New(c.cfg)
	out.http = c.http
	out.scope = &s
	return out
}

func equalAny(value string, choices []string) bool {
	for _, choice := range choices {
		if strings.EqualFold(value, choice) {
			return true
		}
	}
	return false
}
func (s Scope) board(b board.BoardSummary) bool {
	if len(s.Boards) > 0 {
		return equalAny(b.Name, s.Boards)
	}
	return strings.EqualFold(b.Name, s.Name) || strings.EqualFold(b.DisplayName, s.Name) || strings.EqualFold(b.Workspace, s.Name)
}
func (s Scope) note(path string, tags []string) bool {
	path = strings.ToLower(strings.Trim(strings.ReplaceAll(path, "\\", "/"), "/"))
	for _, folder := range s.Folders {
		folder = strings.ToLower(strings.Trim(folder, "/"))
		if folder != "" && (path == folder || strings.HasPrefix(path, folder+"/")) {
			return true
		}
	}
	if len(s.Folders) == 0 {
		for _, segment := range strings.Split(path, "/") {
			if strings.EqualFold(strings.TrimSuffix(segment, ".md"), s.Name) {
				return true
			}
		}
	}
	for _, tag := range tags {
		if equalAny(strings.TrimPrefix(tag, "#"), s.Tags) {
			return true
		}
	}
	return false
}
func (c *Client) scopeBoards(boards []board.BoardSummary) []board.BoardSummary {
	if c.scope == nil {
		return boards
	}
	out := make([]board.BoardSummary, 0)
	for _, b := range boards {
		if c.scope.board(b) {
			out = append(out, b)
		}
	}
	return out
}
func (c *Client) scopeTasks(ctx context.Context, groups []TaskGroup) ([]TaskGroup, error) {
	if c.scope == nil {
		return groups, nil
	}
	boards, err := c.ListBoards(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TaskGroup, 0)
	for _, group := range groups {
		filtered := TaskGroup{Name: group.Name, Tasks: []tasks.Task{}}
		for _, task := range group.Tasks {
			allowed := c.scope.note(task.Path, task.Tags)
			for _, b := range boards {
				if strings.HasPrefix(task.Path, "kanban/"+b.Name+"/") {
					allowed = true
				}
			}
			if allowed {
				filtered.Tasks = append(filtered.Tasks, task)
			}
		}
		if len(filtered.Tasks) > 0 {
			out = append(out, filtered)
		}
	}
	return out, nil
}
func (c *Client) scopeNotes(ctx context.Context, notes []NoteEntry) ([]NoteEntry, error) {
	if c.scope == nil {
		return notes, nil
	}
	index, err := c.ContentIndex(ctx)
	if err != nil {
		return nil, err
	}
	boards, err := c.ListBoards(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NoteEntry, 0)
	for _, note := range notes {
		allowed := c.scope.note(note.Path, index[note.Slug].Tags)
		for _, b := range boards {
			if strings.HasPrefix(note.Path, "kanban/"+b.Name+"/") {
				allowed = true
			}
		}
		if allowed {
			out = append(out, note)
		}
	}
	return out, nil
}
func (c *Client) scopedSlugs(ctx context.Context) (map[string]bool, error) {
	notes, err := c.ListNotes(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, n := range notes {
		out[n.Slug] = true
	}
	return out, nil
}

// Restrict retrieval before ranking/limiting, so unrelated matches cannot
// crowd this workspace's results out of the server's first page.
func (c *Client) searchPath(ctx context.Context, query string) (string, error) {
	path := "/api/search?q=" + url.QueryEscape(query)
	if c.scope == nil {
		return path, nil
	}
	allowed, err := c.scopedSlugs(ctx)
	if err != nil {
		return "", err
	}
	slugs := make([]string, 0, len(allowed))
	for slug := range allowed {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	raw, _ := json.Marshal(slugs)
	return path + "&scope_slugs=" + url.QueryEscape(string(raw)), nil
}
func (c *Client) scopeSearch(ctx context.Context, results []SearchResult) ([]SearchResult, error) {
	if c.scope == nil {
		return results, nil
	}
	allowed, err := c.scopedSlugs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0)
	for _, r := range results {
		if allowed[r.Slug] {
			out = append(out, r)
		}
	}
	return out, nil
}
func (c *Client) scopeVault(ctx context.Context, results []VaultHit) ([]VaultHit, error) {
	if c.scope == nil {
		return results, nil
	}
	allowed, err := c.scopedSlugs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]VaultHit, 0)
	for _, r := range results {
		if allowed[r.Slug] {
			out = append(out, r)
		}
	}
	return out, nil
}

// MatchesBoard reports whether a board belongs to this workspace.
func (s Scope) MatchesBoard(b board.BoardSummary) bool { return s.board(b) }
