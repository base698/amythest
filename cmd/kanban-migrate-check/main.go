// Command kanban-migrate-check is a read-only dry run of the markdown→YAML
// kanban migration: it parses every legacy board file under the given root,
// renders it as YAML, re-parses that, and verifies the data is identical.
// Nothing is written. Temporary tool for the v3 format rollout.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/base698/amythest/internal/kanban/board"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: kanban-migrate-check <kanban-root>")
		os.Exit(2)
	}
	root := os.Args[1]
	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	failed := false
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, name := range []string{"board.md", "done.md", "board.yaml", "done.yaml"} {
			path := filepath.Join(root, entry.Name(), name)
			if _, err := os.Stat(path); err != nil {
				continue
			}
			checked++
			original, rt, err := board.DryRunConvert(path)
			if err != nil {
				fmt.Printf("FAIL %s: %v\n", path, err)
				failed = true
				continue
			}
			if !reflect.DeepEqual(original, rt) {
				fmt.Printf("FAIL %s: yaml round trip diverges\n", path)
				failed = true
				continue
			}
			fmt.Printf("ok   %s (%d cards)\n", path, len(original.Cards))
		}
	}
	fmt.Printf("checked %d files\n", checked)
	if failed {
		os.Exit(1)
	}
}
