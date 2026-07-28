package board

import "time"

type Status string

const (
	Triage     Status = "triage"
	Backlog    Status = "backlog"
	Ready      Status = "ready"
	InProgress Status = "in_progress"
	Verify     Status = "verify"
	Done       Status = "done"
)

var ActiveStatuses = []Status{Triage, Backlog, Ready, InProgress, Verify}

type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

type Attachment struct {
	ID          string    `json:"id"`
	Filename    string    `json:"filename"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
	CreatedAt   time.Time `json:"createdAt"`
}

type AuditEntry struct {
	Action    string    `json:"action"`
	Actor     string    `json:"actor"`
	FromBoard string    `json:"fromBoard,omitempty"`
	ToBoard   string    `json:"toBoard,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type Card struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      Status `json:"status"`
	Assignee    string `json:"assignee,omitempty"`
	// Agent names the dispatch agent (provider + model) that should run this
	// card, overriding the agent implied by Assignee. Empty means "use the
	// assignee's agent".
	Agent string `json:"agent,omitempty"`
	// Blocked marks a card that must not be worked yet. It is a flag rather than
	// a status so a card keeps its place in the flow while blocked, and the
	// dispatcher will not claim it.
	Blocked     bool         `json:"blocked,omitempty"`
	Labels      []string     `json:"labels"`
	Comments    []Comment    `json:"comments"`
	Attachments []Attachment `json:"attachments"`
	Audit       []AuditEntry `json:"audit,omitempty"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	DoneAt      *time.Time   `json:"doneAt,omitempty"`
}

type Board struct {
	Version         int    `json:"version"`
	Name            string `json:"name"`
	DispatchEnabled bool   `json:"dispatchEnabled"`
	Cards           []Card `json:"cards"`
}

type BoardSummary struct {
	Name   string         `json:"name"`
	Counts map[Status]int `json:"counts"`
}

type CardInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      Status   `json:"status"`
	Assignee    string   `json:"assignee"`
	Agent       string   `json:"agent"`
	Blocked     bool     `json:"blocked"`
	Labels      []string `json:"labels"`
}

type CardPatch struct {
	Title       *string   `json:"title"`
	Description *string   `json:"description"`
	Status      *Status   `json:"status"`
	Assignee    *string   `json:"assignee"`
	Agent       *string   `json:"agent"`
	Blocked     *bool     `json:"blocked"`
	Labels      *[]string `json:"labels"`
}

func ValidStatus(status Status) bool {
	switch status {
	case Triage, Backlog, Ready, InProgress, Verify, Done:
		return true
	default:
		return false
	}
}
