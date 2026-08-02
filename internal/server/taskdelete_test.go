package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

func TestHandleTaskCancelThenPurgeRemovesTheLine(t *testing.T) {
	root := t.TempDir()
	rel := "Project.md"
	content := []byte("# Project\n\n- [ ] Obsolete idea 📅 2026-09-01\n- [ ] Keep me\n")
	if err := os.WriteFile(filepath.Join(root, rel), content, 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	engine := markdown.New("/")
	if err := db.Reconcile(v, engine); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{Vault: root, BaseURL: "/"}, db: db, engine: engine}
	auth := withSession(t, s)
	s.vault.Store(v)
	s.tree.Store(buildTree(v, "/"))

	// Cancel the dated task (triage cancel would refuse it).
	body := fmt.Sprintf(`{"slug":"Project","expectedVersion":%q,"items":[{"line":3,"expectedText":"Obsolete idea"}]}`,
		tasks.FileVersion(content))
	rec := httptest.NewRecorder()
	s.handleTaskCancel(rec, auth(httptest.NewRequest(http.MethodPost, "/api/tasks/cancel", bytes.NewBufferString(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", rec.Code, rec.Body.String())
	}
	afterCancel, _ := os.ReadFile(filepath.Join(root, rel))
	if !strings.Contains(string(afterCancel), "- [-] Obsolete idea 📅 2026-09-01 ❌ ") {
		t.Fatalf("cancel did not mark the line: %q", afterCancel)
	}

	// A stale purge (old version) must 409 without changing the file.
	stale := fmt.Sprintf(`{"slug":"Project","expectedVersion":%q,"lines":[3]}`, tasks.FileVersion(content))
	rec = httptest.NewRecorder()
	s.handleTaskPurge(rec, auth(httptest.NewRequest(http.MethodPost, "/api/tasks/purge", bytes.NewBufferString(stale))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale purge status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Purge with the current version removes the line.
	fresh := fmt.Sprintf(`{"slug":"Project","expectedVersion":%q,"lines":[3]}`, tasks.FileVersion(afterCancel))
	rec = httptest.NewRecorder()
	s.handleTaskPurge(rec, auth(httptest.NewRequest(http.MethodPost, "/api/tasks/purge", bytes.NewBufferString(fresh))))
	if rec.Code != http.StatusOK {
		t.Fatalf("purge status=%d body=%s", rec.Code, rec.Body.String())
	}
	final, _ := os.ReadFile(filepath.Join(root, rel))
	want := "# Project\n\n- [ ] Keep me\n"
	if string(final) != want {
		t.Fatalf("final=%q want=%q", final, want)
	}
	all, err := db.AllTasks()
	if err != nil || len(all) != 1 || all[0].Text != "Keep me" {
		t.Fatalf("reindex after purge: tasks=%#v err=%v", all, err)
	}
}
