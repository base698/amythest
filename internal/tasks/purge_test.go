package tasks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPurgeRemovesCancelledLinesAndKeepsChildren(t *testing.T) {
	root := tempVaultDir(t)
	src := []byte("---\ntitle: P\n---\n# P\n- [-] Old idea ❌ 2026-08-01\n    - child note stays\n- [ ] Keep me\n- [-] Also old ❌ 2026-08-01\n")
	path := filepath.Join(root, "P.md")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	err := PurgeCancelledInFileAndReindex(root, "P.md", []int{2, 5}, FileVersion(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	want := "---\ntitle: P\n---\n# P\n    - child note stays\n- [ ] Keep me\n"
	if string(out) != want {
		t.Fatalf("out=%q want=%q", out, want)
	}
}

func TestPurgeRefusesOpenAndDoneTasks(t *testing.T) {
	root := tempVaultDir(t)
	src := []byte("- [ ] Open\n- [x] Done ✅ 2026-08-01\n- [-] Cancelled ❌ 2026-08-01\n")
	path := filepath.Join(root, "P.md")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, line := range []int{1, 2} {
		if err := PurgeCancelledInFileAndReindex(root, "P.md", []int{line}, FileVersion(src), nil); err == nil {
			t.Fatalf("non-cancelled line %d purged", line)
		}
	}
	// A batch mixing valid and invalid lines must not apply at all.
	if err := PurgeCancelledInFileAndReindex(root, "P.md", []int{1, 3}, FileVersion(src), nil); err == nil {
		t.Fatal("mixed batch accepted")
	}
	if out, _ := os.ReadFile(path); string(out) != string(src) {
		t.Fatalf("rejected purge mutated file: %q", out)
	}
}

func TestPurgeRejectsStaleVersionAndDuplicates(t *testing.T) {
	root := tempVaultDir(t)
	src := []byte("- [-] Gone ❌ 2026-08-01\n")
	path := filepath.Join(root, "P.md")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PurgeCancelledInFileAndReindex(root, "P.md", []int{1}, FileVersion([]byte("x")), nil); err == nil {
		t.Fatal("stale version accepted")
	}
	if err := PurgeCancelledInFileAndReindex(root, "P.md", []int{1, 1}, FileVersion(src), nil); err == nil {
		t.Fatal("duplicate lines accepted")
	}
}

func TestPurgeRemovesFinalLineWithoutTrailingNewline(t *testing.T) {
	root := tempVaultDir(t)
	src := []byte("- [ ] Keep\n- [-] Last ❌ 2026-08-01")
	path := filepath.Join(root, "P.md")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PurgeCancelledInFileAndReindex(root, "P.md", []int{2}, FileVersion(src), nil); err != nil {
		t.Fatal(err)
	}
	if out, _ := os.ReadFile(path); string(out) != "- [ ] Keep\n" {
		t.Fatalf("out=%q", out)
	}
}
