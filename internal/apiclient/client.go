package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
)

// APIError is a non-2xx response with the server's error message.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message) }

// ErrConflict matches 409 responses via errors.Is — the optimistic-locking
// signal from task writes ("task file changed; refresh and retry").
var ErrConflict = errors.New("conflict")

func (e *APIError) Is(target error) bool {
	return target == ErrConflict && e.Status == http.StatusConflict
}

type Client struct {
	cfg  Config
	http *http.Client
	now  func() time.Time
	user string

	mu      sync.Mutex
	session *session
}

func New(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
		now:  time.Now,
	}
}

// User is the logged-in username, known after the first authenticated call.
func (c *Client) User() string { return c.user }

func (c *Client) Endpoint() string { return c.cfg.Endpoint }

// ensureSession returns a valid session, minting one if the cache is empty,
// expired, or pinned to a different endpoint. The mutex keeps concurrent
// tea.Cmd goroutines from stampeding the login rate limit (5 per 5 min).
func (c *Client) ensureSession(ctx context.Context, force bool) (*session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force {
		if c.session != nil && c.session.Exp >= float64(c.now().Unix()+60) {
			return c.session, nil
		}
		if s := c.loadSession(); s != nil {
			c.session = s
			return s, nil
		}
	}
	s, err := c.login(ctx)
	if err != nil {
		return nil, err
	}
	c.session = s
	return s, nil
}

// do performs one authenticated request. path is relative to the endpoint
// (e.g. "/api/tasks" or "/kanban/api/boards"). Non-GET kanban requests carry
// the CSRF token; notes-side task writes need only the cookie. On 401/403 it
// re-logs-in once and retries, mirroring kanban.py.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	sess, err := c.ensureSession(ctx, false)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		var reader io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				return err
			}
			reader = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.cfg.Endpoint+path, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Cookie", sess.Cookie)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if method != http.MethodGet && strings.HasPrefix(path, "/kanban/") {
			req.Header.Set("X-CSRF-Token", sess.CSRF)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("cannot reach %s: %w", c.cfg.Endpoint, err)
		}
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && attempt == 0 {
			resp.Body.Close()
			if sess, err = c.ensureSession(ctx, true); err != nil {
				return err
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return decodeError(resp)
		}
		if out == nil {
			io.Copy(io.Discard, resp.Body)
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return fmt.Errorf("authentication failed")
}

func decodeError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(raw))
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Error != "" {
		message = payload.Error
	}
	return &APIError{Status: resp.StatusCode, Message: message}
}

// --------------------------------------------------------------------- tasks

type TaskGroup struct {
	Name  string       `json:"name"`
	Tasks []tasks.Task `json:"tasks"`
}

// ListTasks runs a tasks query (semicolon- or newline-separated instructions,
// e.g. "not done;sort by due") against the vault index.
func (c *Client) ListTasks(ctx context.Context, query string) ([]TaskGroup, error) {
	var groups []TaskGroup
	path := "/api/tasks?query=" + url.QueryEscape(query)
	if err := c.do(ctx, http.MethodGet, path, nil, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

// ToggleTask completes or reopens the vault task at (slug, line). version is
// the sha256 file hash from a prior ListTasks; a stale one yields ErrConflict.
// recurred=true means the server inserted a next occurrence and every line
// number in that file may have shifted.
func (c *Client) ToggleTask(ctx context.Context, slug string, line int, version string, done bool) (recurred bool, err error) {
	body := struct {
		Slug            string `json:"slug"`
		Line            int    `json:"line"`
		ExpectedVersion string `json:"expectedVersion"`
		Done            bool   `json:"done"`
	}{slug, line, version, done}
	var out struct {
		OK       bool `json:"ok"`
		Recurred bool `json:"recurred"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/tasks/toggle", body, &out); err != nil {
		return false, err
	}
	return out.Recurred, nil
}

// SetTaskDue sets, changes, or clears (due="") a vault task's 📅 due date.
// The expected* fields come from a prior ListTasks read; the server rejects
// the write if any of them changed underneath us.
func (c *Client) SetTaskDue(ctx context.Context, t tasks.Task, due string) error {
	body := struct {
		Slug            string `json:"slug"`
		Line            int    `json:"line"`
		ExpectedText    string `json:"expectedText"`
		ExpectedStatus  string `json:"expectedStatus"`
		ExpectedDue     string `json:"expectedDue"`
		ExpectedVersion string `json:"expectedVersion"`
		Due             string `json:"due"`
	}{t.Slug, t.Line, t.Text, t.Status, t.Due, t.Version, due}
	var out struct {
		OK  bool   `json:"ok"`
		Due string `json:"due"`
	}
	return c.do(ctx, http.MethodPost, "/api/tasks/due", body, &out)
}

// CancelTask soft-deletes a vault task: the line becomes "- [-] … ❌ date",
// still visible in the note and recoverable by editing it.
func (c *Client) CancelTask(ctx context.Context, t tasks.Task) error {
	body := struct {
		Slug            string `json:"slug"`
		ExpectedVersion string `json:"expectedVersion"`
		Items           []struct {
			Line         int    `json:"line"`
			ExpectedText string `json:"expectedText"`
		} `json:"items"`
	}{Slug: t.Slug, ExpectedVersion: t.Version}
	body.Items = append(body.Items, struct {
		Line         int    `json:"line"`
		ExpectedText string `json:"expectedText"`
	}{t.Line, t.Text})
	return c.do(ctx, http.MethodPost, "/api/tasks/cancel", body, nil)
}

// PurgeTask permanently removes an already-cancelled task line.
func (c *Client) PurgeTask(ctx context.Context, t tasks.Task) error {
	body := struct {
		Slug            string `json:"slug"`
		ExpectedVersion string `json:"expectedVersion"`
		Lines           []int  `json:"lines"`
	}{t.Slug, t.Version, []int{t.Line}}
	return c.do(ctx, http.MethodPost, "/api/tasks/purge", body, nil)
}

// AddTask appends a "- [ ] text" line to a note: today's daily note when
// daily is true (created if missing), otherwise the note named by slug.
// Returns the vault-relative path written to.
func (c *Client) AddTask(ctx context.Context, slug string, daily bool, text string) (string, error) {
	body := struct {
		Slug  string `json:"slug"`
		Daily bool   `json:"daily"`
		Text  string `json:"text"`
	}{slug, daily, text}
	var out struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/tasks/add", body, &out); err != nil {
		return "", err
	}
	return out.Path, nil
}

// --------------------------------------------------------------------- notes

type SearchResult struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Excerpt  string `json:"excerpt"` // server-escaped HTML snippet with <b> marks
	Archived bool   `json:"archived"`
}

// SearchNotes runs the server-side FTS query over the vault.
func (c *Client) SearchNotes(ctx context.Context, query string) ([]SearchResult, error) {
	var results []SearchResult
	path := "/api/search?q=" + url.QueryEscape(query)
	if err := c.do(ctx, http.MethodGet, path, nil, &results); err != nil {
		return nil, err
	}
	return results, nil
}

type Note struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Markdown string `json:"markdown"`
}

// GetNote fetches a note's raw markdown. ref may be a slug or a raw
// [[wikilink]] target — the server resolves both.
func (c *Client) GetNote(ctx context.Context, ref string) (*Note, error) {
	var note Note
	path := "/api/note?slug=" + url.QueryEscape(ref)
	if err := c.do(ctx, http.MethodGet, path, nil, &note); err != nil {
		return nil, err
	}
	return &note, nil
}

// -------------------------------------------------------------------- kanban

func (c *Client) ListBoards(ctx context.Context) ([]board.BoardSummary, error) {
	var boards []board.BoardSummary
	if err := c.do(ctx, http.MethodGet, "/kanban/api/boards", nil, &boards); err != nil {
		return nil, err
	}
	return boards, nil
}

// GetBoard returns a board with its active cards. Done cards live in the
// archive (ListArchive), not here.
func (c *Client) GetBoard(ctx context.Context, name string) (*board.Board, error) {
	var b board.Board
	if err := c.do(ctx, http.MethodGet, "/kanban/api/boards/"+url.PathEscape(name), nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *Client) ListArchive(ctx context.Context, name string, limit int) ([]board.Card, error) {
	var cards []board.Card
	path := fmt.Sprintf("/kanban/api/boards/%s/archive?limit=%d", url.PathEscape(name), limit)
	if err := c.do(ctx, http.MethodGet, path, nil, &cards); err != nil {
		return nil, err
	}
	return cards, nil
}

// CardPatch is board.CardPatch with omitempty so a partial update puts only
// the fields being changed on the wire, like the web UI does.
type CardPatch struct {
	Title       *string         `json:"title,omitempty"`
	Description *string         `json:"description,omitempty"`
	DueDate     *string         `json:"dueDate,omitempty"`
	Milestone   *string         `json:"milestone,omitempty"`
	Priority    *board.Priority `json:"priority,omitempty"`
	Status      *board.Status   `json:"status,omitempty"`
	Assignee    *string         `json:"assignee,omitempty"`
	Agent       *string         `json:"agent,omitempty"`
	Blocked     *bool           `json:"blocked,omitempty"`
	Labels      *[]string       `json:"labels,omitempty"`
}

// PatchCard partially updates a card. Setting status "done" archives it.
func (c *Client) PatchCard(ctx context.Context, boardName, cardID string, patch CardPatch) (*board.Card, error) {
	var card board.Card
	path := "/kanban/api/boards/" + url.PathEscape(boardName) + "/cards/" + url.PathEscape(cardID)
	if err := c.do(ctx, http.MethodPut, path, patch, &card); err != nil {
		return nil, err
	}
	return &card, nil
}

// CreateCard adds a card with the given title to a board column.
func (c *Client) CreateCard(ctx context.Context, boardName, title string, status board.Status) (*board.Card, error) {
	body := struct {
		Title  string       `json:"title"`
		Status board.Status `json:"status"`
	}{title, status}
	var card board.Card
	path := "/kanban/api/boards/" + url.PathEscape(boardName) + "/cards"
	if err := c.do(ctx, http.MethodPost, path, body, &card); err != nil {
		return nil, err
	}
	return &card, nil
}

// AddComment appends a comment to a card and returns the updated card.
func (c *Client) AddComment(ctx context.Context, boardName, cardID, bodyText string) (*board.Card, error) {
	body := struct {
		Body string `json:"body"`
	}{bodyText}
	var card board.Card
	path := "/kanban/api/boards/" + url.PathEscape(boardName) + "/cards/" + url.PathEscape(cardID) + "/comments"
	if err := c.do(ctx, http.MethodPost, path, body, &card); err != nil {
		return nil, err
	}
	return &card, nil
}

// DeleteCard permanently deletes a card (active or archived) — comments and
// attachments included. There is no server-side undo.
func (c *Client) DeleteCard(ctx context.Context, boardName, cardID string) error {
	path := "/kanban/api/boards/" + url.PathEscape(boardName) + "/cards/" + url.PathEscape(cardID)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// RestoreCard moves an archived (done) card back onto the board in the given
// column.
func (c *Client) RestoreCard(ctx context.Context, boardName, cardID string, status board.Status) error {
	body := struct {
		Status board.Status `json:"status"`
	}{status}
	path := "/kanban/api/boards/" + url.PathEscape(boardName) + "/archive/" + url.PathEscape(cardID) + "/restore"
	return c.do(ctx, http.MethodPost, path, body, nil)
}

// MoveCardToBoard transfers a card to another board.
func (c *Client) MoveCardToBoard(ctx context.Context, boardName, cardID, destination string) error {
	body := struct {
		DestinationBoard string `json:"destinationBoard"`
		Confirm          bool   `json:"confirm"`
	}{destination, true}
	path := "/kanban/api/boards/" + url.PathEscape(boardName) + "/cards/" + url.PathEscape(cardID) + "/board"
	return c.do(ctx, http.MethodPost, path, body, nil)
}

// MoveCard moves a card to a column, before beforeID or appended when empty.
func (c *Client) MoveCard(ctx context.Context, boardName, cardID string, status board.Status, beforeID string) error {
	body := struct {
		Status   board.Status `json:"status"`
		BeforeID string       `json:"beforeId"`
	}{status, beforeID}
	path := "/kanban/api/boards/" + url.PathEscape(boardName) + "/cards/" + url.PathEscape(cardID) + "/move"
	return c.do(ctx, http.MethodPost, path, body, nil)
}
