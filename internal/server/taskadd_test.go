package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/config"
)

func TestHandleTaskAddAppendsToNoteAndCreatesDailyNote(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Inbox.md"), []byte("# Inbox\n\n- [ ] existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	auth := withSession(t, s)

	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/tasks/add", strings.NewReader(body))
		s.handleTaskAdd(rec, auth(req))
		return rec
	}

	// Append to an existing note by slug.
	if rec := post(`{"slug":"Inbox","text":"water the ferns"}`); rec.Code != http.StatusOK {
		t.Fatalf("append status = %d body=%s", rec.Code, rec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(root, "Inbox.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "# Inbox\n\n- [ ] existing\n- [ ] water the ferns\n" {
		t.Fatalf("inbox content = %q", content)
	}

	// Daily note is created on demand.
	rec := post(`{"daily":true,"text":"sweep the porch"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("daily status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	wantPath := "Daily Notes/" + time.Now().Format("2006-01-02") + ".md"
	if payload.Path != wantPath {
		t.Fatalf("path = %q, want %q", payload.Path, wantPath)
	}
	daily, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(wantPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(daily) != "- [ ] sweep the porch\n" {
		t.Fatalf("daily content = %q", daily)
	}
	// A second daily add appends to the now-existing note.
	if rec := post(`{"daily":true,"text":"call plumber"}`); rec.Code != http.StatusOK {
		t.Fatalf("second daily status = %d", rec.Code)
	}
	daily, _ = os.ReadFile(filepath.Join(root, filepath.FromSlash(wantPath)))
	if string(daily) != "- [ ] sweep the porch\n- [ ] call plumber\n" {
		t.Fatalf("daily after second add = %q", daily)
	}

	// Validation: unknown note, both/neither destination, multiline text.
	if rec := post(`{"slug":"Nope","text":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown slug status = %d", rec.Code)
	}
	if rec := post(`{"daily":true,"slug":"Inbox","text":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("both destinations status = %d", rec.Code)
	}
	if rec := post(`{"text":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no destination status = %d", rec.Code)
	}
	if rec := post(`{"daily":true,"text":"a\nb"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("multiline status = %d", rec.Code)
	}
}
