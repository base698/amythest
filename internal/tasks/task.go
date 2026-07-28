// Package tasks parses Obsidian Tasks-style checkboxes (emoji metadata
// syntax) out of notes, indexes them, and executes ```tasks``` query blocks
// so dashboards render live instead of rotting like the old dataview setup.
package tasks

type Task struct {
	Slug       string   `json:"slug"`
	Path       string   `json:"path"`
	Line       int      `json:"line"` // 1-based line in the source file
	Text       string   `json:"text"` // description with metadata stripped
	Status     string   `json:"status"` // open | done | cancelled | other
	Due        string   `json:"due,omitempty"`       // ISO dates sort lexically
	Scheduled  string   `json:"scheduled,omitempty"`
	Start      string   `json:"start,omitempty"`
	Recurrence string   `json:"recurrence,omitempty"`
	Priority   int      `json:"priority"` // 0 highest … 5 lowest, 3 = none
	DoneDate   string   `json:"doneDate,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

const (
	StatusOpen      = "open"
	StatusDone      = "done"
	StatusCancelled = "cancelled"
	StatusOther     = "other"
)

// InlineField is a `[Key:: value]` dataview-style field found in a note.
type InlineField struct {
	Line  int
	Key   string
	Value string
}
