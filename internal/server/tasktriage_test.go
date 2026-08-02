package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

func TestServerRescanWaitsForSharedVaultCriticalSection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Project.md"), []byte("- [ ] Task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Vault: root, DataDir: t.TempDir(), BaseURL: "/", SiteName: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	lockEntered := make(chan struct{})
	releaseLock := make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- tasks.WithVaultWriteLock(root, func() error {
			close(lockEntered)
			<-releaseLock
			return nil
		})
	}()
	<-lockEntered
	rescanDone := make(chan error, 1)
	go func() { rescanDone <- s.Rescan() }()
	select {
	case err := <-rescanDone:
		t.Fatalf("rescan escaped shared vault lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseLock)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	if err := <-rescanDone; err != nil {
		t.Fatal(err)
	}
}

func TestHandleTaskToggleRequiresExpectedVersion(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleTaskToggle(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/toggle", bytes.NewBufferString(
		`{"slug":"Project","line":1,"done":true}`,
	)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleTaskToggleRejectsStaleVersionAfterLineShift(t *testing.T) {
	root := t.TempDir()
	original := []byte("- [ ] Recurring 🔁 every week\n- [ ] Other\n")
	path := filepath.Join(root, "Project.md")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	shifted := []byte("- [ ] Inserted\n- [ ] Recurring 🔁 every week\n- [ ] Other\n")
	if err := os.WriteFile(path, shifted, 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{Vault: root}}
	s.vault.Store(v)
	body := fmt.Sprintf(`{"slug":"Project","line":1,"expectedVersion":%q,"done":true}`, tasks.FileVersion(original))
	rec := httptest.NewRecorder()
	s.handleTaskToggle(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/toggle", bytes.NewBufferString(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != string(shifted) {
		t.Fatalf("stale request mutated shifted task: %q err=%v", got, readErr)
	}
}

func TestHandleTaskDueChangesAndReindexesVaultTask(t *testing.T) {
	root := t.TempDir()
	rel := "Project.md"
	if err := os.WriteFile(filepath.Join(root, rel), []byte("# Project\n\n- [ ] Ship release 📅 2026-08-15\n"), 0o644); err != nil {
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

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/due", bytes.NewBufferString(
		`{"slug":"Project","line":3,"expectedText":"Ship release","expectedStatus":"open","expectedDue":"2026-08-15","due":"2026-08-20"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleTaskDue(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	all, err := db.AllTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Due != "2026-08-20" {
		t.Fatalf("reindexed task = %#v", all)
	}
}

func TestHandleTaskDueClearsDueDate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Project.md"), []byte("- [ ] Ship release 📅 2026-08-15\n"), 0o644); err != nil {
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

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/due", bytes.NewBufferString(
		`{"slug":"Project","line":1,"expectedText":"Ship release","expectedStatus":"open","expectedDue":"2026-08-15","due":""}`,
	))
	rec := httptest.NewRecorder()
	s.handleTaskDue(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	all, err := db.AllTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Due != "" {
		t.Fatalf("reindexed task = %#v", all)
	}
}

func TestHandleTaskFileHideAddsFrontmatterAndReindexes(t *testing.T) {
	root := t.TempDir()
	rel := "Project.md"
	content := []byte("# Project\n\n- [ ] Hide me\n")
	if err := os.WriteFile(filepath.Join(root, rel), content, 0o640); err != nil {
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

	body := fmt.Sprintf(`{"slug":"Project","expectedVersion":%q}`, tasks.FileVersion(content))
	rec := httptest.NewRecorder()
	s.handleTaskFileHide(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/file/hide", bytes.NewBufferString(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	got, _ := os.ReadFile(filepath.Join(root, rel))
	if !strings.HasPrefix(string(got), "---\ntasks: false\n---\n# Project") {
		t.Fatalf("hidden file=%q", got)
	}
	all, err := db.AllTasks()
	if err != nil || len(all) != 0 {
		t.Fatalf("tasks=%#v err=%v", all, err)
	}
	results, err := db.Search("Project", 10, false)
	if err != nil || len(results) != 1 {
		t.Fatalf("note no longer searchable: %#v err=%v", results, err)
	}
}

func TestHandleTaskFileDispositionArchivesAndReindexes(t *testing.T) {
	root := t.TempDir()
	rel := "Projects/Old.md"
	if err := os.MkdirAll(filepath.Join(root, "Projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("# Old\n\n- [ ] Retire this\n")
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), content, 0o644); err != nil {
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

	body := fmt.Sprintf(`{"slug":"Projects/Old","action":"archive","expectedVersion":%q}`, tasks.FileVersion(content))
	rec := httptest.NewRecorder()
	s.handleTaskFileDisposition(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/file", bytes.NewBufferString(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	destination := "Archive/Deleted/Projects/Old.md"
	if got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(destination))); err != nil || string(got) != string(content) {
		t.Fatalf("archived content=%q err=%v", got, err)
	}
	all, err := db.AllTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Path != destination {
		t.Fatalf("archive was not reindexed at destination: %#v", all)
	}
}

func TestHandleTaskFileDispositionRequiresVersionAndKnownAction(t *testing.T) {
	for _, body := range []string{
		`{"slug":"Project","action":"trash"}`,
		`{"slug":"Project","action":"delete","expectedVersion":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
	} {
		s := &Server{}
		rec := httptest.NewRecorder()
		s.handleTaskFileDisposition(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/file", bytes.NewBufferString(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, rec.Code, rec.Body.String())
		}
	}
}

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

func TestHandleTaskTriageSupportsFiveThousandRealisticTasks(t *testing.T) {
	root := t.TempDir()
	var note strings.Builder
	note.WriteString("# Large checklist\n")
	items := make([]map[string]any, 0, taskTriageBatchLimit)
	for i := 1; i <= taskTriageBatchLimit; i++ {
		text := fmt.Sprintf("Item %d %s", i, strings.TrimSpace(strings.Repeat("detail ", 22)))
		note.WriteString("- [ ] " + text + "\n")
		items = append(items, map[string]any{"line": i + 1, "action": "reference", "expectedText": text})
	}
	if err := os.WriteFile(filepath.Join(root, "Large.md"), []byte(note.String()), 0o644); err != nil {
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
	payload, err := json.Marshal(map[string]any{"slug": "Large", "items": items})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= 1<<20 {
		t.Fatalf("regression payload must exceed old 1 MiB limit, got %d bytes", len(payload))
	}
	rec := httptest.NewRecorder()
	s.handleTaskTriage(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/triage", bytes.NewReader(payload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out, err := os.ReadFile(filepath.Join(root, "Large.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "- [ ]") {
		t.Fatal("reference conversion left task checkboxes behind")
	}
	all, err := db.AllTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("reindex retained %d converted tasks", len(all))
	}
}
