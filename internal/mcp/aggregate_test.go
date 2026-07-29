package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/base698/amythest/internal/vault"
)

func TestAggregateFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeNote := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeNote("Records.md", "---\ntags: [records]\n---\nSummary\n")
	writeNote(
		"Records/Entries/one.md",
		"---\nprimary_value: 1.2\nsecondary_value: 0.4\n---\n",
	)
	writeNote(
		"Records/Entries/two.md",
		"---\nprimary_value: 2\nsecondary_value: \"1.6\"\n---\n",
	)
	writeNote("Elsewhere.md", "---\nprimary_value: 99\n---\n")

	scanned, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := aggregateFrontmatter(scanned, aggregateFrontmatterIn{
		Folder: "Records",
		Fields: []string{"primary_value", "secondary_value", "primary_value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NotesScanned != 3 {
		t.Fatalf("notes scanned = %d, want 3", got.NotesScanned)
	}
	if got.Fields["primary_value"].Sum != 3.2 ||
		got.Fields["primary_value"].Count != 2 {
		t.Fatalf(
			"primary_value = %#v, want sum 3.2 count 2",
			got.Fields["primary_value"],
		)
	}
	if got.Fields["secondary_value"].Sum != 2 ||
		got.Fields["secondary_value"].Count != 2 {
		t.Fatalf(
			"secondary_value = %#v, want sum 2 count 2",
			got.Fields["secondary_value"],
		)
	}
}

func TestAggregateFrontmatterRejectsUnsafeOrEmptyInput(t *testing.T) {
	scanned, err := vault.Scan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []aggregateFrontmatterIn{
		{Folder: "../outside", Fields: []string{"hours"}},
		{Folder: "Records"},
		{Folder: "Records", Fields: make([]string, maxAggregateFields+1)},
	} {
		if _, err := aggregateFrontmatter(scanned, test); err == nil {
			t.Fatalf("aggregateFrontmatter(%#v) unexpectedly succeeded", test)
		}
	}
}
