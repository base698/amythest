package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
)

func TestHandleTaskTriageMutatesAndReindexesVaultTask(t *testing.T) {
	root := t.TempDir()
	rel := "Project.md"
	if err := os.WriteFile(filepath.Join(root, rel), []byte("# Project\n\n- [ ] Choose a direction\n"), 0o644); err != nil {
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
	s.vault.Store(v)
	s.tree.Store(buildTree(v, "/"))

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/triage", bytes.NewBufferString(
		`{"slug":"Project","line":3,"action":"backlog","expectedText":"Choose a direction"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleTaskTriage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Choose a direction #backlog") {
		t.Fatalf("file was not triaged:\n%s", out)
	}
	all, err := db.AllTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || len(all[0].Tags) != 1 || all[0].Tags[0] != "backlog" {
		t.Fatalf("reindexed task = %#v", all)
	}
}

func TestTasksTriageRouteRendersNoDateQueue(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Project.md"), []byte("# Project\n\n- [ ] Choose a direction\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{
		Vault: root, DataDir: t.TempDir(), BaseURL: "/", SiteName: "Test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tasks/triage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Triage by file", "Choose a direction", "Keep as backlog", `class="triage-context"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestHandleTaskTriageAppliesFileBatch(t *testing.T) {
	root := t.TempDir()
	rel := "Checklist.md"
	if err := os.WriteFile(filepath.Join(root, rel), []byte("# Checklist\n- [ ] First\n- [ ] Second\n"), 0o644); err != nil {
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
	s.vault.Store(v)
	s.tree.Store(buildTree(v, "/"))

	body := `{"slug":"Checklist","items":[{"line":2,"action":"reference","expectedText":"First"},{"line":3,"action":"reference","expectedText":"Second"}]}`
	rec := httptest.NewRecorder()
	s.handleTaskTriage(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/triage", bytes.NewBufferString(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "# Checklist\n- First\n- Second\n"; got != want {
		t.Fatalf("out=%q want=%q", got, want)
	}
}

func TestHandleTaskTriageRejectsUnknownActionAtContractBoundary(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	body := `{"slug":"Project","line":1,"action":"delete","expectedText":"Task"}`
	s.handleTaskTriage(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/triage", bytes.NewBufferString(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
