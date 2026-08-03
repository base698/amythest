package index_test

import (
	"testing"

	"github.com/base698/amythest/internal/bases"
	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
)

func openReconciled(t *testing.T, root string) *index.DB {
	t.Helper()
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Reconcile(v, markdown.New("/")); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTaskAndItemRowSources(t *testing.T) {
	root := t.TempDir()
	writeNote(t, root, "Garden/Plan.md", `---
title: Garden Plan
---
- [ ] buy seeds #garden 📅 2026-05-01 ⏫
- [x] sketch beds #garden ✅ 2026-04-02
- a plain bullet with no fields
`)
	writeNote(t, root, "Garden/Log.md", `---
title: Garden Log
---
- [Planted:: 2026-04-10] [Crop:: beans] [Count:: 24]
- [Planted:: 2026-04-12] [Crop:: squash] [Count:: 8]

- watering session
  [Duration:: 45]
  [Zone:: back beds]
`)
	db := openReconciled(t, root)

	// tasks source: one row per checkbox, task fields as note.* properties
	taskRows, err := db.RowsForSource("tasks")
	if err != nil {
		t.Fatal(err)
	}
	if len(taskRows) != 2 {
		t.Fatalf("task rows = %d, want 2", len(taskRows))
	}
	base, err := bases.ParseBase([]byte(`
source: tasks
filters:
  and:
    - note.status == "open"
views:
  - type: table
    name: Open
    order: [note.text, note.due, note.priority]
`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := base.Data(taskRows, 0)
	if err != nil {
		t.Fatal(err)
	}
	rows := data.Groups[0].Rows
	if len(rows) != 1 {
		t.Fatalf("open tasks = %#v", rows)
	}
	if rows[0][1] != "2026-05-01" || rows[0][2] != "1" {
		t.Errorf("task row = %v, want due 2026-05-01 priority 1", rows[0])
	}

	// items source: one row per line of inline fields, numeric values parsed
	itemRows, err := db.RowsForSource("items")
	if err != nil {
		t.Fatal(err)
	}
	if len(itemRows) != 3 {
		t.Fatalf("item rows = %d, want 3", len(itemRows))
	}
	// fields on indented continuation lines group into one item row
	var watering map[string]any
	for _, r := range itemRows {
		if r.Frontmatter["Zone"] == "back beds" {
			watering = r.Frontmatter
		}
	}
	if watering == nil || watering["Duration"] != 45.0 {
		t.Errorf("multi-line item row = %#v, want Duration 45 with Zone", watering)
	}
	base, err = bases.ParseBase([]byte(`
source: items
filters:
  and:
    - note.Count > 10
views:
  - type: table
    name: Big plantings
    order: [note.Crop, note.Count, note.Planted]
`))
	if err != nil {
		t.Fatal(err)
	}
	data, err = base.Data(itemRows, 0)
	if err != nil {
		t.Fatal(err)
	}
	rows = data.Groups[0].Rows
	if len(rows) != 1 || rows[0][0] != "beans" || rows[0][1] != "24" {
		t.Errorf("item rows = %#v, want one beans/24 row", rows)
	}

	if _, err := db.RowsForSource("nonsense"); err == nil {
		t.Error("expected error for unknown source")
	}
}
