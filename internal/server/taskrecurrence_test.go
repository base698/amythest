package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/tasks"
)

func TestHandleTaskRecurrenceSetsAndClearsRule(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("- [ ] Shave 🔁 every week on Wednesday, Saturday 🏁 delete 📅 2026-08-12\n")
	if err := os.WriteFile(filepath.Join(root, "Chores.md"), content, 0o644); err != nil {
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
		req := httptest.NewRequest(http.MethodPost, "/api/tasks/recurrence", strings.NewReader(body))
		s.handleTaskRecurrence(rec, auth(req))
		return rec
	}
	version := tasks.FileVersion(content)

	// The parsed expectedRecurrence excludes the 🏁 field (marker-boundary fix).
	body := fmt.Sprintf(`{"slug":"Chores","line":1,"expectedText":"Shave","expectedStatus":"open",
		"expectedRecurrence":"every week on Wednesday, Saturday","expectedVersion":%q,
		"recurrence":"every 4 days when done"}`, version)
	if rec := post(body); rec.Code != http.StatusOK {
		t.Fatalf("set status = %d body=%s", rec.Code, rec.Body.String())
	}
	after, _ := os.ReadFile(filepath.Join(root, "Chores.md"))
	want := "- [ ] Shave 🔁 every 4 days when done 🏁 delete 📅 2026-08-12\n"
	if string(after) != want {
		t.Fatalf("file = %q, want %q", after, want)
	}

	// Stale version → 409.
	if rec := post(body); rec.Code != http.StatusConflict {
		t.Fatalf("stale status = %d", rec.Code)
	}

	// Invalid rule → 400.
	badRule := fmt.Sprintf(`{"slug":"Chores","line":1,"expectedText":"Shave","expectedStatus":"open",
		"expectedRecurrence":"every 4 days when done","expectedVersion":%q,"recurrence":"every blue moon"}`,
		tasks.FileVersion(after))
	if rec := post(badRule); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid rule status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Clear the rule.
	clear := fmt.Sprintf(`{"slug":"Chores","line":1,"expectedText":"Shave","expectedStatus":"open",
		"expectedRecurrence":"every 4 days when done","expectedVersion":%q,"recurrence":""}`,
		tasks.FileVersion(after))
	if rec := post(clear); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d body=%s", rec.Code, rec.Body.String())
	}
	after, _ = os.ReadFile(filepath.Join(root, "Chores.md"))
	if string(after) != "- [ ] Shave 🏁 delete 📅 2026-08-12\n" {
		t.Fatalf("cleared file = %q", after)
	}
}
