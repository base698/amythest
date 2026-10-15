package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/config"
)

func TestTasksPageListsChip(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Watchlist.md", "---\ntasks: list\n---\n- [ ] Zebra film\n")
	write("Work.md", "- [ ] Ship the report\n")
	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	page := func(q string) string {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", "/tasks"+q, nil))
		if rec.Code != 200 {
			t.Fatalf("GET /tasks%s = %d", q, rec.Code)
		}
		return rec.Body.String()
	}
	off := page("")
	if strings.Contains(off, "Zebra film") || !strings.Contains(off, "Ship the report") {
		t.Fatal("dashboard without lists should show work only")
	}
	on := page("?lists=1")
	lists := strings.Index(on, "<h2>Lists</h2>")
	if lists < 0 || !strings.Contains(on[lists:], "Zebra film") {
		t.Fatal("dashboard with lists should show list items under a Lists heading")
	}
	if noDue := strings.Index(on, "<h2>No due date</h2>"); noDue < 0 || strings.Contains(on[noDue:lists], "Zebra film") {
		t.Fatal("list items belong in the Lists section, not No due date")
	}
	if open := page("?status=open&lists=1"); !strings.Contains(open, "Zebra film") || !strings.Contains(open, "Ship the report") {
		t.Fatal("the open view with lists should show both")
	}
}
