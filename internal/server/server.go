// Package server wires the HTTP routes for the notes site, APIs, kanban,
// and MCP endpoint. Handlers never touch the filesystem or database
// directly; they go through the vault/index/tasks/bases/kanban stores.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
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
	"github.com/base698/amythest/internal/tasks"
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

	rescanMu    sync.Mutex
	taskWriteMu sync.Mutex

	// Coalescing state for scheduleFullReconcile. reconcileDelay is the quiet
	// period before a background pass; zero means the default (tests shorten it).
	reconcileMu      sync.Mutex
	reconcileRunning bool
	reconcilePending bool
	reconcileDelay   time.Duration

	chromaCSS   []byte
	kanban      *board.Store   // nil when kanban is not configured
	kanbanAuth  *auth.Manager  // gates vault writes when set
	catalog     *bases.Catalog // sqlite tabular data store
	share       *share.Store
	metrics     serverMetrics
}

// kanbanSessionCookie mirrors httpapi.SessionCookie without importing the
// package into every handler file.
const kanbanSessionCookie = httpapi.SessionCookie

func jsonDecode(r *http.Request, into any) error {
	return jsonDecodeLimit(r, into, 1<<20)
}

func jsonDecodeLimit(r *http.Request, into any, limit int64) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	return dec.Decode(into)
}

func New(cfg config.Config) (*Server, error) {
	// Canonicalize once so every consumer — the vault scanner, the no-follow
	// task writer, the kanban store, share — agrees on one symlink-free root.
	resolvedVault, err := filepath.EvalSymlinks(cfg.Vault)
	if err != nil {
		return nil, fmt.Errorf("resolve vault path %q: %w", cfg.Vault, err)
	}
	cfg.Vault = resolvedVault
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

	// Kanban comes up before the first index pass: it supplies the board list
	// the renderer bakes into move-to-board controls on note task lines, and
	// that list feeds render-cache invalidation.
	if err := s.mountKanban(); err != nil {
		return nil, err
	}

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
	s.mux.HandleFunc("GET /api/note", s.handleNoteRaw)
	s.mux.HandleFunc("PUT /api/note", s.handleNoteWrite)
	s.mux.HandleFunc("GET /api/notes", s.handleNotesList)
	s.mux.HandleFunc("GET /api/bases", s.handleBasesList)
	s.mux.HandleFunc("GET /api/base", s.handleBaseData)
	s.mux.HandleFunc("GET /api/tasks", s.handleTasksAPI)
	s.mux.HandleFunc("POST /api/tasks/toggle", s.handleTaskToggle)
	s.mux.HandleFunc("POST /api/tasks/add", s.handleTaskAdd)
	s.mux.HandleFunc("POST /api/tasks/due", s.handleTaskDue)
	s.mux.HandleFunc("POST /api/tasks/recurrence", s.handleTaskRecurrence)
	s.mux.HandleFunc("POST /api/tasks/triage", s.handleTaskTriage)
	s.mux.HandleFunc("POST /api/tasks/file", s.handleTaskFileDisposition)
	s.mux.HandleFunc("POST /api/tasks/file/hide", s.handleTaskFileHide)
	s.mux.HandleFunc("POST /api/tasks/move-to-board", s.handleTaskMoveToBoard)
	s.mux.HandleFunc("POST /api/tasks/priority", s.handleTaskPriority)
	s.mux.HandleFunc("POST /api/tasks/cancel", s.handleTaskCancel)
	s.mux.HandleFunc("POST /api/tasks/purge", s.handleTaskPurge)
	s.mux.HandleFunc("POST /api/notes/property", s.handleNoteProperty)
	s.mux.HandleFunc("GET /tasks", s.handleTasksPage)
	s.mux.HandleFunc("GET /tasks/triage", s.handleTasksTriagePage)
	s.mux.HandleFunc("GET /share", s.handleSharePage)
	s.mux.HandleFunc("POST /api/share/upload", s.handleShareUpload)
	s.mux.HandleFunc("POST /api/share/text", s.handleShareText)
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

	if h := mcp.Handler(mcp.Deps{
		DB:      s.db,
		Catalog: s.catalog,
		Kanban:  s.kanban,
		Vault:   func() *vault.Vault { return s.vault.Load() },
		BaseURL: s.base(),
		Rescan:  s.rescanWhileVaultLocked,
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

// Rescan rebuilds the vault snapshot and reconciles the index. The shared vault
// lock serializes snapshots and commits across Amythest processes.
func (s *Server) Rescan() error {
	return tasks.WithVaultWriteLock(s.cfg.Vault, s.rescanWhileVaultLocked)
}

// rescanWhileVaultLocked is used by mutation helpers that already hold the
// shared vault lock, avoiding lock reentrancy while keeping their write and
// synchronous index commit in one critical section.
func (s *Server) rescanWhileVaultLocked() (err error) {
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

// scheduleFullReconcile runs one background rescan that re-renders the whole
// corpus, coalescing concurrent requests into a single pass — a burst of
// uploads must not queue a burst of full reconciles.
//
// Callers are mutations that already indexed their own note incrementally
// (see indexShareUpload). What is left to do is global and not
// latency-critical: a new slug can make a [[Link]] elsewhere resolve, and only
// a re-render notices. InvalidateRenderCache is what forces that pass, since
// the incremental insert otherwise leaves the index looking up to date.
// A short quiet period first, because the pass takes the same in-process
// rescan lock the incremental publish needs: without it, the second upload of
// a burst would wait out the first upload's reconcile and we would have moved
// the stall rather than removed it. One full pass covers every write that
// preceded it, so batching a burst costs nothing but freshness lag.
const defaultFullReconcileDelay = 3 * time.Second

func (s *Server) scheduleFullReconcile() {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	s.reconcilePending = true
	if s.reconcileRunning {
		return // the in-flight pass will cover this write, or loop once more
	}
	s.reconcileRunning = true
	go s.fullReconcileLoop()
}

func (s *Server) fullReconcileLoop() {
	delay := s.reconcileDelay
	if delay <= 0 {
		delay = defaultFullReconcileDelay
	}
	for {
		time.Sleep(delay)
		s.reconcileMu.Lock()
		s.reconcilePending = false
		s.reconcileMu.Unlock()

		if err := s.db.InvalidateRenderCache(); err != nil {
			slog.Warn("background reconcile invalidate", "err", err)
		} else if err := s.Rescan(); err != nil {
			slog.Warn("background reconcile", "err", err)
		}

		s.reconcileMu.Lock()
		done := !s.reconcilePending // nothing landed while we were reconciling
		s.reconcileRunning = !done
		s.reconcileMu.Unlock()
		if done {
			return
		}
	}
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
	for i := range results {
		results[i].Excerpt = snippetHTML(results[i].Excerpt)
	}
	s.writeJSON(w, results)
}

// snippetHTML escapes the raw FTS excerpt (note content is untrusted) and
// only then turns the store's control-character markers into highlights.
func snippetHTML(excerpt string) string {
	escaped := html.EscapeString(excerpt)
	escaped = strings.ReplaceAll(escaped, "\x02", "<b>")
	return strings.ReplaceAll(escaped, "\x03", "</b>")
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
