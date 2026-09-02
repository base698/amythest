// Package azboards backs amy's "virtual boards" with Azure Boards via the
// az CLI (azure-devops extension). A virtual board is WIQL + AreaPath +
// work item type; columns are System.State values, and moving a card
// between columns is a state transition.
//
// Auth is az's own two-layer story (az login + az devops login / PAT); this
// package only detects the logged-out condition and surfaces the fix. Work
// items are cached in memory (list per board, detail per id) so navigation
// does not re-query; mutations invalidate.
package azboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/base698/amythest/internal/source"
)

type BoardConfig struct {
	Name    string   `yaml:"name"`
	Area    string   `yaml:"area"`    // System.AreaPath, e.g. My Project\Team
	Type    string   `yaml:"type"`    // work item type; wrong type = zero rows
	Columns []string `yaml:"columns"` // state order; discovered when empty
}

type Config struct {
	Org     string        `yaml:"org"`     // https://dev.azure.com/<org>
	Project string        `yaml:"project"` //
	Boards  []BoardConfig `yaml:"boards"`
}

type sourcesFile struct {
	Sources struct {
		AZBoards *Config `yaml:"azboards"`
	} `yaml:"sources"`
}

// LoadConfig parses sources.azboards out of cli.yaml. ok=false when absent.
func LoadConfig(path string) (Config, bool, error) {
	if path == "" {
		return Config{}, false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, false, nil
	}
	var sf sourcesFile
	if err := yaml.Unmarshal(raw, &sf); err != nil {
		return Config{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if sf.Sources.AZBoards == nil {
		return Config{}, false, nil
	}
	cfg := *sf.Sources.AZBoards
	if cfg.Org == "" || cfg.Project == "" {
		return Config{}, false, errors.New("sources.azboards needs org and project")
	}
	return cfg, true, nil
}

type WorkItem struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	State        string `json:"state"`
	Assignee     string `json:"assigned"`
	CommentCount int    `json:"comments"`
	Description  string `json:"description"`
}

// ErrNotLoggedIn is the surfaced auth failure; the UI renders the fix.
var ErrNotLoggedIn = errors.New("not logged in to Azure DevOps")

const (
	listTTL   = 60 * time.Second
	detailTTL = 5 * time.Minute
)

type cachedList struct {
	items []WorkItem
	at    time.Time
}
type cachedItem struct {
	item WorkItem
	at   time.Time
}

type Source struct {
	cfg    Config
	azPath string // "az" unless overridden (tests)
	now    func() time.Time

	mu    sync.Mutex
	lists map[string]cachedList
	items map[int]cachedItem
}

func New(cfg Config) *Source {
	return &Source{cfg: cfg, azPath: "az", now: time.Now,
		lists: map[string]cachedList{}, items: map[int]cachedItem{}}
}

func (s *Source) Name() string          { return "azboards" }
func (s *Source) Config() Config        { return s.cfg }
func (s *Source) Boards() []BoardConfig { return s.cfg.Boards }

// WebURL is the work item's Azure Boards page.
func (s *Source) WebURL(id int) string {
	return strings.TrimRight(s.cfg.Org, "/") + "/" + url.PathEscape(s.cfg.Project) +
		"/_workitems/edit/" + fmt.Sprint(id)
}

// run executes az with JSON output, translating auth failures.
func (s *Source) run(ctx context.Context, args ...string) ([]byte, error) {
	path := s.azPath
	if env := os.Getenv("AMY_AZ"); env != "" {
		path = env
	}
	cmd := exec.CommandContext(ctx, path, args...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		detail := errb.String()
		if isAuthError(detail) {
			return nil, ErrNotLoggedIn
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("az CLI not found — brew install azure-cli && az extension add --name azure-devops")
		}
		msg := strings.TrimSpace(detail)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("az: %s", msg)
	}
	return []byte(out.String()), nil
}

// isAuthError matches the documented logged-out signatures.
func isAuthError(stderr string) bool {
	lower := strings.ToLower(stderr)
	for _, marker := range []string{
		"tf400813", "not authorized", "az devops login",
		"before you can run azure devops commands", "azure_devops_ext_pat",
		"please run 'az login'",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// wiqlEscape doubles single quotes inside WIQL string literals.
func wiqlEscape(v string) string { return strings.ReplaceAll(v, "'", "''") }

// BoardItems lists a virtual board's open work items, cached for a minute.
func (s *Source) BoardItems(ctx context.Context, b BoardConfig, force bool) ([]WorkItem, error) {
	s.mu.Lock()
	if c, ok := s.lists[b.Name]; ok && !force && s.now().Sub(c.at) < listTTL {
		items := c.items
		s.mu.Unlock()
		return items, nil
	}
	s.mu.Unlock()

	wiql := fmt.Sprintf(
		"SELECT [System.Id], [System.Title], [System.State], [System.AssignedTo] FROM WorkItems "+
			"WHERE [System.WorkItemType] = '%s' AND [System.TeamProject] = '%s' AND [System.AreaPath] = '%s' "+
			"AND [System.State] <> 'Closed' AND [System.State] <> 'Removed'",
		wiqlEscape(b.Type), wiqlEscape(s.cfg.Project), wiqlEscape(b.Area))
	out, err := s.run(ctx, "boards", "query", "--wiql", wiql,
		"--project", s.cfg.Project, "--org", s.cfg.Org, "-o", "json",
		"--query", `[].{id:id, title:fields."System.Title", state:fields."System.State", assigned:fields."System.AssignedTo".displayName}`)
	if err != nil {
		return nil, err
	}
	var items []WorkItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("parse az boards query output: %w", err)
	}
	s.mu.Lock()
	s.lists[b.Name] = cachedList{items: items, at: s.now()}
	s.mu.Unlock()
	return items, nil
}

// Item fetches one work item with description + comment count, cached.
func (s *Source) Item(ctx context.Context, id int, force bool) (WorkItem, error) {
	s.mu.Lock()
	if c, ok := s.items[id]; ok && !force && s.now().Sub(c.at) < detailTTL {
		item := c.item
		s.mu.Unlock()
		return item, nil
	}
	s.mu.Unlock()

	out, err := s.run(ctx, "boards", "work-item", "show", "--id", fmt.Sprint(id),
		"--org", s.cfg.Org, "-o", "json",
		"--fields", "System.Id,System.Title,System.State,System.AssignedTo,System.CommentCount,System.Description",
		"--query", `{id:id, title:fields."System.Title", state:fields."System.State", assigned:fields."System.AssignedTo".displayName, comments:fields."System.CommentCount", description:fields."System.Description"}`)
	if err != nil {
		return WorkItem{}, err
	}
	var item WorkItem
	if err := json.Unmarshal(out, &item); err != nil {
		return WorkItem{}, fmt.Errorf("parse az work-item show output: %w", err)
	}
	s.mu.Lock()
	s.items[id] = cachedItem{item: item, at: s.now()}
	s.mu.Unlock()
	return item, nil
}

// SetState moves a work item to another column (state transition).
func (s *Source) SetState(ctx context.Context, id int, state string) error {
	_, err := s.run(ctx, "boards", "work-item", "update", "--id", fmt.Sprint(id),
		"--state", state, "--org", s.cfg.Org, "-o", "json", "--query", "id")
	if err == nil {
		s.invalidate(id)
	}
	return err
}

// Comment appends a discussion comment.
func (s *Source) Comment(ctx context.Context, id int, text string) error {
	_, err := s.run(ctx, "boards", "work-item", "update", "--id", fmt.Sprint(id),
		"--discussion", text, "--org", s.cfg.Org, "-o", "json", "--query", "id")
	if err == nil {
		s.invalidate(id)
	}
	return err
}

// invalidate drops caches touched by a mutation.
func (s *Source) invalidate(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	s.lists = map[string]cachedList{}
}

// Columns returns the board's column order: configured, else the ADO
// default progression filtered/extended by observed states.
func Columns(b BoardConfig, items []WorkItem) []string {
	if len(b.Columns) > 0 {
		return b.Columns
	}
	defaults := []string{"New", "To Do", "Active", "Doing", "In Progress", "Resolved", "Done", "Closed"}
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.State] = true
	}
	var cols []string
	for _, d := range defaults {
		if seen[d] {
			cols = append(cols, d)
			delete(seen, d)
		}
	}
	var extra []string
	for state := range seen {
		extra = append(extra, state)
	}
	// Stable-ish: append leftovers sorted.
	for i := 0; i < len(extra); i++ {
		for j := i + 1; j < len(extra); j++ {
			if extra[j] < extra[i] {
				extra[i], extra[j] = extra[j], extra[i]
			}
		}
	}
	cols = append(cols, extra...)
	if len(cols) == 0 {
		cols = []string{"New", "Active", "Resolved"}
	}
	return cols
}

// --- source.Source (health surfacing on the 0 screen) ---

func (s *Source) Health(ctx context.Context) source.Health {
	// Cheap probe: a WIQL query that returns no rows still exercises auth.
	_, err := s.run(ctx, "boards", "query",
		"--wiql", "SELECT [System.Id] FROM WorkItems WHERE [System.Id] = 0",
		"--project", s.cfg.Project, "--org", s.cfg.Org, "-o", "json")
	switch {
	case err == nil:
		return source.Health{State: source.StateOK,
			Detail: fmt.Sprintf("%s · %d virtual board(s)", s.cfg.Org, len(s.cfg.Boards))}
	case errors.Is(err, ErrNotLoggedIn):
		return source.Health{State: source.StateMisconfigured,
			Detail: "az devops login --organization " + s.cfg.Org + " (or export AZURE_DEVOPS_EXT_PAT)"}
	default:
		return source.Health{State: source.StateMisconfigured, Detail: err.Error()}
	}
}

// DueItems: Azure work items don't merge into Today in v1.
func (s *Source) DueItems(ctx context.Context, day string, includeDone bool) ([]source.Item, error) {
	return nil, nil
}

func (s *Source) AgentContext(it source.Item) (string, string, error) {
	item, ok := it.Payload.(WorkItem)
	if !ok {
		return "", "", fmt.Errorf("not an azure work item: %s", it.ID)
	}
	body := fmt.Sprintf("Context from my Azure Boards work item #%d %q (%s):\nstate: %s",
		item.ID, item.Title, s.WebURL(item.ID), item.State)
	if item.Assignee != "" {
		body += " · assignee: " + item.Assignee
	}
	if item.Description != "" {
		body += "\n\n" + StripHTML(item.Description)
	}
	return fmt.Sprintf("AB#%d", item.ID), body, nil
}

// StripHTML flattens the HTML bodies ADO returns (System.Description) into
// readable terminal text. Minimal by design: tags out, common entities in.
func StripHTML(html string) string {
	var b strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	text := b.String()
	replacer := strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'")
	text = replacer.Replace(text)
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return strings.Join(lines, "\n")
}
