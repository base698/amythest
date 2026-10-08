package tasks

import (
	"strings"
	"testing"
)

func TestParseFile(t *testing.T) {
	body := `# Note

- [ ] Buy milk 📅 2026-08-01 ⏫ #errand
- [x] Ship release 📅 2026-07-01 ✅ 2026-07-02
- [-] Cancelled thing ❌ 2026-06-01
- [ ] Recurring 🔁 every week 📅 2026-08-03
- normal list item, not a task
- [Weight:: 185] on a list line

` + "```" + `
- [ ] inside a fence, not indexed
` + "```" + `
`
	got, fields, items := ParseFile("slug", "path.md", []byte(body))
	if len(got) != 4 {
		t.Fatalf("tasks = %d, want 4: %#v", len(got), got)
	}
	milk := got[0]
	if milk.Due != "2026-08-01" || milk.Priority != 1 || milk.Status != StatusOpen {
		t.Errorf("milk = %+v", milk)
	}
	if milk.Text != "Buy milk" && !strings.HasPrefix(milk.Text, "Buy milk") {
		t.Errorf("milk text = %q", milk.Text)
	}
	if len(milk.Tags) != 1 || milk.Tags[0] != "errand" {
		t.Errorf("milk tags = %v", milk.Tags)
	}
	if got[1].Status != StatusDone || got[1].DoneDate != "2026-07-02" {
		t.Errorf("done task = %+v", got[1])
	}
	if got[2].Status != StatusCancelled {
		t.Errorf("cancelled = %+v", got[2])
	}
	if got[3].Recurrence != "every week" || got[3].Due != "2026-08-03" {
		t.Errorf("recurring = %+v", got[3])
	}
	if len(fields) != 1 || fields[0].Key != "Weight" || fields[0].Value != "185" {
		t.Errorf("fields = %+v", fields)
	}
	if len(items) != 6 {
		t.Fatalf("list items = %d, want 6 (fenced bullet excluded): %#v", len(items), items)
	}
	if items[4].Text != "normal list item, not a task" || items[4].Status != "" {
		t.Errorf("plain item = %+v", items[4])
	}
	if items[0].Status != StatusOpen || items[1].Status != StatusDone {
		t.Errorf("item statuses = %+v %+v", items[0], items[1])
	}
	if items[5].Text != "on a list line" {
		t.Errorf("field item text = %q, want fields stripped", items[5].Text)
	}
}

func TestQueryFiltersSortsGroupsLimits(t *testing.T) {
	all := []Task{
		{Text: "a", Status: StatusOpen, Due: "2026-01-02", Priority: 3, Path: "X/a.md"},
		{Text: "b", Status: StatusOpen, Due: "2026-01-01", Priority: 1, Path: "X/b.md"},
		{Text: "c", Status: StatusDone, Due: "2026-01-01", Priority: 3, Path: "Y/c.md"},
		{Text: "d", Status: StatusOpen, Priority: 3, Path: "Y/d.md", Tags: []string{"errand"}},
	}

	q := ParseQuery("not done\ndue before 2026-02-01\nsort by due, priority")
	if q.Err() != nil {
		t.Fatal(q.Err())
	}
	groups := q.Run(all)
	if len(groups) != 1 || len(groups[0].Tasks) != 2 {
		t.Fatalf("groups = %#v", groups)
	}
	if groups[0].Tasks[0].Text != "b" || groups[0].Tasks[1].Text != "a" {
		t.Errorf("sort order = %v %v", groups[0].Tasks[0].Text, groups[0].Tasks[1].Text)
	}

	q = ParseQuery("path does not include Y\nlimit 1")
	if got := q.Run(all); len(got[0].Tasks) != 1 || got[0].Tasks[0].Path != "X/b.md" && got[0].Tasks[0].Text != "b" {
		t.Errorf("path filter = %#v", got)
	}

	q = ParseQuery("tags include #errand")
	if got := q.Run(all); len(got[0].Tasks) != 1 || got[0].Tasks[0].Text != "d" {
		t.Errorf("tag filter = %#v", got)
	}

	q = ParseQuery("group by status")
	if got := q.Run(all); len(got) != 2 {
		t.Errorf("group count = %d", len(got))
	}

	if ParseQuery("frobnicate the widgets").Err() == nil {
		t.Error("expected error for unknown instruction")
	}
	if ParseQuery("hide edit button").Err() != nil {
		t.Error("presentation directives should be accepted")
	}
}

func TestQueryReverseSortAndTagExclusion(t *testing.T) {
	all := []Task{
		{Text: "a", Status: StatusDone, DoneDate: "2026-01-01", Path: "X/a.md"},
		{Text: "b", Status: StatusDone, DoneDate: "2026-01-03", Path: "X/b.md"},
		{Text: "c", Status: StatusDone, DoneDate: "2026-01-02", Path: "X/c.md", Tags: []string{"errand"}},
	}

	q := ParseQuery("done\nsort by done reverse")
	if q.Err() != nil {
		t.Fatal(q.Err())
	}
	got := q.Run(all)[0].Tasks
	if got[0].Text != "b" || got[1].Text != "c" || got[2].Text != "a" {
		t.Errorf("reverse sort order = %v %v %v", got[0].Text, got[1].Text, got[2].Text)
	}

	q = ParseQuery("tag does not include #errand")
	if q.Err() != nil {
		t.Fatal(q.Err())
	}
	if got := q.Run(all)[0].Tasks; len(got) != 2 || got[0].Text == "c" || got[1].Text == "c" {
		t.Errorf("tag exclusion = %#v", got)
	}

	if ParseQuery("sort by due backwards").Err() == nil {
		t.Error("expected error for unknown sort modifier")
	}
}

// group by note puts each file's tasks under their own heading; group by
// folder keeps collapsing a directory. Both are offered in the UI, so the
// two must stay distinguishable.
func TestQueryGroupByNoteAndFolder(t *testing.T) {
	all := []Task{
		{Text: "a", Status: StatusOpen, Path: "X/a.md", Slug: "X/a"},
		{Text: "b", Status: StatusOpen, Path: "X/b.md", Slug: "X/b"},
		{Text: "c", Status: StatusOpen, Path: "X/a.md", Slug: "X/a"},
		{Text: "d", Status: StatusOpen, Path: "Y/d.md", Slug: "Y/d"},
	}

	groups := ParseQuery("group by note").Run(all)
	if len(groups) != 3 {
		t.Fatalf("note groups = %d, want one per file: %#v", len(groups), groups)
	}
	if groups[0].Name != "X/a.md" || len(groups[0].Tasks) != 2 {
		t.Errorf("first note group = %q with %d tasks", groups[0].Name, len(groups[0].Tasks))
	}
	if groups[1].Name != "X/b.md" || groups[2].Name != "Y/d.md" {
		t.Errorf("note group names = %q %q", groups[1].Name, groups[2].Name)
	}

	// "file" is accepted as a synonym.
	if got := ParseQuery("group by file").Run(all); len(got) != 3 {
		t.Errorf("file groups = %d", len(got))
	}

	// folder still collapses a directory, and "path" keeps that older
	// meaning so existing ```tasks blocks do not change behaviour.
	for _, key := range []string{"group by folder", "group by path"} {
		got := ParseQuery(key).Run(all)
		if len(got) != 2 {
			t.Fatalf("%s = %d groups, want 2", key, len(got))
		}
		if got[0].Name != "X" || len(got[0].Tasks) != 3 {
			t.Errorf("%s first group = %q with %d tasks", key, got[0].Name, len(got[0].Tasks))
		}
	}

	if ParseQuery("group by nonsense").Err() == nil {
		t.Error("expected an error for an unknown group field")
	}
}
