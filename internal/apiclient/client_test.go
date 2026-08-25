package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
)

var frozenNow = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

// fakeServer is a minimal amythest stand-in that records auth headers.
type fakeServer struct {
	*httptest.Server
	logins      int
	sawCSRF     map[string]string // path -> X-CSRF-Token header value
	failFirst        bool // return 401 on the first authed request
	failedOnce       bool
	toggleCode       int
	toggleCalls      int
	contentIndexHits int
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	fs := &fakeServer{sawCSRF: map[string]string{}, toggleCode: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /kanban/api/login", func(w http.ResponseWriter, r *http.Request) {
		var creds struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil || creds.Username != "smoke" || creds.Password != "smoketest123" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
			return
		}
		fs.logins++
		http.SetCookie(w, &http.Cookie{Name: "amythest_kanban_session", Value: "tok", Path: "/"})
		json.NewEncoder(w).Encode(map[string]string{"user": "smoke", "csrf": "csrf-token"})
	})
	authed := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if fs.failFirst && !fs.failedOnce {
				fs.failedOnce = true
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if c, err := r.Cookie("amythest_kanban_session"); err != nil || c.Value != "tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fs.sawCSRF[r.URL.Path] = r.Header.Get("X-CSRF-Token")
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/tasks", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{
			"name": "",
			"tasks": []map[string]any{{
				"slug": "chores", "path": "chores.md", "line": 3,
				"text": "water the ferns", "status": "open", "priority": 3,
				"version": "abc123",
			}},
		}})
	}))
	mux.HandleFunc("POST /api/tasks/toggle", authed(func(w http.ResponseWriter, r *http.Request) {
		fs.toggleCalls++
		if fs.toggleCode != http.StatusOK {
			w.WriteHeader(fs.toggleCode)
			json.NewEncoder(w).Encode(map[string]string{"error": "task file changed; refresh and retry"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "recurred": false})
	}))
	mux.HandleFunc("POST /api/tasks/recurrence", authed(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Slug            string `json:"slug"`
			Line            int    `json:"line"`
			ExpectedVersion string `json:"expectedVersion"`
			Recurrence      string `json:"recurrence"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.ExpectedVersion) != 64 || payload.Line < 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "recurrence": payload.Recurrence})
	}))
	mux.HandleFunc("POST /api/tasks/cancel", authed(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Slug            string `json:"slug"`
			ExpectedVersion string `json:"expectedVersion"`
			Items           []struct {
				Line         int    `json:"line"`
				ExpectedText string `json:"expectedText"`
			} `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Items) != 1 || len(payload.ExpectedVersion) != 64 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	mux.HandleFunc("POST /api/tasks/purge", authed(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Slug            string `json:"slug"`
			ExpectedVersion string `json:"expectedVersion"`
			Lines           []int  `json:"lines"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Lines) != 1 || len(payload.ExpectedVersion) != 64 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	mux.HandleFunc("DELETE /kanban/api/boards/{board}/cards/{card}", authed(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("POST /api/tasks/add", authed(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Slug  string `json:"slug"`
			Daily bool   `json:"daily"`
			Text  string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Text == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		path := payload.Slug + ".md"
		if payload.Daily {
			path = "Daily Notes/2026-08-11.md"
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": path})
	}))
	mux.HandleFunc("POST /kanban/api/boards/{board}/cards", authed(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Title  string `json:"title"`
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Title == "" || payload.Status == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": "new1", "title": payload.Title, "status": payload.Status})
	}))
	mux.HandleFunc("GET /kanban/api/boards", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"name": "personal", "displayName": "Personal"}})
	}))
	mux.HandleFunc("GET /api/contentIndex", authed(func(w http.ResponseWriter, r *http.Request) {
		fs.contentIndexHits++
		json.NewEncoder(w).Encode(map[string]any{
			"Garden":      map[string]any{"title": "Garden", "links": []string{"Chores-List"}},
			"Chores-List": map[string]any{"title": "Chores List", "links": []string{"Garden"}},
			"Home":        map[string]any{"title": "Home", "links": []string{"Garden", "Chores-List"}},
		})
	}))
	mux.HandleFunc("GET /api/notes", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"slug": "Garden", "title": "Garden", "path": "Garden.md", "mtime": 1756000000, "size": 120},
			{"slug": "Home", "title": "Home", "path": "Home.md", "mtime": 1756000500, "size": 80},
		})
	}))
	mux.HandleFunc("GET /api/bases", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]string{"Projects"})
	}))
	mux.HandleFunc("GET /api/base", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"name": "Projects", "views": []string{"All"},
			"data": map[string]any{
				"view": "All", "columns": []string{"Name", "Status"},
				"groups": []map[string]any{{"rows": [][]string{{"Garden", "active"}}, "slugs": []string{"Garden"}}},
			},
		})
	}))
	mux.HandleFunc("POST /kanban/api/boards/{board}/cards/{card}/comments", authed(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Body == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":       r.PathValue("card"),
			"comments": []map[string]any{{"id": "cm1", "body": payload.Body}},
		})
	}))
	mux.HandleFunc("POST /kanban/api/boards/{board}/cards/{card}/board", authed(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			DestinationBoard string `json:"destinationBoard"`
			Confirm          bool   `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.DestinationBoard == "" || !payload.Confirm {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("card")})
	}))
	mux.HandleFunc("PUT /kanban/api/boards/{board}/cards/{card}", authed(func(w http.ResponseWriter, r *http.Request) {
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, ok := patch["title"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "unexpected field in partial patch"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("card"), "status": "done"})
	}))
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)
	return fs
}

func testClient(t *testing.T, srv *fakeServer) *Client {
	t.Helper()
	t.Setenv("KANBAN_USERNAME", "smoke")
	t.Setenv("KANBAN_PASSWORD", "smoketest123")
	c := New(Config{
		Endpoint:    srv.URL,
		Timeout:     5 * time.Second,
		SessionFile: filepath.Join(t.TempDir(), "session.json"),
	})
	c.now = func() time.Time { return frozenNow }
	return c
}

func TestLoginCachesSessionInKanbanPyFormat(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)

	groups, err := c.ListTasks(context.Background(), "not done")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Tasks) != 1 || groups[0].Tasks[0].Text != "water the ferns" {
		t.Fatalf("groups = %+v", groups)
	}
	if srv.logins != 1 {
		t.Fatalf("logins = %d", srv.logins)
	}

	info, err := os.Stat(c.cfg.SessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session file mode = %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(c.cfg.SessionFile)
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s["cookie"] != "amythest_kanban_session=tok" || s["csrf"] != "csrf-token" {
		t.Fatalf("session = %v", s)
	}
	if s["base"] != srv.URL+"/kanban" {
		t.Fatalf("base = %v", s["base"])
	}
	if exp, ok := s["exp"].(float64); !ok || exp != float64(frozenNow.Unix())+7*3600 {
		t.Fatalf("exp = %v", s["exp"])
	}
	if c.User() != "smoke" {
		t.Fatalf("user = %q", c.User())
	}
}

func TestCachedSessionIsReusedAndBaseMismatchForcesRelogin(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	if _, err := c.ListTasks(context.Background(), "not done"); err != nil {
		t.Fatal(err)
	}

	// A second client sharing the file reuses the session without logging in.
	c2 := New(c.cfg)
	c2.now = c.now
	if _, err := c2.ListBoards(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.logins != 1 {
		t.Fatalf("logins after reuse = %d", srv.logins)
	}

	// Rewrite the cache pinned to a different base: must re-login.
	raw, _ := os.ReadFile(c.cfg.SessionFile)
	var s session
	json.Unmarshal(raw, &s)
	s.Base = "http://other.example/kanban"
	rewritten, _ := json.Marshal(s)
	os.WriteFile(c.cfg.SessionFile, rewritten, 0o600)
	c3 := New(c.cfg)
	c3.now = c.now
	if _, err := c3.ListBoards(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.logins != 2 {
		t.Fatalf("logins after base mismatch = %d", srv.logins)
	}
}

func TestUnauthorizedTriggersExactlyOneReloginAndRetry(t *testing.T) {
	srv := newFakeServer(t)
	srv.failFirst = true
	c := testClient(t, srv)
	if _, err := c.ListBoards(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.logins != 2 { // initial login + re-login after the injected 401
		t.Fatalf("logins = %d", srv.logins)
	}
}

func TestCSRFSentOnKanbanWritesButNotTaskToggles(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	done := board.Done
	if _, err := c.PatchCard(context.Background(), "personal", "c1", CardPatch{Status: &done}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ToggleTask(context.Background(), "chores", 3, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true); err != nil {
		t.Fatal(err)
	}
	if got := srv.sawCSRF["/kanban/api/boards/personal/cards/c1"]; got != "csrf-token" {
		t.Fatalf("kanban write CSRF = %q", got)
	}
	if got := srv.sawCSRF["/api/tasks/toggle"]; got != "" {
		t.Fatalf("task toggle CSRF = %q, want empty", got)
	}
}

func TestPatchCardSendsOnlyChangedFields(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	desc := "- [x] done ✅ 2026-08-10"
	// The fake server 400s if "title" appears in the payload, so a pass here
	// proves omitempty kept unset fields off the wire.
	card, err := c.PatchCard(context.Background(), "personal", "c9", CardPatch{Description: &desc})
	if err != nil {
		t.Fatal(err)
	}
	if card.ID != "c9" {
		t.Fatalf("card = %+v", card)
	}
}

func TestAddCommentPostsWithCSRFAndReturnsCard(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	card, err := c.AddComment(context.Background(), "personal", "c3", "looks good")
	if err != nil {
		t.Fatal(err)
	}
	if card.ID != "c3" || len(card.Comments) != 1 || card.Comments[0].Body != "looks good" {
		t.Fatalf("card = %+v", card)
	}
	if got := srv.sawCSRF["/kanban/api/boards/personal/cards/c3/comments"]; got != "csrf-token" {
		t.Fatalf("comment CSRF = %q", got)
	}
}

func TestSetTaskRecurrencePostsExpectedTriple(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	task := tasks.Task{Slug: "chores", Line: 1, Text: "Shave", Status: tasks.StatusOpen,
		Recurrence: "every week on Wednesday, Saturday", Version: strings.Repeat("a", 64)}
	if err := c.SetTaskRecurrence(context.Background(), task, "every 4 days when done"); err != nil {
		t.Fatal(err)
	}
	if got := srv.sawCSRF["/api/tasks/recurrence"]; got != "" {
		t.Fatalf("recurrence CSRF = %q, want empty (notes-side write)", got)
	}
}

func TestCancelPurgeAndDeleteCard(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	task := tasks.Task{Slug: "chores", Line: 3, Text: "water the ferns", Status: tasks.StatusOpen, Version: strings.Repeat("a", 64)}
	if err := c.CancelTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	task.Status = tasks.StatusCancelled
	if err := c.PurgeTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCard(context.Background(), "personal", "c1"); err != nil {
		t.Fatal(err)
	}
	if got := srv.sawCSRF["/kanban/api/boards/personal/cards/c1"]; got != "csrf-token" {
		t.Fatalf("delete card CSRF = %q", got)
	}
}

func TestAddTaskDailyAndSlugDestinations(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	path, err := c.AddTask(context.Background(), "", true, "water the ferns")
	if err != nil || path != "Daily Notes/2026-08-11.md" {
		t.Fatalf("daily add: path=%q err=%v", path, err)
	}
	path, err = c.AddTask(context.Background(), "Inbox", false, "sweep")
	if err != nil || path != "Inbox.md" {
		t.Fatalf("slug add: path=%q err=%v", path, err)
	}
	if got := srv.sawCSRF["/api/tasks/add"]; got != "" {
		t.Fatalf("task add CSRF = %q, want empty (notes-side write)", got)
	}
}

func TestCreateCardPostsTitleAndStatus(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	card, err := c.CreateCard(context.Background(), "personal", "Try the hose", board.Ready)
	if err != nil {
		t.Fatal(err)
	}
	if card.ID != "new1" || card.Status != board.Ready {
		t.Fatalf("card = %+v", card)
	}
	if got := srv.sawCSRF["/kanban/api/boards/personal/cards"]; got != "csrf-token" {
		t.Fatalf("create card CSRF = %q", got)
	}
}

func TestMoveCardToBoardPostsConfirmedTransfer(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	if err := c.MoveCardToBoard(context.Background(), "personal", "c7", "work"); err != nil {
		t.Fatal(err)
	}
	if got := srv.sawCSRF["/kanban/api/boards/personal/cards/c7/board"]; got != "csrf-token" {
		t.Fatalf("transfer CSRF = %q", got)
	}
}

func TestToggleConflictSurfacesErrConflict(t *testing.T) {
	srv := newFakeServer(t)
	srv.toggleCode = http.StatusConflict
	c := testClient(t, srv)
	_, err := c.ToggleTask(context.Background(), "chores", 3, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Fatalf("err = %v", err)
	}
}

func TestCredentialsFallBackToEnvFile(t *testing.T) {
	srv := newFakeServer(t)
	t.Setenv("KANBAN_USERNAME", "")
	t.Setenv("KANBAN_PASSWORD", "")
	envFile := filepath.Join(t.TempDir(), "env")
	content := "# amythest secrets\nKANBAN_USERNAME=smoke\nKANBAN_PASSWORD='smoketest123'\nOTHER=x\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(Config{
		Endpoint:    srv.URL,
		Timeout:     5 * time.Second,
		SessionFile: filepath.Join(t.TempDir(), "session.json"),
		EnvFile:     envFile,
	})
	c.now = func() time.Time { return frozenNow }
	if _, err := c.ListBoards(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.logins != 1 {
		t.Fatalf("logins = %d", srv.logins)
	}
}

func TestContentIndexCachesAndComputesBacklinks(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	if _, err := c.Backlinks(context.Background(), "Garden"); err != nil {
		t.Fatal(err)
	}
	backlinks, err := c.Backlinks(context.Background(), "Garden")
	if err != nil {
		t.Fatal(err)
	}
	if len(backlinks) != 2 || backlinks[0] != "Chores-List" || backlinks[1] != "Home" {
		t.Fatalf("backlinks = %v", backlinks)
	}
	if srv.contentIndexHits != 1 {
		t.Fatalf("contentIndex fetched %d times, want 1 (cached)", srv.contentIndexHits)
	}
}

func TestListNotesAndBaseData(t *testing.T) {
	srv := newFakeServer(t)
	c := testClient(t, srv)
	notes, err := c.ListNotes(context.Background())
	if err != nil || len(notes) != 2 || notes[0].Slug != "Garden" || notes[0].MTime == 0 {
		t.Fatalf("notes = %+v err=%v", notes, err)
	}
	names, err := c.ListBases(context.Background())
	if err != nil || len(names) != 1 || names[0] != "Projects" {
		t.Fatalf("bases = %v err=%v", names, err)
	}
	data, err := c.GetBase(context.Background(), "Projects", 0)
	if err != nil {
		t.Fatal(err)
	}
	if data.Name != "Projects" || len(data.Data.Groups) != 1 || data.Data.Groups[0].Slugs[0] != "Garden" {
		t.Fatalf("base data = %+v", data)
	}
}
