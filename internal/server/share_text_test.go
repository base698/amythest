package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func textShareRequest(t *testing.T, title, description string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"title":       title,
		"description": description,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/share/text", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestShareTextCreatesImmediatelyReachableNote(t *testing.T) {
	s, stamp := newShareTestServer(t, 10)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, stamp(textShareRequest(t, "Call the vet", "Ask about the refill.")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Note    string `json:"note"`
		NoteURL string `json:"noteURL"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Note == "" || resp.NoteURL == "" {
		t.Fatalf("incomplete response: %+v", resp)
	}
	page := httptest.NewRecorder()
	s.ServeHTTP(page, httptest.NewRequest(http.MethodGet, resp.NoteURL, nil))
	if page.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d", resp.NoteURL, page.Code)
	}
	if !strings.Contains(page.Body.String(), "Call the vet") || !strings.Contains(page.Body.String(), "Ask about the refill.") {
		t.Fatalf("rendered note missing text: %s", page.Body.String())
	}
	note, err := os.ReadFile(filepath.Join(s.cfg.Vault, filepath.FromSlash("_Inbox/call-the-vet.md")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(note), "type: source") {
		t.Fatalf("wrong note type:\n%s", note)
	}
}

func TestShareTextRequiresTitle(t *testing.T) {
	s, stamp := newShareTestServer(t, 1)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, stamp(textShareRequest(t, "   ", "description")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestShareTextIndexTitleGetsDistinctReachableURL(t *testing.T) {
	s, stamp := newShareTestServer(t, 1)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, stamp(textShareRequest(t, "index", "Folder-safe note.")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		NoteURL string `json:"noteURL"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.NoteURL == "/_Inbox" || resp.NoteURL == "/_Inbox/" {
		t.Fatalf("index title took over folder URL: %q", resp.NoteURL)
	}
	page := httptest.NewRecorder()
	s.ServeHTTP(page, httptest.NewRequest(http.MethodGet, resp.NoteURL, nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Folder-safe note.") {
		t.Fatalf("GET %s: status %d body %s", resp.NoteURL, page.Code, page.Body.String())
	}
}

func TestSharePageOffersTextNoteForm(t *testing.T) {
	s, _ := newShareTestServer(t, 1)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/share", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"share-text-title", "share-text-description", "share-text-save", "Quick text note"} {
		if !strings.Contains(body, want) {
			t.Errorf("share page missing %q", want)
		}
	}
}
