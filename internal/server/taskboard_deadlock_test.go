package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

// Moving a task to a board holds that board's lock for the whole operation,
// including the reindex at the end. The renderer fingerprints the board set
// during reindex, so if that path goes through the store's mutex it re-enters
// a non-reentrant lock and wedges the board forever — every later kanban call
// then blocks and the UI goes dead. Regression: this must finish.
func TestMoveToBoardDoesNotDeadlockAgainstRendererBoardLookup(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("# Project\n\n- [ ] Ship release 📅 2026-08-20\n")
	if err := os.WriteFile(filepath.Join(root, "Project.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	store := board.NewStore(filepath.Join(root, "kanban"), time.Now)
	if _, err := store.CreateBoard("proof"); err != nil {
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
	s := &Server{cfg: config.Config{Vault: root, BaseURL: "/"}, db: db, engine: engine, kanban: store}
	// The wiring that caused the deadlock: the renderer resolves board names
	// on every reconcile.
	engine.SetBoards(s.boardNames)
	if err := db.Reconcile(v, engine); err != nil {
		t.Fatal(err)
	}
	auth := withSession(t, s)
	s.vault.Store(v)
	s.tree.Store(buildTree(v, "/"))

	body := fmt.Sprintf(`{"board":"proof","slug":"Project","line":3,"expectedText":"Ship release","expectedStatus":"open","expectedVersion":%q}`,
		tasks.FileVersion(content))

	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		s.handleTaskMoveToBoard(rec, auth(httptest.NewRequest(http.MethodPost, "/api/tasks/move-to-board", bytes.NewBufferString(body))))
		done <- rec.Code
	}()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("move status=%d", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("move to board deadlocked: the renderer's board lookup re-entered the board lock")
	}

	// The board must still be usable afterwards — a leaked lock shows up here.
	listed := make(chan error, 1)
	go func() {
		_, err := store.ListBoards()
		listed <- err
	}()
	select {
	case err := <-listed:
		if err != nil {
			t.Fatalf("ListBoards after move: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("board lock still held after move: kanban would be unresponsive")
	}

	loaded, err := store.Load("proof")
	if err != nil || len(loaded.Cards) != 1 {
		t.Fatalf("board=%#v err=%v", loaded, err)
	}
}
