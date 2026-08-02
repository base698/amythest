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
