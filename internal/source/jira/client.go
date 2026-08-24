package jira

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// httpClient is the real Atlassian Cloud client — a skeleton until there is
// a Jira to test against. Wire notes for whoever fills it in:
//
//   - Auth: HTTP basic with email:api-token on every request
//     (req.SetBasicAuth(email, token)). Tokens from id.atlassian.com.
//   - Search: POST {base}/rest/api/3/search/jql
//     {"jql": "...", "fields": ["summary","status","assignee","duedate",
//     "description","comment"], "maxResults": 100} — page via nextPageToken.
//   - Comment: POST {base}/rest/api/3/issue/{key}/comment with an ADF body:
//     {"body":{"type":"doc","version":1,"content":[{"type":"paragraph",
//     "content":[{"type":"text","text":"..."}]}]}}
//   - Do NOT reuse apiclient's session cache: it is keyed by the amythest
//     KanbanBase and shared with kanban.py. Basic auth needs no session.
type httpClient struct {
	cfg   Config
	email string
	token string
	http  *http.Client
}

func (c *httpClient) Search(ctx context.Context, jql string) ([]Issue, error) {
	return nil, c.notImplemented()
}

func (c *httpClient) AddComment(ctx context.Context, key, body string) error {
	return c.notImplemented()
}

func (c *httpClient) notImplemented() error {
	return fmt.Errorf("jira: real client not implemented yet — set stub: true in cli.yaml, or fill in internal/source/jira/client.go")
}

var _ = time.Second // referenced by the eventual implementation's timeouts
