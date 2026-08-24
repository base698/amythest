package jira

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// stubClient serves canned fixtures with dates relative to now, so the Today
// merge always has an overdue and a due-today issue to demonstrate.
// AddComment mutates in memory, making the comment flow demonstrable
// end-to-end without a Jira.
type stubClient struct {
	mu     sync.Mutex
	issues []Issue
}

func newStub(now func() time.Time) *stubClient {
	day := func(offset int) string { return now().AddDate(0, 0, offset).Format("2006-01-02") }
	return &stubClient{issues: []Issue{
		{
			Key: "DEMO-101", Summary: "Fix login redirect loop", Status: "In Progress",
			Assignee: "you", Due: day(-1),
			Description: "Users bounce between /login and /home when the session cookie is stale.\n\nRepro: expire the cookie, load /home.",
		},
		{
			Key: "DEMO-205", Summary: "Upgrade TLS certs on edge proxy", Status: "To Do",
			Assignee: "you", Due: day(0),
			Description: "Certs expire soon; rotate via the usual runbook and verify SAN list.",
		},
		{
			Key: "DEMO-212", Summary: "Write Q3 incident postmortem", Status: "To Do",
			Assignee: "you", Due: day(3),
			Description: "Cover the cache stampede and the follow-up rate limiting work.",
		},
		{
			Key: "DEMO-198", Summary: "Customer CSV export truncates rows", Status: "Backlog",
			Assignee: "you",
			Description: "Exports cap at 10k rows silently. Either paginate or stream.",
			Comments: []Comment{{Author: "sam", Body: "Repro attached in the ticket.", Created: day(-7)}},
		},
	}}
}

func (c *stubClient) Search(ctx context.Context, jql string) ([]Issue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Issue, len(c.issues))
	copy(out, c.issues)
	return out, nil
}

func (c *stubClient) AddComment(ctx context.Context, key, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.issues {
		if c.issues[i].Key == key {
			c.issues[i].Comments = append(c.issues[i].Comments, Comment{
				Author: "you", Body: body, Created: time.Now().Format("2006-01-02"),
			})
			return nil
		}
	}
	return fmt.Errorf("issue %s not found", key)
}
