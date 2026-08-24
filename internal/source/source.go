// Package source is the seam between the amy TUI and its backends. The
// amythest server is the built-in source; external ticketing systems (Jira,
// Azure DevOps work items, …) implement the same interface and their items
// merge into the Today view and get a dedicated browsing view.
//
// The abstraction is deliberately narrow: only the cross-source surfaces
// (Today aggregation, agent-context framing, browsing, commenting) go
// through it. Amythest-specific behaviors — board columns, two-stage task
// delete, due/recurrence prompts — stay on the concrete API client, reached
// via Item.Payload type-switches.
//
// Adding a provider = one package implementing Source (plus whichever
// optional capabilities it supports) and one entry in cli.yaml's sources:
// section. Providers that need network credentials read them from the
// environment (never yaml), and should offer a Stub mode serving canned
// fixtures so the UX is demonstrable before credentials exist.
package source

import "context"

// Item is a normalized, render-ready row from any source.
type Item struct {
	Source  string   // registry name: "amythest", "jira", "azdo"
	ID      string   // stable within the source: "slug#line", card ID, PROJ-42, AB#1234
	Kind    string   // "task" | "card" | "issue" — drives the [kind] badge
	Title   string
	Due     string // YYYY-MM-DD, "" when none
	Done    bool
	Focused bool     // pin to the Focus section regardless of due date
	Badges  []string // extra styled badges, e.g. "blocked"
	Meta    string   // secondary text: task path, board name, "DEMO-101 · In Progress"
	URL     string   // browser-openable; "" when not applicable
	Payload any      // source-native value for type-switched behaviors
}

type State string

const (
	StateOK            State = "connected"
	StateStubbed       State = "stubbed"
	StateMisconfigured State = "missing-credentials"
)

type Health struct {
	State  State
	Detail string // e.g. "5 boards", "set JIRA_EMAIL / JIRA_API_TOKEN"
}

// Source is the minimum contract: identity, health, Today items, and agent
// context framing.
type Source interface {
	Name() string
	Health(ctx context.Context) Health
	// DueItems returns items due on or before day (YYYY-MM-DD) plus,
	// when includeDone, items completed on that day. Filtering belongs to
	// the source; section assignment (SectionFor) belongs to the view.
	DueItems(ctx context.Context, day string, includeDone bool) ([]Item, error)
	// AgentContext frames an item for a herdr agent prompt.
	AgentContext(it Item) (subject, body string, err error)
}

// Optional capabilities, discovered by type assertion.

// Commenter can append a comment to an item (manual only — amy never writes
// state changes back to external systems).
type Commenter interface {
	Comment(ctx context.Context, it Item, body string) error
}

// Lister returns the source's full browsing list for its dedicated view.
type Lister interface {
	List(ctx context.Context) ([]Item, error)
}

// SectionFor assigns a Today-view section for an item on the given day.
func SectionFor(it Item, day string) string {
	switch {
	case it.Done:
		return "Done today"
	case it.Focused:
		return "Focus"
	case it.Due != "" && it.Due < day:
		return "Overdue"
	default:
		return "Due today"
	}
}
