// Package mcp exposes Amythest to AI agents over the Model Context
// Protocol (streamable HTTP): note search/read, the link graph, task
// queries, bases views, catalog SQL, and kanban actions — all in-process
// against the same stores the web UI uses.
package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/base698/amythest/internal/bases"
	"github.com/base698/amythest/internal/index"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

type Deps struct {
	DB      *index.DB
	Catalog *bases.Catalog
	Kanban  *board.Store // nil when kanban is disabled
	Vault   func() *vault.Vault
	BaseURL string
	// Rescan reindexes after a write so the new note is immediately
	// searchable and linked. It runs inside the tasks package's shared vault
	// lock and therefore must not try to acquire that lock itself.
	Rescan func() error
}

// Handler builds the /mcp HTTP handler, guarded by a bearer token.
// Returns nil (endpoint disabled) when AMYTHEST_MCP_TOKEN is unset.
func Handler(deps Deps) http.Handler {
	token := os.Getenv("AMYTHEST_MCP_TOKEN")
	if token == "" {
		return nil
	}
	inner := HandlerNoAuth(deps)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// HandlerNoAuth builds the MCP handler without the static-token gate, for
// servers that wrap it with their own auth layer (e.g. JWT mode).
func HandlerNoAuth(deps Deps) http.Handler {
	server := sdk.NewServer(&sdk.Implementation{Name: "amythest", Version: "0.1.0"}, nil)
	registerNoteTools(server, deps)
	registerTaskTools(server, deps)
	registerBaseTools(server, deps)
	if deps.Kanban != nil {
		registerKanbanTools(server, deps)
	}
	return sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
}

// ---- notes ----

type searchIn struct {
	Query           string `json:"query" jsonschema:"the search text; #tag terms restrict to tags"`
	Limit           int    `json:"limit,omitempty" jsonschema:"max results (default 10)"`
	IncludeArchived bool   `json:"include_archived,omitempty" jsonschema:"include archived notes and kanban done cards (default false)"`
}
type searchOut struct {
	Results []index.SearchResult `json:"results"`
}

type aggregateFrontmatterIn struct {
	Folder string   `json:"folder" jsonschema:"vault-relative folder prefix returned by search_notes"`
	Fields []string `json:"fields" jsonschema:"top-level numeric frontmatter fields to sum (max 20)"`
}

type numericAggregate struct {
	Sum   float64 `json:"sum"`
	Count int     `json:"count" jsonschema:"notes containing a numeric value for this field"`
}

type aggregateFrontmatterOut struct {
	Folder       string                      `json:"folder"`
	NotesScanned int                         `json:"notes_scanned"`
	Fields       map[string]numericAggregate `json:"fields"`
}

type getNoteIn struct {
	Slug string `json:"slug" jsonschema:"note slug, e.g. Food/Cooking; wikilink names also resolve"`
}
type getNoteOut struct {
	Slug        string          `json:"slug"`
	Path        string          `json:"path"`
	Title       string          `json:"title"`
	Frontmatter map[string]any  `json:"frontmatter,omitempty"`
	Markdown    string          `json:"markdown"`
	Backlinks   []index.NoteRef `json:"backlinks,omitempty"`
}

type listNotesIn struct {
	Folder string `json:"folder,omitempty" jsonschema:"restrict to a vault folder prefix"`
	Tag    string `json:"tag,omitempty" jsonschema:"restrict to a tag"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max results (default 100)"`
}
type listNotesOut struct {
	Notes []index.NoteRef `json:"notes"`
}

type createNoteIn struct {
	Path      string `json:"path" jsonschema:"vault-relative path ending in .md, e.g. _Inbox/Idea.md"`
	Content   string `json:"content" jsonschema:"full markdown content, frontmatter included if wanted"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"replace an existing note (default false)"`
}
type createNoteOut struct {
	Path string `json:"path"`
	Slug string `json:"slug"`
	URL  string `json:"url"`
}

type graphIn struct {
	Slug  string `json:"slug,omitempty" jsonschema:"center note; empty = whole graph"`
	Depth int    `json:"depth,omitempty" jsonschema:"BFS depth from slug (default 1)"`
}
type graphOut struct {
	Nodes []string            `json:"nodes"`
	Edges map[string][]string `json:"edges" jsonschema:"outgoing links per node"`
}

func registerNoteTools(server *sdk.Server, deps Deps) {
	sdk.AddTool(server, &sdk.Tool{Name: "search_notes",
		Description: "Full-text search over the notes vault (FTS5). Returns slugs, titles, and highlighted excerpts. Archived notes (frontmatter status or kanban done cards) are excluded unless include_archived is set."},
		func(ctx context.Context, req *sdk.CallToolRequest, in searchIn) (*sdk.CallToolResult, searchOut, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 10
			}
			res, err := deps.DB.Search(in.Query, limit, in.IncludeArchived)
			if err != nil {
				return nil, searchOut{}, err
			}
			for i := range res {
				res[i].Excerpt = strings.ReplaceAll(res[i].Excerpt, "\x02", "**")
				res[i].Excerpt = strings.ReplaceAll(res[i].Excerpt, "\x03", "**")
			}
			return nil, searchOut{Results: res}, nil
		})

	sdk.AddTool(server, &sdk.Tool{Name: "aggregate_frontmatter",
		Description: "Sum explicit numeric frontmatter fields across notes beneath one vault folder. Use search_notes first to locate the canonical folder and field names. Read-only and bounded to 20 fields."},
		func(ctx context.Context, req *sdk.CallToolRequest, in aggregateFrontmatterIn) (*sdk.CallToolResult, aggregateFrontmatterOut, error) {
			out, err := aggregateFrontmatter(deps.Vault(), in)
			return nil, out, err
		})

	sdk.AddTool(server, &sdk.Tool{Name: "get_note",
		Description: "Read a note: frontmatter, raw markdown body, and backlinks."},
		func(ctx context.Context, req *sdk.CallToolRequest, in getNoteIn) (*sdk.CallToolResult, getNoteOut, error) {
			v := deps.Vault()
			n, ok := v.BySlug(strings.Trim(in.Slug, "/"))
			if !ok {
				n, ok = v.Resolve(in.Slug)
			}
			if !ok {
				return nil, getNoteOut{}, fmt.Errorf("note not found: %s", in.Slug)
			}
			body, err := v.ReadBody(n)
			if err != nil {
				return nil, getNoteOut{}, err
			}
			backlinks, _ := deps.DB.Backlinks(n.Slug)
			return nil, getNoteOut{
				Slug: n.Slug, Path: n.Path, Title: n.Title,
				Frontmatter: n.FM.Meta, Markdown: string(body), Backlinks: backlinks,
			}, nil
		})

	sdk.AddTool(server, &sdk.Tool{Name: "list_notes",
		Description: "List notes, optionally filtered by folder prefix or tag."},
		func(ctx context.Context, req *sdk.CallToolRequest, in listNotesIn) (*sdk.CallToolResult, listNotesOut, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 100
			}
			var out []index.NoteRef
			if in.Tag != "" {
				refs, err := deps.DB.TagNotes(strings.TrimPrefix(in.Tag, "#"))
				if err != nil {
					return nil, listNotesOut{}, err
				}
				out = refs
			} else {
				v := deps.Vault()
				for _, n := range v.Notes {
					if in.Folder == "" || strings.HasPrefix(n.Path, strings.TrimSuffix(in.Folder, "/")+"/") {
						out = append(out, index.NoteRef{Slug: n.Slug, Title: n.Title})
					}
				}
			}
			if len(out) > limit {
				out = out[:limit]
			}
			return nil, listNotesOut{Notes: out}, nil
		})

	sdk.AddTool(server, &sdk.Tool{Name: "create_note",
		Description: "Create a markdown note in the vault. Path is vault-relative and must end in .md; kanban board files cannot be written this way."},
		func(ctx context.Context, req *sdk.CallToolRequest, in createNoteIn) (*sdk.CallToolResult, createNoteOut, error) {
			rel := strings.Trim(filepath.ToSlash(filepath.Clean(in.Path)), "/")
			switch {
			case rel == "" || rel == ".":
				return nil, createNoteOut{}, fmt.Errorf("path required")
			case strings.HasPrefix(rel, ".."), filepath.IsAbs(in.Path):
				return nil, createNoteOut{}, fmt.Errorf("path escapes the vault")
			case !strings.HasSuffix(rel, ".md"):
				return nil, createNoteOut{}, fmt.Errorf("path must end in .md")
			case strings.HasPrefix(rel, "kanban/"):
				return nil, createNoteOut{}, fmt.Errorf("kanban boards are managed through the kanban tools")
			case strings.HasPrefix(rel, "."):
				return nil, createNoteOut{}, fmt.Errorf("dotfiles are not notes")
			}
			v := deps.Vault()
			if err := tasks.WriteNoteFileAndReindex(v.Root, rel, []byte(in.Content), in.Overwrite, deps.Rescan); err != nil {
				return nil, createNoteOut{}, err
			}
			return nil, createNoteOut{Path: rel, Slug: vault.Slugify(rel), URL: deps.BaseURL + vault.Slugify(rel)}, nil
		})

	sdk.AddTool(server, &sdk.Tool{Name: "get_graph",
		Description: "Get the note link graph: all of it, or a BFS neighbourhood around one note."},
		func(ctx context.Context, req *sdk.CallToolRequest, in graphIn) (*sdk.CallToolResult, graphOut, error) {
			ci, err := deps.DB.ContentIndex()
			if err != nil {
				return nil, graphOut{}, err
			}
			keep := map[string]bool{}
			if in.Slug == "" {
				for s := range ci {
					keep[s] = true
				}
			} else {
				depth := in.Depth
				if depth <= 0 {
					depth = 1
				}
				undirected := map[string][]string{}
				for from, e := range ci {
					for _, to := range e.Links {
						undirected[from] = append(undirected[from], to)
						undirected[to] = append(undirected[to], from)
					}
				}
				frontier := []string{in.Slug}
				keep[in.Slug] = true
				for d := 0; d < depth; d++ {
					var next []string
					for _, s := range frontier {
						for _, nb := range undirected[s] {
							if !keep[nb] {
								keep[nb] = true
								next = append(next, nb)
							}
						}
					}
					frontier = next
				}
			}
			out := graphOut{Edges: map[string][]string{}}
			for s := range keep {
				if _, ok := ci[s]; !ok {
					continue
				}
				out.Nodes = append(out.Nodes, s)
				for _, to := range ci[s].Links {
					if keep[to] {
						out.Edges[s] = append(out.Edges[s], to)
					}
				}
			}
			return nil, out, nil
		})
}

// ---- tasks ----

type taskQueryIn struct {
	Query string `json:"query" jsonschema:"tasks query, newline or semicolon separated instructions, e.g. 'not done; due before tomorrow; sort by due'"`
}
type taskQueryOut struct {
	Groups []tasks.Group `json:"groups"`
}

func registerTaskTools(server *sdk.Server, deps Deps) {
	sdk.AddTool(server, &sdk.Tool{Name: "query_tasks",
		Description: "Query the vault-wide task index using the Obsidian Tasks-style language (filters: done/not done, due/scheduled before|after|on <date>, path includes, tags include, priority is; sort by; group by; limit)."},
		func(ctx context.Context, req *sdk.CallToolRequest, in taskQueryIn) (*sdk.CallToolResult, taskQueryOut, error) {
			q := tasks.ParseQuery(strings.ReplaceAll(in.Query, ";", "\n"))
			if q.Err() != nil {
				return nil, taskQueryOut{}, q.Err()
			}
			all, err := deps.DB.AllTasks()
			if err != nil {
				return nil, taskQueryOut{}, err
			}
			return nil, taskQueryOut{Groups: q.Run(all)}, nil
		})

	sdk.AddTool(server, &sdk.Tool{Name: "toggle_task",
		Description: "Complete or reopen a task in a vault note. Use the exact slug, line, and version that query_tasks returned. Completing a recurring task also inserts its next occurrence, unchecked with its dates advanced, matching the Obsidian Tasks plugin. Kanban cards are not vault tasks - use the kanban tools for those."},
		func(ctx context.Context, req *sdk.CallToolRequest, in toggleTaskIn) (*sdk.CallToolResult, toggleTaskOut, error) {
			out, err := toggleTask(deps, in)
			return nil, out, err
		})
}

// toggleTask contains the transport-independent toggle behavior so the MCP tool
// and its stale-write guarantees can be exercised without an SDK round trip.
func toggleTask(deps Deps, in toggleTaskIn) (toggleTaskOut, error) {
	v := deps.Vault()
	n, ok := v.BySlug(in.Slug)
	if !ok {
		return toggleTaskOut{}, fmt.Errorf("note %q not found", in.Slug)
	}
	if strings.HasPrefix(n.Path, "kanban/") {
		return toggleTaskOut{}, fmt.Errorf("kanban boards are managed through the kanban tools")
	}
	recurred, err := tasks.ToggleInFileAndReindex(v.Root, n.Path, in.Line, in.ExpectedVersion, in.Done, time.Now(), deps.Rescan)
	if err != nil {
		return toggleTaskOut{}, err
	}
	return toggleTaskOut{OK: true, Recurred: recurred, Path: n.Path}, nil
}

type toggleTaskIn struct {
	Slug            string `json:"slug" jsonschema:"slug of the note holding the task, exactly as query_tasks returned it"`
	Line            int    `json:"line" jsonschema:"1-based line number of the task, exactly as query_tasks returned it"`
	ExpectedVersion string `json:"expectedVersion" jsonschema:"version of the task source file, exactly as query_tasks returned it"`
	Done            bool   `json:"done" jsonschema:"true to complete the task, false to reopen it"`
}

type toggleTaskOut struct {
	OK       bool   `json:"ok"`
	Recurred bool   `json:"recurred" jsonschema:"a recurring task also created its next occurrence"`
	Path     string `json:"path"`
}

// ---- bases + catalog ----

type baseQueryIn struct {
	Name string `json:"name,omitempty" jsonschema:"name of a .base file in the vault (see /bases)"`
	YAML string `json:"yaml,omitempty" jsonschema:"inline base YAML (filters/formulas/views) instead of a file"`
	View int    `json:"view,omitempty" jsonschema:"view index (default 0)"`
}

type sqlIn struct {
	SQL     string `json:"sql" jsonschema:"a single SELECT statement against the catalog database"`
	MaxRows int    `json:"maxRows,omitempty"`
}
type sqlOut struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

func registerBaseTools(server *sdk.Server, deps Deps) {
	sdk.AddTool(server, &sdk.Tool{Name: "query_base",
		Description: "Evaluate an Obsidian-style base view over note frontmatter. Give either the name of a .base file in the vault or inline YAML."},
		func(ctx context.Context, req *sdk.CallToolRequest, in baseQueryIn) (*sdk.CallToolResult, *bases.ViewData, error) {
			var src []byte
			switch {
			case in.YAML != "":
				src = []byte(in.YAML)
			case in.Name != "":
				v := deps.Vault()
				rel, ok := v.Bases[in.Name]
				if !ok {
					return nil, nil, fmt.Errorf("no base named %q", in.Name)
				}
				abs, _ := v.AssetPath(rel)
				raw, err := os.ReadFile(abs)
				if err != nil {
					return nil, nil, err
				}
				src = raw
			default:
				return nil, nil, fmt.Errorf("give name or yaml")
			}
			b, err := bases.ParseBase(src)
			if err != nil {
				return nil, nil, err
			}
			rows, err := deps.DB.RowsForSource(b.Source)
			if err != nil {
				return nil, nil, err
			}
			data, err := b.Data(rows, in.View)
			if err != nil {
				return nil, nil, err
			}
			return nil, data, nil
		})

	sdk.AddTool(server, &sdk.Tool{Name: "query_sql",
		Description: "Run a read-only SELECT against the catalog database (CSV imports and other tabular data)."},
		func(ctx context.Context, req *sdk.CallToolRequest, in sqlIn) (*sdk.CallToolResult, sqlOut, error) {
			cols, rows, err := deps.Catalog.Query(in.SQL, in.MaxRows)
			if err != nil {
				return nil, sqlOut{}, err
			}
			return nil, sqlOut{Columns: cols, Rows: rows}, nil
		})
}

// ---- kanban ----

type boardIn struct {
	Board string `json:"board" jsonschema:"board name, e.g. operations"`
}
type cardIn struct {
	Board string `json:"board"`
	Card  string `json:"card" jsonschema:"card id (k_...)"`
}
type createBoardIn struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	SortOrder   int    `json:"sortOrder,omitempty"`
	Pinned      *bool  `json:"pinned,omitempty"`
}
type updateBoardSettingsIn struct {
	Board           string  `json:"board"`
	DisplayName     *string `json:"displayName,omitempty"`
	Description     *string `json:"description,omitempty"`
	Icon            *string `json:"icon,omitempty"`
	Color           *string `json:"color,omitempty"`
	SortOrder       *int    `json:"sortOrder,omitempty"`
	Pinned          *bool   `json:"pinned,omitempty"`
	Archived        *bool   `json:"archived,omitempty"`
	FocusCardID     *string `json:"focusCardId,omitempty"`
	DispatchEnabled *bool   `json:"dispatchEnabled,omitempty"`
}
type createCardIn struct {
	Board       string   `json:"board"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	DueDate     string   `json:"dueDate,omitempty" jsonschema:"optional due date in YYYY-MM-DD format"`
	Milestone   string   `json:"milestone,omitempty" jsonschema:"optional release milestone, separate from topic labels"`
	Priority    string   `json:"priority,omitempty" jsonschema:"p0|p1|p2|p3 (default p2)"`
	Status      string   `json:"status,omitempty" jsonschema:"triage|backlog|ready|in_progress|verify (default triage)"`
	Assignee    string   `json:"assignee,omitempty"`
	Agent       string   `json:"agent,omitempty"`
	Blocked     bool     `json:"blocked,omitempty"`
	Labels      []string `json:"labels,omitempty" jsonschema:"topic labels only; follow the deployment's documented controlled vocabulary; release numbers belong in milestone"`
}
type updateCardIn struct {
	Board       string    `json:"board"`
	Card        string    `json:"card"`
	Title       *string   `json:"title,omitempty"`
	Description *string   `json:"description,omitempty"`
	DueDate     *string   `json:"dueDate,omitempty" jsonschema:"due date in YYYY-MM-DD format; empty string clears it"`
	Milestone   *string   `json:"milestone,omitempty" jsonschema:"release milestone; empty string clears it"`
	Priority    *string   `json:"priority,omitempty" jsonschema:"p0|p1|p2|p3"`
	Status      *string   `json:"status,omitempty" jsonschema:"triage|backlog|ready|in_progress|verify|done (done archives)"`
	Assignee    *string   `json:"assignee,omitempty"`
	Agent       *string   `json:"agent,omitempty"`
	Blocked     *bool     `json:"blocked,omitempty"`
	Labels      *[]string `json:"labels,omitempty" jsonschema:"topic labels only; use the controlled vocabulary from kanban_create_card; release numbers belong in milestone"`
}
type moveCardIn struct {
	Board  string `json:"board"`
	Card   string `json:"card"`
	Status string `json:"status" jsonschema:"triage|backlog|ready|in_progress|verify|done (done archives)"`
	Before string `json:"before,omitempty" jsonschema:"card id to place this card before"`
}
type moveBetweenBoardsIn struct {
	Board            string `json:"board"`
	Card             string `json:"card"`
	DestinationBoard string `json:"destinationBoard"`
	Confirm          bool   `json:"confirm" jsonschema:"must be true after reviewing the exact source card"`
}
type deleteBoardIn struct {
	Board   string `json:"board"`
	Confirm bool   `json:"confirm" jsonschema:"must be true after verifying the board has no active or archived cards"`
}
type commentIn struct {
	Board string `json:"board"`
	Card  string `json:"card"`
	Body  string `json:"body"`
}
type archiveIn struct {
	Board string `json:"board"`
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type boardsOut struct {
	Boards []board.BoardSummary `json:"boards"`
}
type cardsOut struct {
	Cards []board.Card `json:"cards"`
}

const mcpActor = "mcp"

func registerKanbanTools(server *sdk.Server, deps Deps) {
	sdk.AddTool(server, &sdk.Tool{Name: "kanban_list_boards",
		Description: "List kanban boards with metadata, focus card IDs, and per-status card counts."},
		func(ctx context.Context, req *sdk.CallToolRequest, in struct{}) (*sdk.CallToolResult, boardsOut, error) {
			boards, err := deps.Kanban.ListBoards()
			if err != nil {
				return nil, boardsOut{}, err
			}
			return nil, boardsOut{Boards: boards}, nil
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_create_board",
		Description: "Create a board with dispatch disabled. The slug is immutable; displayName is editable."},
		func(ctx context.Context, req *sdk.CallToolRequest, in createBoardIn) (*sdk.CallToolResult, board.Board, error) {
			created, err := deps.Kanban.CreateBoardWithInput(board.BoardInput{
				Name: in.Name, DisplayName: in.DisplayName, Description: in.Description,
				Icon: in.Icon, Color: in.Color, SortOrder: in.SortOrder, Pinned: in.Pinned,
			})
			return nil, created, err
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_update_board_settings",
		Description: "Patch board display metadata, ordering, archive state, focus card, or dispatch setting. Omitted fields are preserved."},
		func(ctx context.Context, req *sdk.CallToolRequest, in updateBoardSettingsIn) (*sdk.CallToolResult, board.Board, error) {
			updated, err := deps.Kanban.PatchBoardSettings(in.Board, board.BoardSettingsPatch{
				DisplayName: in.DisplayName, Description: in.Description, Icon: in.Icon, Color: in.Color,
				SortOrder: in.SortOrder, Pinned: in.Pinned, Archived: in.Archived,
				FocusCardID: in.FocusCardID, DispatchEnabled: in.DispatchEnabled,
			})
			return nil, updated, err
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_delete_board",
		Description: "Permanently delete a board only when both active and archived card lists are empty. Requires explicit confirmation."},
		func(ctx context.Context, req *sdk.CallToolRequest, in deleteBoardIn) (*sdk.CallToolResult, struct{}, error) {
			if !in.Confirm {
				return nil, struct{}{}, errors.New("board deletion requires explicit confirmation")
			}
			return nil, struct{}{}, deps.Kanban.DeleteBoard(in.Board)
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_list_cards",
		Description: "List all active cards on a board."},
		func(ctx context.Context, req *sdk.CallToolRequest, in boardIn) (*sdk.CallToolResult, cardsOut, error) {
			b, err := deps.Kanban.Load(in.Board)
			if err != nil {
				return nil, cardsOut{}, err
			}
			return nil, cardsOut{Cards: b.Cards}, nil
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_create_card",
		Description: "Create a card on a board."},
		func(ctx context.Context, req *sdk.CallToolRequest, in createCardIn) (*sdk.CallToolResult, board.Card, error) {
			status := board.Status(in.Status)
			if in.Status == "" {
				status = board.Triage
			}
			card, err := deps.Kanban.CreateCard(in.Board, board.CardInput{
				Title: in.Title, Description: in.Description, DueDate: in.DueDate, Milestone: in.Milestone,
				Priority: board.Priority(in.Priority), Status: status, Assignee: in.Assignee,
				Agent: in.Agent, Blocked: in.Blocked, Labels: in.Labels,
			})
			return nil, card, err
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_update_card",
		Description: "Patch card fields (only the ones provided)."},
		func(ctx context.Context, req *sdk.CallToolRequest, in updateCardIn) (*sdk.CallToolResult, board.Card, error) {
			var status *board.Status
			if in.Status != nil {
				value := board.Status(*in.Status)
				status = &value
			}
			var priority *board.Priority
			if in.Priority != nil {
				value := board.Priority(*in.Priority)
				priority = &value
			}
			card, err := deps.Kanban.UpdateCard(in.Board, in.Card, board.CardPatch{
				Title: in.Title, Description: in.Description, DueDate: in.DueDate, Milestone: in.Milestone,
				Priority: priority, Status: status, Assignee: in.Assignee, Agent: in.Agent,
				Blocked: in.Blocked, Labels: in.Labels,
			})
			return nil, card, err
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_move_card",
		Description: "Move a card to a status column; moving to done archives it."},
		func(ctx context.Context, req *sdk.CallToolRequest, in moveCardIn) (*sdk.CallToolResult, board.Card, error) {
			card, err := deps.Kanban.MoveCard(in.Board, in.Card, board.Status(in.Status), in.Before)
			return nil, card, err
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_move_between_boards",
		Description: "Move an active card to another board while preserving its stable ID and all metadata. Requires confirm=true."},
		func(ctx context.Context, req *sdk.CallToolRequest, in moveBetweenBoardsIn) (*sdk.CallToolResult, board.Card, error) {
			if !in.Confirm {
				return nil, board.Card{}, fmt.Errorf("board move requires explicit confirmation")
			}
			card, err := deps.Kanban.MoveCardToBoard(in.Board, in.DestinationBoard, in.Card, mcpActor)
			return nil, card, err
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_move_archived_between_boards",
		Description: "Move a done card between board archives while preserving stable ID, done timestamp, metadata, comments, and attachments. Requires confirm=true."},
		func(ctx context.Context, req *sdk.CallToolRequest, in moveBetweenBoardsIn) (*sdk.CallToolResult, board.Card, error) {
			if !in.Confirm {
				return nil, board.Card{}, fmt.Errorf("archived board move requires explicit confirmation")
			}
			card, err := deps.Kanban.MoveArchivedCardToBoard(in.Board, in.DestinationBoard, in.Card, mcpActor)
			return nil, card, err
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_comment",
		Description: "Append a comment to a card."},
		func(ctx context.Context, req *sdk.CallToolRequest, in commentIn) (*sdk.CallToolResult, board.Card, error) {
			card, err := deps.Kanban.AddComment(in.Board, in.Card, mcpActor, in.Body)
			return nil, card, err
		})

	sdk.AddTool(server, &sdk.Tool{Name: "kanban_archive",
		Description: "List archived (done) cards on a board, optionally text-filtered."},
		func(ctx context.Context, req *sdk.CallToolRequest, in archiveIn) (*sdk.CallToolResult, cardsOut, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 30
			}
			cards, err := deps.Kanban.ListArchived(in.Board, in.Query, limit)
			if err != nil {
				return nil, cardsOut{}, err
			}
			return nil, cardsOut{Cards: cards}, nil
		})
}
