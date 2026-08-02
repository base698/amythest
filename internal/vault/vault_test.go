package vault_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/base698/amythest/internal/vault"
)

func TestScanWarnsOnSlugCollisionAndKeepsBothNotes(t *testing.T) {
	root := t.TempDir()
	// "Foo Bar.md" and "Foo-Bar.md" both slugify to "Foo-Bar".
	if err := os.WriteFile(filepath.Join(root, "Foo Bar.md"), []byte("spaced note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Foo-Bar.md"), []byte("dashed note\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Notes) != 2 {
		t.Fatalf("scanned %d notes, want both colliding notes", len(v.Notes))
	}
	n, ok := v.BySlug("Foo-Bar")
	if !ok {
		t.Fatal("colliding slug resolves to no note")
	}
	// Notes are path-sorted, so the later path wins deterministically.
	if n.Path != "Foo-Bar.md" {
		t.Fatalf("BySlug winner = %q, want Foo-Bar.md", n.Path)
	}
}
