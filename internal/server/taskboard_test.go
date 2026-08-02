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
	"time"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

func TestHandleTaskMoveToBoardCreatesTriageCardConvertsSourceAndReindexes(t *testing.T) {
	root := t.TempDir()
	content := []byte("# Project\n\n- [ ] Ship release 📅 2026-08-20 #launch\n")
	if err := os.WriteFile(filepath.Join(root, "Project.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	store := board.NewStore(filepath.Join(root, "kanban"), func() time.Time {
		return time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	})
	createdBoard, err := store.CreateBoard("product")
	if err != nil {
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
	engine := markdown.New("/notes/")
	if err := db.Reconcile(v, engine); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{Vault: root, BaseURL: "/notes/"}, db: db, engine: engine, kanban: store}
	s.vault.Store(v)
	s.tree.Store(buildTree(v, "/notes/"))

	payload := fmt.Sprintf(`{"board":"product","slug":"Project","line":3,"expectedText":"Ship release","expectedStatus":"open","expectedVersion":%q}`, tasks.FileVersion(content))
	request := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleTaskMoveToBoard(rec, httptest.NewRequest(http.MethodPost, "/notes/api/tasks/move-to-board", bytes.NewBufferString(payload)))
		return rec
	}
	if rec := request(); rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	loaded, err := store.Load("product")
	if err != nil || len(loaded.Cards) != 1 {
		t.Fatalf("board=%#v err=%v", loaded, err)
	}
	card := loaded.Cards[0]
	if card.Title != "Ship release" || card.DueDate != "2026-08-20" || card.Status != board.Triage || card.Assignee != "" || card.Agent != "" {
		t.Fatalf("card=%#v", card)
	}
	if !strings.Contains(card.Description, "[[Project]]") || !strings.Contains(card.Description, "Project.md") {
		t.Fatalf("description lacks provenance: %q", card.Description)
	}
	if loaded.DispatchEnabled != createdBoard.DispatchEnabled {
		t.Fatal("dispatcher setting changed")
	}
	source, _ := os.ReadFile(filepath.Join(root, "Project.md"))
	wantLink := "[[kanban/product/board#^card-" + card.ID
	if strings.Contains(string(source), "[ ]") || !strings.Contains(string(source), wantLink) || !strings.Contains(string(source), "📅 2026-08-20 #launch") {
		t.Fatalf("source=%q", source)
	}
	all, err := db.AllTasks()
	if err != nil || len(all) != 0 {
		t.Fatalf("tasks=%#v err=%v", all, err)
	}
	if rec := request(); rec.Code != http.StatusConflict {
		t.Fatalf("retry status=%d body=%s", rec.Code, rec.Body.String())
	}
	loaded, _ = store.Load("product")
	if len(loaded.Cards) != 1 {
		t.Fatalf("retry created duplicate cards: %#v", loaded.Cards)
	}
}

func TestHandleTaskMoveToBoardRejectsUnavailableAndMissingBoard(t *testing.T) {
	for name, server := range map[string]*Server{
		"unavailable": {},
		"missing":     {kanban: board.NewStore(t.TempDir(), time.Now)},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			body := `{"board":"missing","slug":"Project","line":1,"expectedText":"Task","expectedStatus":"open","expectedVersion":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
			server.handleTaskMoveToBoard(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/move-to-board", bytes.NewBufferString(body)))
			if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
