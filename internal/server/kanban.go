package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/base698/amythest/internal/kanban/auth"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/kanban/httpapi"
	"github.com/base698/amythest/web"
)

// mountKanban wires the kanban app at /kanban/ when credentials are
// configured. Boards live inside the vault at {vault}/kanban — the same
// markdown-canonical format as the original Netexplore server, and the wire
// API is identical so existing clients keep working.
func (s *Server) mountKanban() error {
	username := os.Getenv("KANBAN_USERNAME")
	password := os.Getenv("KANBAN_PASSWORD")
	secret := os.Getenv("KANBAN_SESSION_SECRET")
	if username == "" || password == "" || secret == "" {
		slog.Info("kanban disabled: set KANBAN_USERNAME, KANBAN_PASSWORD, KANBAN_SESSION_SECRET to enable")
		return nil
	}

	manager, err := auth.NewManager(username, password, []byte(secret), 8*time.Hour)
	if err != nil {
		return err
	}
	root := os.Getenv("KANBAN_ROOT")
	if root == "" {
		root = filepath.Join(s.cfg.Vault, "kanban")
	}
	store := board.NewStore(root, time.Now)

	assets, err := fs.Sub(web.FS, "dist/kanban-ui")
	if err != nil {
		return err
	}
	handler := httpapi.New(httpapi.Config{
		Store:        store,
		Auth:         manager,
		Assets:       assets,
		Now:          time.Now,
		CookieSecure: os.Getenv("KANBAN_COOKIE_SECURE") == "true",
	})
	s.kanban = store
	s.kanbanAuth = manager
	// Task lines inside notes offer move-to-board, so the renderer needs the
	// board list (and re-renders when it changes — see Engine.RenderSalt).
	s.engine.SetBoards(s.boardNames)
	s.mux.Handle("/kanban/", http.StripPrefix("/kanban", handler))
	slog.Info("kanban enabled", "root", root)
	return nil
}
