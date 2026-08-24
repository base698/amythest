// Package jira is the first external ticketing source for amy. It follows
// the pattern every provider should: a small yaml Config under cli.yaml's
// sources: key, credentials from the environment only, an issueClient seam
// with a stub implementation (canned fixtures, no network) so the UX works
// before credentials exist, and a real HTTP client to fill in.
//
// Azure DevOps is the intended second provider: a sibling package with the
// same shape — Config{Org, Project, WIQL, Stub}, a workItemClient seam
// (GET /_apis/wit/wiql for queries, POST …/workItems/{id}/comments), PAT
// bearer auth from AZDO_PAT — implementing the same source.Source contract.
package jira

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/base698/amythest/internal/envfile"
	"github.com/base698/amythest/internal/source"
)

type Config struct {
	URL     string `yaml:"url"`     // https://yourcompany.atlassian.net
	JQL     string `yaml:"jql"`     // issue filter for List/DueItems
	Project string `yaml:"project"` // optional display hint
	Stub    bool   `yaml:"stub"`    // serve canned fixtures, no network
}

const defaultJQL = "assignee = currentUser() AND resolution = EMPTY ORDER BY due ASC"

type sourcesFile struct {
	Sources struct {
		Jira *Config `yaml:"jira"`
	} `yaml:"sources"`
}

// LoadConfig parses the sources.jira section out of the cli.yaml at path.
// ok is false when the file or section is absent; unknown yaml keys are
// ignored, matching the endpoint loader's behavior.
func LoadConfig(path string) (Config, bool, error) {
	if path == "" {
		return Config{}, false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, false, nil // absent file = no source, not an error
	}
	var sf sourcesFile
	if err := yaml.Unmarshal(raw, &sf); err != nil {
		return Config{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if sf.Sources.Jira == nil {
		return Config{}, false, nil
	}
	cfg := *sf.Sources.Jira
	if cfg.JQL == "" {
		cfg.JQL = defaultJQL
	}
	return cfg, true, nil
}

type Issue struct {
	Key         string
	Summary     string
	Status      string
	Assignee    string
	Due         string // YYYY-MM-DD or ""
	Description string
	Comments    []Comment
}

type Comment struct {
	Author  string
	Body    string
	Created string
}

// issueClient is the seam between the source and the wire: the stub and the
// real HTTP client both implement it.
type issueClient interface {
	Search(ctx context.Context, jql string) ([]Issue, error)
	AddComment(ctx context.Context, key, body string) error
}

type Source struct {
	cfg     Config
	envFile string
	client  issueClient
	now     func() time.Time
}

func New(cfg Config, envFile string) *Source {
	s := &Source{cfg: cfg, envFile: envFile, now: time.Now}
	if cfg.Stub {
		s.client = newStub(s.now)
	} else {
		s.client = &httpClient{cfg: cfg, email: s.credential("JIRA_EMAIL"), token: s.credential("JIRA_API_TOKEN")}
	}
	return s
}

func (s *Source) Name() string { return "jira" }

func (s *Source) credential(key string) string {
	return envfile.Lookup(s.envFile, key)
}

func (s *Source) baseURL() string {
	if s.cfg.URL != "" {
		return strings.TrimRight(s.cfg.URL, "/")
	}
	return "https://example.atlassian.net"
}

func (s *Source) Health(ctx context.Context) source.Health {
	if s.cfg.Stub {
		return source.Health{State: source.StateStubbed, Detail: "canned fixture data — remove stub: true once credentials are set"}
	}
	if s.credential("JIRA_EMAIL") == "" || s.credential("JIRA_API_TOKEN") == "" {
		return source.Health{State: source.StateMisconfigured, Detail: "set JIRA_EMAIL / JIRA_API_TOKEN (env or " + s.envFile + ")"}
	}
	return source.Health{State: source.StateOK, Detail: s.baseURL() + " (HTTP client not yet implemented)"}
}

func (s *Source) DueItems(ctx context.Context, day string, includeDone bool) ([]source.Item, error) {
	issues, err := s.client.Search(ctx, s.cfg.JQL)
	if err != nil {
		return nil, err
	}
	var items []source.Item
	for _, issue := range issues {
		if issue.Due == "" || issue.Due > day {
			continue
		}
		items = append(items, s.item(issue))
	}
	return items, nil
}

// List returns every issue matching the configured JQL, for the jira view.
func (s *Source) List(ctx context.Context) ([]source.Item, error) {
	issues, err := s.client.Search(ctx, s.cfg.JQL)
	if err != nil {
		return nil, err
	}
	items := make([]source.Item, 0, len(issues))
	for _, issue := range issues {
		items = append(items, s.item(issue))
	}
	return items, nil
}

func (s *Source) item(issue Issue) source.Item {
	return source.Item{
		Source:  s.Name(),
		ID:      issue.Key,
		Kind:    "issue",
		Title:   issue.Summary,
		Due:     issue.Due,
		Meta:    issue.Key + " · " + issue.Status,
		URL:     s.baseURL() + "/browse/" + issue.Key,
		Payload: issue,
	}
}

// Comment posts a comment to the issue — manual-only; amy never writes
// state changes back to Jira.
func (s *Source) Comment(ctx context.Context, it source.Item, body string) error {
	return s.client.AddComment(ctx, it.ID, body)
}

func (s *Source) AgentContext(it source.Item) (string, string, error) {
	issue, ok := it.Payload.(Issue)
	if !ok {
		return "", "", fmt.Errorf("not a jira issue: %s", it.ID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Context from my jira issue %s %q (%s):\n", issue.Key, issue.Summary, it.URL)
	fmt.Fprintf(&b, "status: %s", issue.Status)
	if issue.Due != "" {
		fmt.Fprintf(&b, " · due: %s", issue.Due)
	}
	if issue.Assignee != "" {
		fmt.Fprintf(&b, " · assignee: %s", issue.Assignee)
	}
	b.WriteString("\n\n")
	b.WriteString(issue.Description)
	if len(issue.Comments) > 0 {
		b.WriteString("\n\nComments:\n")
		for _, c := range issue.Comments {
			fmt.Fprintf(&b, "- %s (%s): %s\n", c.Author, c.Created, c.Body)
		}
	}
	return issue.Key, b.String(), nil
}
