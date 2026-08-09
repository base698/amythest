package share

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveUploadNeutralizesHostileTitleAndMime(t *testing.T) {
	root := t.TempDir()
	store := New(root, nil)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	up, err := store.SaveUpload("payload.bin",
		"<img src=x onerror=alert(1)> evil",
		"text/html\"><script>alert(1)</script>",
		strings.NewReader("data"), now)
	if err != nil {
		t.Fatal(err)
	}
	note, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(up.NoteRel)))
	if err != nil {
		t.Fatal(err)
	}
	body := string(note)
	if strings.Contains(body, "<img") || strings.Contains(body, "<script") {
		t.Fatalf("hostile markup reached the note body:\n%s", body)
	}
	if !strings.Contains(body, "&lt;img src=x onerror=alert(1)&gt; evil") &&
		!strings.Contains(body, "&lt;img") {
		t.Fatalf("title missing from note body:\n%s", body)
	}
	if !strings.Contains(body, "(application/octet-stream)") {
		t.Fatalf("malformed mime not replaced:\n%s", body)
	}
}

func TestSaveUploadKeepsWellFormedMime(t *testing.T) {
	root := t.TempDir()
	store := New(root, nil)
	up, err := store.SaveUpload("memo.m4a", "Standup notes", "audio/mp4",
		strings.NewReader("data"), time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	note, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(up.NoteRel)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(note), "(audio/mp4)") || !strings.Contains(string(note), "# Standup notes") {
		t.Fatalf("note body mangled:\n%s", note)
	}
}

func TestCreateTextNoteWritesTitleAndOptionalDescription(t *testing.T) {
	root := t.TempDir()
	store := New(root, nil)
	now := time.Date(2026, 8, 9, 15, 4, 5, 0, time.UTC)

	up, err := store.CreateTextNote("Trip reminder", "Pack chargers.\n\nLeave by 8.", now)
	if err != nil {
		t.Fatal(err)
	}
	if up.AssetRel != "" {
		t.Fatalf("text note unexpectedly has asset %q", up.AssetRel)
	}
	note, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(up.NoteRel)))
	if err != nil {
		t.Fatal(err)
	}
	body := string(note)
	for _, want := range []string{
		"type: source",
		"created: 2026-08-09",
		"tags: [share]",
		"# Trip reminder",
		"Pack chargers.\n\nLeave by 8.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("note missing %q:\n%s", want, body)
		}
	}
}

func TestCreateTextNoteRequiresTitleAndNeutralizesHTML(t *testing.T) {
	root := t.TempDir()
	store := New(root, nil)
	now := time.Date(2026, 8, 9, 15, 4, 5, 0, time.UTC)

	if _, err := store.CreateTextNote("   ", "description", now); err == nil {
		t.Fatal("empty title accepted")
	}
	up, err := store.CreateTextNote("<script>alert(1)</script>", "<img src=x onerror=alert(1)>", now)
	if err != nil {
		t.Fatal(err)
	}
	note, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(up.NoteRel)))
	if err != nil {
		t.Fatal(err)
	}
	body := string(note)
	if strings.Contains(body, "<script") || strings.Contains(body, "<img") {
		t.Fatalf("hostile HTML reached note body:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "&lt;img") {
		t.Fatalf("escaped text missing:\n%s", body)
	}
}

func TestCreateTextNoteUsesReachableFallbackFilenameForDegenerateTitles(t *testing.T) {
	root := t.TempDir()
	store := New(root, nil)
	now := time.Date(2026, 8, 9, 15, 4, 5, 0, time.UTC)

	for _, title := range []string{"中文标题", "..", "!!!", "index", "index!!!"} {
		up, err := store.CreateTextNote(title, "", now)
		if err != nil {
			t.Fatalf("title %q: %v", title, err)
		}
		base := strings.TrimSuffix(filepath.Base(up.NoteRel), ".md")
		if base == "" || base == "." || base == ".." || strings.TrimSpace(base) == "" {
			t.Errorf("title %q produced degenerate note path %q", title, up.NoteRel)
		}
		if up.NoteSlug == "_Inbox" || up.NoteSlug == "_Inbox/.." {
			t.Errorf("title %q produced unreachable slug %q", title, up.NoteSlug)
		}
	}
}

func TestCreateTextNoteCanonicalizesFilenameBeforeReservingUniqueSlug(t *testing.T) {
	root := t.TempDir()
	store := New(root, nil)
	now := time.Date(2026, 8, 9, 15, 4, 5, 0, time.UTC)
	seen := map[string]bool{}

	for _, title := range []string{"Foo-Bar", "Foo Bar", "FOO BAR"} {
		up, err := store.CreateTextNote(title, "", now)
		if err != nil {
			t.Fatalf("title %q: %v", title, err)
		}
		slug := strings.ToLower(up.NoteSlug)
		if seen[slug] {
			t.Fatalf("title %q reused URL slug %q", title, up.NoteSlug)
		}
		seen[slug] = true
	}
}
