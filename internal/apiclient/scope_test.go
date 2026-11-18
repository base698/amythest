package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
)

func TestWorkspaceMapping(t *testing.T) {
	cfg := Config{Workspaces: map[string]Scope{"Garden": {Boards: []string{"plants"}, Folders: []string{"Projects/Garden"}, Tags: []string{"garden"}, Project: "Botany"}}}
	s := cfg.Workspace("garden")
	if s.Name != "garden" || s.Project != "Botany" || !s.board(board.BoardSummary{Name: "plants"}) || s.board(board.BoardSummary{DisplayName: "Garden"}) {
		t.Fatalf("explicit mapping: %+v", s)
	}
	for _, tc := range []struct {
		path string
		tags []string
		want bool
	}{
		{"Projects/Garden/Plan.md", nil, true},
		{"Projects/Gardening/Plan.md", nil, false},
		{"Daily/Today.md", []string{"#GARDEN"}, true},
		{"Daily/Today.md", []string{"gardening"}, false},
	} {
		if got := s.note(tc.path, tc.tags); got != tc.want {
			t.Errorf("note(%q) = %v", tc.path, got)
		}
	}
	defaultScope := cfg.Workspace("Garden Tools")
	if defaultScope.Project != "Garden Tools" || !reflect.DeepEqual(defaultScope.Tags, []string{"garden-tools"}) || !defaultScope.board(board.BoardSummary{Workspace: "garden tools"}) {
		t.Fatalf("default mapping: %+v", defaultScope)
	}
}

func TestScopedClientKeepsListsAndSearchInWorkspace(t *testing.T) {
	var searchScopes [][]string
	mux := http.NewServeMux()
	encode := func(w http.ResponseWriter, value any) {
		t.Helper()
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}
	mux.HandleFunc("/kanban/api/boards", func(w http.ResponseWriter, _ *http.Request) {
		encode(w, []board.BoardSummary{{Name: "plants", DisplayName: "Garden"}, {Name: "other"}})
	})
	mux.HandleFunc("/api/notes", func(w http.ResponseWriter, _ *http.Request) {
		encode(w, []NoteEntry{{Slug: "Garden/Plan", Path: "Garden/Plan.md"}, {Slug: "Daily/Today", Path: "Daily/Today.md"}, {Slug: "kanban/plants/board", Path: "kanban/plants/board.md"}, {Slug: "Other/Plan", Path: "Other/Plan.md"}})
	})
	mux.HandleFunc("/api/contentIndex", func(w http.ResponseWriter, _ *http.Request) {
		encode(w, map[string]ContentEntry{"Daily/Today": {Tags: []string{"garden"}}})
	})
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "limit") {
			t.Error("limit reached server before scope")
		}
		encode(w, []TaskGroup{{Name: "open", Tasks: []tasks.Task{
			{Text: "folder", Path: "Garden/Plan.md"}, {Text: "tag", Path: "Daily/Today.md", Tags: []string{"garden"}},
			{Text: "board", Path: "kanban/plants/board.md"}, {Text: "unrelated", Path: "Daily/Today.md"},
		}}})
	})
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		var slugs []string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("scope_slugs")), &slugs); err != nil {
			t.Error(err)
		}
		searchScopes = append(searchScopes, slugs)
		// Defensive filtering must also reject a stale server's global result.
		encode(w, []VaultHit{{Slug: "Garden/Plan", Title: "kept"}, {Slug: "Other/Plan", Title: "excluded"}})
	})
	mux.HandleFunc("/api/work/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") != "Garden" {
			t.Error("missing project filter")
		}
		encode(w, []WorkHit{{Project: "Garden"}, {Project: "Other"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(Config{Endpoint: srv.URL, Timeout: time.Second})
	c.session = &session{Exp: float64(time.Now().Add(time.Hour).Unix()), Cookie: "test=1"}
	scoped := c.Scoped(Config{}.Workspace("Garden"))
	scoped.session = c.session
	ctx := context.Background()
	boards, err := scoped.ListBoards(ctx)
	if err != nil || len(boards) != 1 || boards[0].Name != "plants" {
		t.Fatalf("boards: %+v %v", boards, err)
	}
	groups, err := scoped.ListTasks(ctx, "not done")
	if err != nil || len(groups) != 1 || len(groups[0].Tasks) != 3 {
		t.Fatalf("tasks: %+v %v", groups, err)
	}
	limited, err := scoped.ListTasks(ctx, "limit 2;sort by description")
	if err != nil || len(limited) != 1 || len(limited[0].Tasks) != 2 || limited[0].Tasks[0].Text != "board" {
		t.Fatalf("scoped limit: %+v %v", limited, err)
	}
	notes, err := scoped.ListNotes(ctx)
	if err != nil || len(notes) != 3 {
		t.Fatalf("notes: %+v %v", notes, err)
	}
	hits, err := scoped.SearchNotes(ctx, "seed")
	if err != nil || len(hits) != 1 || hits[0].Title != "kept" {
		t.Fatalf("search: %+v %v", hits, err)
	}
	vaultHits, err := scoped.VaultSearch(ctx, "seed")
	if err != nil || len(vaultHits) != 1 {
		t.Fatalf("vault: %+v %v", vaultHits, err)
	}
	for _, slugs := range searchScopes {
		if !reflect.DeepEqual(slugs, []string{"Daily/Today", "Garden/Plan", "kanban/plants/board"}) {
			t.Errorf("search scope: %v", slugs)
		}
	}
	work, err := scoped.WorkSearch(ctx, "seed", 30)
	if err != nil || len(work) != 1 {
		t.Fatalf("work: %+v %v", work, err)
	}
	all, err := c.ListNotes(ctx)
	if err != nil || len(all) != 4 {
		t.Fatalf("original client changed: %+v %v", all, err)
	}
	empty := c.Scoped(Config{}.Workspace("Unknown"))
	empty.session = c.session
	unknown, err := empty.ListNotes(ctx)
	if err != nil || len(unknown) != 0 {
		t.Fatalf("unknown scope: %+v %v", unknown, err)
	}
}
