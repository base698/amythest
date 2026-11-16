package server

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	username, err := credentialEnv("AMYTHEST_USERNAME", "KANBAN_USERNAME")
	if err != nil {
		return err
	}
	password, err := credentialEnv("AMYTHEST_PASSWORD", "KANBAN_PASSWORD")
	if err != nil {
		return err
	}
	secret, err := credentialEnv("AMYTHEST_SESSION_SECRET", "KANBAN_SESSION_SECRET")
	if err != nil {
		return err
	}
	if username == "" || password == "" || secret == "" {
		slog.Info("kanban disabled: set KANBAN_USERNAME, KANBAN_PASSWORD, KANBAN_SESSION_SECRET to enable")
		return nil
	}

	var manager *auth.Manager
	if s.cfg.AuthMode == "private" {
		ttl := s.cfg.AuthSessionTTL
		if ttl == 0 {
			ttl = auth.DefaultSessionTTL
		}
		if ttl < 24*time.Hour || ttl > 365*24*time.Hour {
			return fmt.Errorf("auth_session_ttl must be between 24h and 8760h")
		}
		if s.cfg.AuthPublicURL != "" {
			u, e := url.Parse(s.cfg.AuthPublicURL)
			if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(s.cfg.AuthAllowHTTP && u.Scheme == "http")) {
				return fmt.Errorf("auth_public_url must be a canonical HTTPS URL")
			}
			if strings.TrimRight(u.Path, "/") != strings.TrimRight(s.cfg.BaseURL, "/") {
				return fmt.Errorf("auth_public_url path must match base_url")
			}
		}
		if s.cfg.AuthAllowHTTP {
			host, _, e := net.SplitHostPort(s.cfg.Listen)
			if e != nil || !(host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
				return fmt.Errorf("auth_allow_http requires a loopback listener")
			}
		}
		manager, err = auth.OpenManager(filepath.Join(s.cfg.DataDir, "auth.db"), username, password, []byte(secret), ttl)
	} else {
		manager, err = auth.NewManager(username, password, []byte(secret), 8*time.Hour)
	}
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

func credentialEnv(current, legacy string) (string, error) {
	a, b := os.Getenv(current), os.Getenv(legacy)
	if a != "" && b != "" && a != b {
		return "", fmt.Errorf("conflicting %s and %s", current, legacy)
	}
	if a != "" {
		return a, nil
	}
	return b, nil
}
