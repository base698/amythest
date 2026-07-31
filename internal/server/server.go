// Package server wires the HTTP routes for the notes site, APIs, kanban,
// and MCP endpoint. Handlers never touch the filesystem or database
// directly; they go through the vault/index/tasks/bases/kanban stores.
package server

import (
	"context"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/base698/amythest/internal/bases"
	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/kanban/auth"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/kanban/httpapi"
	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/mcp"
	"github.com/base698/amythest/internal/share"
	"github.com/base698/amythest/internal/vault"
	"github.com/base698/amythest/web"
)

type Server struct {
	cfg    config.Config
	tmpl   *template.Template
	mux    *http.ServeMux
	engine *markdown.Engine
	db     *index.DB

	vault atomic.Pointer[vault.Vault]
	tree  atomic.Pointer[treeNode]

	rescanMu   sync.Mutex
	chromaCSS  []byte
	kanban     *board.Store   // nil when kanban is not configured
	kanbanAuth *auth.Manager  // gates vault writes when set
	catalog    *bases.Catalog // sqlite tabular data store
	share      *share.Store
	metrics    serverMetrics
}

// kanbanSessionCookie mirrors httpapi.SessionCookie without importing the
// package into every handler file.
const kanbanSessionCookie = httpapi.SessionCookie

func jsonDecode(r *http.Request, into any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(into)
}

func New(cfg config.Config) (*Server, error) {
	tmpl, err := template.ParseFS(web.FS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	db, err := index.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	catalog, err := bases.OpenCatalog(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:     cfg,
		tmpl:    tmpl,
		mux:     http.NewServeMux(),
		engine:  markdown.New(cfg.BaseURL),
		db:      db,
		catalog: catalog,
	}

	s.share = share.New(cfg.Vault, cfg.SharePlugins)

	if err := s.Rescan(); err != nil {
		return nil, err
	}

	s.chromaCSS = []byte(markdown.ChromaCSS("github", "") +
		markdown.ChromaCSS("github-dark", `[data-theme="dark"]`))

	dist, err := fs.Sub(web.FS, "dist")
	if err != nil {
		return nil, err
	}
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(dist)))
	s.mux.HandleFunc("GET /gen/chroma.css", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(s.chromaCSS)
	})
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)

	s.mux.HandleFunc("GET /api/search", s.handleSearch)
	s.mux.HandleFunc("GET /api/contentIndex", s.handleContentIndex)
	s.mux.HandleFunc("GET /api/tasks", s.handleTasksAPI)
	s.mux.HandleFunc("POST /api/tasks/toggle", s.handleTaskToggle)
	s.mux.HandleFunc("GET /tasks", s.handleTasksPage)
	s.mux.HandleFunc("GET /share", s.handleSharePage)
	s.mux.HandleFunc("POST /api/share/upload", s.handleShareUpload)
	s.mux.HandleFunc("GET /bases/", s.handleBases)
	s.mux.HandleFunc("GET /db", s.handleDB)
	s.mux.HandleFunc("GET /db/", s.handleDB)
	s.mux.HandleFunc("POST /api/db/query", s.handleDBQueryAPI)

	s.mux.HandleFunc("GET /assets/", s.handleAsset)
	s.mux.HandleFunc("GET /tags/", s.handleTags)
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleNote(w, r)
	})

	if err := s.mountKanban(); err != nil {
		return nil, err
	}

	if h := mcp.Handler(mcp.Deps{
		DB:      s.db,
		Catalog: s.catalog,
		Kanban:  s.kanban,
		Vault:   func() *vault.Vault { return s.vault.Load() },
		BaseURL: s.base(),
		Rescan:  s.Rescan,
	}); h != nil {
		s.mux.Handle("/mcp", h)
		s.mux.Handle("/mcp/", h)
		slog.Info("mcp enabled at /mcp")
	} else {
		slog.Info("mcp disabled: set AMYTHEST_MCP_TOKEN to enable")
	}
	return s, nil
}

func (s *Server) Close() error {
	if s.catalog != nil {
		_ = s.catalog.Close()
	}
	return s.db.Close()
}

// Rescan rebuilds the vault snapshot and reconciles the index. Safe to call
// concurrently; runs are serialized.
func (s *Server) Rescan() (err error) {
	s.rescanMu.Lock()
	defer s.rescanMu.Unlock()
	start := time.Now()
	defer func() {
		s.metrics.observeRescan(time.Since(start), err)
	}()
	v, err := vault.Scan(s.cfg.Vault)
	if err != nil {
		return err
	}
	if err := s.db.Reconcile(v, s.engine); err != nil {
		return err
	}
	s.vault.Store(v)
	s.tree.Store(buildTree(v, s.base()))
	slog.Debug("vault scan", "notes", len(v.Notes), "assets", len(v.Assets), "took", time.Since(start))
	return nil
}

// RunRescanLoop rescans on the configured interval until ctx is done. The
// periodic rescan is the source of truth for freshness — no watcher to die.
func (s *Server) RunRescanLoop(ctx context.Context) {
	interval := s.cfg.RescanInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Rescan(); err != nil {
				slog.Error("rescan", "err", err)
			}
		}
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	s.metrics.inFlight.Add(1)
	defer s.metrics.inFlight.Add(-1)

	mw := &metricsResponseWriter{ResponseWriter: w, status: http.StatusOK}
	s.mux.ServeHTTP(mw, r)
	s.metrics.observeRequest(r.Method, mw.status, time.Since(start))
}

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write json", "err", err)
	}
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	includeArchived := parseBoolParam(r.URL.Query().Get("include_archived"))
	results, err := s.db.Search(q, 10, includeArchived)
	if err != nil {
		slog.Warn("search", "q", q, "err", err)
		results = nil
	}
	if results == nil {
		results = []index.SearchResult{}
	}
	s.writeJSON(w, results)
}

// parseBoolParam accepts the common truthy query-string spellings; anything
// else (including empty/absent) is false, preserving default behavior.
func parseBoolParam(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Server) handleContentIndex(w http.ResponseWriter, r *http.Request) {
	ci, err := s.db.ContentIndex()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.writeJSON(w, ci)
}
