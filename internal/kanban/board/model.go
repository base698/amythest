package board

import "time"

type Status string
type Priority string

const (
	Triage     Status = "triage"
	Backlog    Status = "backlog"
	Ready      Status = "ready"
	InProgress Status = "in_progress"
	Verify     Status = "verify"
	Done       Status = "done"
)

const (
	P0 Priority = "p0"
	P1 Priority = "p1"
	P2 Priority = "p2"
	P3 Priority = "p3"
)

var ActiveStatuses = []Status{Triage, Backlog, Ready, InProgress, Verify}

type Comment struct {
	ID        string    `json:"id" yaml:"id"`
	Author    string    `json:"author" yaml:"author"`
	Body      string    `json:"body" yaml:"body"`
	CreatedAt time.Time `json:"createdAt" yaml:"createdAt"`
}

type Attachment struct {
	ID          string    `json:"id" yaml:"id"`
	Filename    string    `json:"filename" yaml:"filename"`
	Size        int64     `json:"size" yaml:"size"`
	ContentType string    `json:"contentType" yaml:"contentType"`
	CreatedAt   time.Time `json:"createdAt" yaml:"createdAt"`
}

type AuditEntry struct {
	Action    string    `json:"action" yaml:"action"`
	Actor     string    `json:"actor" yaml:"actor"`
	FromBoard string    `json:"fromBoard,omitempty" yaml:"fromBoard,omitempty"`
	ToBoard   string    `json:"toBoard,omitempty" yaml:"toBoard,omitempty"`
	CreatedAt time.Time `json:"createdAt" yaml:"createdAt"`
}

type Card struct {
	ID          string   `json:"id" yaml:"id"`
	Title       string   `json:"title" yaml:"title"`
	Description string   `json:"description" yaml:"description,omitempty"`
	DueDate     string   `json:"dueDate,omitempty" yaml:"dueDate,omitempty"`
	Milestone   string   `json:"milestone,omitempty" yaml:"milestone,omitempty"`
	Priority    Priority `json:"priority" yaml:"priority,omitempty"`
	Status      Status   `json:"status" yaml:"status"`
	Assignee    string   `json:"assignee,omitempty" yaml:"assignee,omitempty"`
	// Agent names the dispatch agent (provider + model) that should run this
	// card, overriding the agent implied by Assignee. Empty means "use the
	// assignee's agent".
	Agent string `json:"agent,omitempty" yaml:"agent,omitempty"`
	// Blocked marks a card that must not be worked yet. It is a flag rather than
	// a status so a card keeps its place in the flow while blocked, and the
	// dispatcher will not claim it.
	Blocked     bool         `json:"blocked,omitempty" yaml:"blocked,omitempty"`
	Labels      []string     `json:"labels" yaml:"labels,omitempty"`
	Comments    []Comment    `json:"comments" yaml:"comments,omitempty"`
	Attachments []Attachment `json:"attachments" yaml:"attachments,omitempty"`
	Audit       []AuditEntry `json:"audit,omitempty" yaml:"audit,omitempty"`
	CreatedAt   time.Time    `json:"createdAt" yaml:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt" yaml:"updatedAt"`
	DoneAt      *time.Time   `json:"doneAt,omitempty" yaml:"doneAt,omitempty"`
}

type Board struct {
	Version         int    `json:"version" yaml:"version"`
	Name            string `json:"name" yaml:"name"`
	DisplayName     string `json:"displayName" yaml:"displayName"`
	Description     string `json:"description,omitempty" yaml:"description,omitempty"`
	Icon            string `json:"icon,omitempty" yaml:"icon,omitempty"`
	Color           string `json:"color,omitempty" yaml:"color,omitempty"`
	SortOrder       int    `json:"sortOrder" yaml:"sortOrder,omitempty"`
	Pinned          bool   `json:"pinned" yaml:"pinned,omitempty"`
	Archived        bool   `json:"archived" yaml:"archived,omitempty"`
	FocusCardID     string `json:"focusCardId,omitempty" yaml:"focusCardId,omitempty"`
	DispatchEnabled bool   `json:"dispatchEnabled" yaml:"dispatchEnabled,omitempty"`
	Cards           []Card `json:"cards" yaml:"cards"`
}

type BoardSummary struct {
	Name            string         `json:"name"`
	DisplayName     string         `json:"displayName"`
	Description     string         `json:"description,omitempty"`
	Icon            string         `json:"icon,omitempty"`
	Color           string         `json:"color,omitempty"`
	SortOrder       int            `json:"sortOrder"`
	Pinned          bool           `json:"pinned"`
	Archived        bool           `json:"archived"`
	FocusCardID     string         `json:"focusCardId,omitempty"`
	DispatchEnabled bool           `json:"dispatchEnabled"`
	Counts          map[Status]int `json:"counts"`
}

type BoardInput struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	SortOrder   int    `json:"sortOrder"`
	Pinned      *bool  `json:"pinned"`
}

type BoardSettingsPatch struct {
	DisplayName     *string `json:"displayName"`
	Description     *string `json:"description"`
	Icon            *string `json:"icon"`
	Color           *string `json:"color"`
	SortOrder       *int    `json:"sortOrder"`
	Pinned          *bool   `json:"pinned"`
	Archived        *bool   `json:"archived"`
	FocusCardID     *string `json:"focusCardId"`
	DispatchEnabled *bool   `json:"dispatchEnabled"`
}

type CardInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	DueDate     string   `json:"dueDate"`
	Milestone   string   `json:"milestone"`
	Priority    Priority `json:"priority"`
	Status      Status   `json:"status"`
	Assignee    string   `json:"assignee"`
	Agent       string   `json:"agent"`
	Blocked     bool     `json:"blocked"`
	Labels      []string `json:"labels"`
}

type CardPatch struct {
	Title       *string   `json:"title"`
	Description *string   `json:"description"`
	DueDate     *string   `json:"dueDate"`
	Milestone   *string   `json:"milestone"`
	Priority    *Priority `json:"priority"`
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

func ValidPriority(priority Priority) bool {
	switch priority {
	case P0, P1, P2, P3:
		return true
	default:
		return false
	}
}
