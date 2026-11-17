package find

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
)

func overview() *apiclient.WorkSnapshot {
	return &apiclient.WorkSnapshot{
		Runs: []apiclient.WorkRun{{ID: "r-open", Session: "s-open"}, {ID: "r-gone", Session: "s-gone"}, {ID: "r-pi", Session: "s-pi"}, {ID: "r-tui", Session: "s-tui"}},
		Slots: []apiclient.WorkSlot{
			{Collector: "mac", Pane: "t7", Session: "s-open", Status: "idle", Tab: "gait"},
			{Collector: "mac", Pane: "t8", Session: "s-gone", Status: "gone", Tab: "old"},
			{Collector: "pi", Pane: "t7", Session: "s-pi", Status: "working", Tab: "cron"}, // same pane id, another machine
			{Collector: "mac", Pane: "w2:p4", Session: "s-tui", Status: "done", Tab: "sim"},
		},
		Sessions: []apiclient.WorkSession{{ID: "s-old", Title: "long ago"}},
	}
}

func TestPlacesOnlyOfferPanesOpenOnThisMachine(t *testing.T) {
	p := NewPlaces(overview(), "mac")
	if pane, tab, ok := p.Pane("r-open"); !ok || pane != "t7" || tab != "gait" {
		t.Errorf("open pane: %q %q %v", pane, tab, ok)
	}
	if _, _, ok := p.Pane("r-gone"); ok {
		t.Error("a closed pane must not be offered")
	}
	if pane, tab, ok := p.Pane("r-pi"); ok || pane != "" || tab != "cron" {
		t.Errorf("another machine's pane: %q %q %v", pane, tab, ok)
	}
	if pane, _, ok := p.Pane("r-tui"); !ok || pane != "w2:p4" || hostOf(pane) != "herdr" || hostOf("t7") != "app" {
		t.Errorf("terminal herdr pane: %q %v", pane, ok)
	}
	if _, _, ok := p.Pane("r-unknown"); ok || p.Known("r-unknown") || !p.Known("r-open") {
		t.Error("unknown run")
	}
	// Without knowing which collector is this machine's, no pane is trusted.
	if _, _, ok := NewPlaces(overview(), "").Pane("r-open"); ok {
		t.Error("an unidentified machine must not claim panes")
	}
}

func TestResultsFromServerHits(t *testing.T) {
	p := NewPlaces(overview(), "mac")
	work := fromWork([]apiclient.WorkHit{
		{Kind: "chat", Title: "assistant", Body: "the  gait\ntiming is fixed", Run: "r-open", Project: "robot", Created: "2026-11-17T10:00:00.000000000Z"},
		{Kind: "commit", Title: "Fix gait", Body: "0123456789abcdef", Run: "r-gone", Project: "robot"},
		{Kind: "image", Title: "", Body: "/w/walk.png", Run: "r-pi", Project: "Hermes"},
	}, p, "https://h/notes/")
	if work[0].Pane != "t7" || work[0].Where != "robot / gait" || work[0].Snippet != "the gait timing is fixed" || work[0].URL != "https://h/notes/kanban/focus" {
		t.Errorf("chat: %+v", work[0])
	}
	if work[1].Pane != "" || work[1].Where != "robot / old" || work[1].Snippet != "" {
		t.Errorf("commit from a closed pane: %+v", work[1])
	}
	if work[2].Pane != "" || work[2].Title != "/w/walk.png" || work[2].Where != "Hermes / cron" {
		t.Errorf("untitled artifact on another machine: %+v", work[2])
	}

	vault := fromVault([]apiclient.VaultHit{
		{Slug: "Projects/Robot", Title: "Robot", Excerpt: "the <b>gait</b> &amp; timing", Kind: "note", Date: "2026-11-01", URL: "Projects/Robot"},
		{Slug: "kanban/robot/board", Title: "Tune gait", Kind: "card", URL: "kanban/robot#card-k1"},
		{Slug: "Daily/2026-11-17", Title: "check gait", Kind: "task", URL: "https://evil.example/x", Archived: true},
	}, "https://h/notes")
	if vault[0].Kind != "task" || vault[1].Kind != "card" || vault[2].Kind != "note" {
		t.Fatalf("tasks and cards come before notes: %v %v %v", vault[0].Kind, vault[1].Kind, vault[2].Kind)
	}
	if vault[2].Snippet != "the gait & timing" || vault[2].URL != "https://h/notes/Projects/Robot" {
		t.Errorf("note: %+v", vault[2])
	}
	if vault[1].URL != "https://h/notes/kanban/robot#card-k1" {
		t.Errorf("card link: %s", vault[1].URL)
	}
	// A link that leaves the notes mount is replaced by the note's own page.
	if vault[0].URL != "https://h/notes/Daily/2026-11-17" || !strings.Contains(vault[0].Where, "archived") {
		t.Errorf("task: %+v", vault[0])
	}
}

func TestSearchScreensMatchesEveryWordNewestFirst(t *testing.T) {
	screens := []Screen{
		{Pane: "t1", Where: "robot / gait", Lines: []string{"build ok", "error: gait  timing off", "retrying", "Error in gait TIMING again", ""}},
		{Pane: "t2", Where: "notes / 1", Lines: []string{"nothing here", "timing only"}},
	}
	got := searchScreens(screens, "gait timing", 2, 20)
	if len(got) != 2 || got[0].Title != "Error in gait TIMING again" || got[1].Title != "error: gait timing off" || got[0].Pane != "t1" || got[0].Where != "robot / gait" {
		t.Fatalf("matches: %+v", got)
	}
	if n := len(searchScreens(screens, "gait timing", 1, 20)); n != 1 {
		t.Errorf("per-terminal cap: %d", n)
	}
	if searchScreens(screens, "  ", 2, 20) != nil {
		t.Error("an empty query matches nothing")
	}
}

func TestReadScreensSkipsWhatIsNotShownHere(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch strings.Join(append([]string{name}, args...), " ") {
		case "hc ls --json":
			return []byte(`{"terminals":[{"address":"t1","where":"robot / gait","state":"live"},
				{"address":"t2","where":"","state":"live"},{"address":"vps/t3","where":"remote / 1","state":"live"},
				{"address":"t4","where":"robot / old","state":"finished"}]}`), nil
		case "hc read t1 --lines 300":
			return []byte("one\ntwo\n"), nil
		}
		return nil, errors.New("unexpected: " + name + " " + strings.Join(args, " "))
	}
	got := ReadScreens(run, 300)
	if len(got) != 1 || got[0].Pane != "t1" || got[0].Where != "robot / gait" || got[0].Lines[1] != "two" {
		t.Fatalf("screens: %+v", got)
	}
}

func TestFocusUsesTheRightHerdr(t *testing.T) {
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		return nil, nil
	}
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("HERDR_ENV", "")
	Focus("t7", run)
	Focus("w2:p4", run)
	if calls[0] != "herdr-app do pane.focus --terminal t7" || calls[1] != "herdr agent focus w2:p4" {
		t.Fatalf("calls: %v", calls)
	}
	// From a popup inside terminal herdr, the focus is asked for after the popup has closed.
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_BIN_PATH", "/opt/herdr")
	Focus("w2:p4", run)
	if !strings.HasPrefix(calls[2], "sh -c (sleep 0.4;") || !strings.HasSuffix(calls[2], "/opt/herdr w2:p4") {
		t.Fatalf("deferred focus: %q", calls[2])
	}
}

type fakeAPI struct {
	work   []apiclient.WorkHit
	vault  []apiclient.VaultHit
	runs   map[string]*apiclient.WorkSnapshot
	asked  []string
	looked []string
}

func (f *fakeAPI) WorkOverview(context.Context) (*apiclient.WorkSnapshot, error) {
	return overview(), nil
}
func (f *fakeAPI) WorkRun(_ context.Context, run string) (*apiclient.WorkSnapshot, error) {
	f.looked = append(f.looked, run)
	if s, ok := f.runs[run]; ok {
		return s, nil
	}
	return nil, errors.New("no such run")
}
func (f *fakeAPI) WorkSearch(_ context.Context, q string, _ int) ([]apiclient.WorkHit, error) {
	f.asked = append(f.asked, q)
	return f.work, nil
}
func (f *fakeAPI) VaultSearch(context.Context, string) ([]apiclient.VaultHit, error) {
	return f.vault, nil
}
func (f *fakeAPI) Endpoint() string { return "https://h/notes" }

// settle runs a message and everything it leads to, including the typing
// pause, the way the program loop would.
func settleAll(t *testing.T, m tea.Model, msgs ...tea.Msg) {
	t.Helper()
	queue := msgs
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 300 {
			t.Fatal("messages never settled")
		}
		_, cmd := m.Update(queue[0])
		queue = append(queue[1:], collect(cmd)...)
	}
}

func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(60 * time.Millisecond):
		return nil // the cursor blink
	}
	switch v := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range v {
			out = append(out, collect(c)...)
		}
		return out
	case tea.QuitMsg:
		return nil
	}
	return []tea.Msg{msg}
}

func open(t *testing.T, api *fakeAPI, o Options) *model {
	t.Helper()
	settle = 5 * time.Millisecond
	o.Now = func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) }
	o.Local = "mac"
	m := New(api, o).(*model)
	settleAll(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	settleAll(t, m, collect(m.Init())...)
	return m
}

func typeText(t *testing.T, m *model, s string) {
	t.Helper()
	for _, r := range s {
		settleAll(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestSearchMergesSourcesAndGoesToTheOpenPane(t *testing.T) {
	api := &fakeAPI{
		work: []apiclient.WorkHit{
			{Kind: "commit", Title: "Fix gait", Run: "r-gone", Project: "robot"},
			{Kind: "chat", Title: "assistant", Body: "gait timing fixed", Run: "r-open", Project: "robot"},
		},
		vault: []apiclient.VaultHit{{Slug: "Projects/Robot", Title: "Robot notes", Kind: "note", URL: "Projects/Robot"}},
	}
	screens := []Screen{{Pane: "t3", Where: "robot / sim", Lines: []string{"gait check passed"}}}
	m := open(t, api, Options{Screens: func() []Screen { return screens }})
	typeText(t, m, "gait")
	if len(api.asked) == 0 || api.asked[len(api.asked)-1] != "gait" {
		t.Fatalf("server asked: %v", api.asked)
	}
	if len(m.rows) != 4 {
		t.Fatalf("rows: %d %+v", len(m.rows), m.rows)
	}
	// The result whose pane is open here is listed first, and selected.
	if m.rows[0].Kind != "chat" || m.rows[0].Pane != "t7" || m.cursor != 0 {
		t.Fatalf("first row: %+v cursor %d", m.rows[0], m.cursor)
	}
	view := m.View()
	for _, want := range []string{"Agents and what they made", "Notes, cards and tasks", "On screen in open terminals", "● open pane", "opens in the browser", "robot / gait", "gait check passed", "4 results"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q\n%s", want, view)
		}
	}
	settleAll(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if c := Chosen(m); c == nil || c.Pane != "t7" || c.URL != "" {
		t.Fatalf("choice: %+v", c)
	}
}

func TestSearchOpensTheBrowserWhenThePaneIsGone(t *testing.T) {
	api := &fakeAPI{work: []apiclient.WorkHit{{Kind: "commit", Title: "Fix gait", Run: "r-gone", Project: "robot"}},
		vault: []apiclient.VaultHit{{Slug: "Projects/Robot", Title: "Robot notes", Kind: "note", URL: "Projects/Robot"}}}
	m := open(t, api, Options{Query: "gait"})
	if len(m.rows) != 2 {
		t.Fatalf("a starting query searches at once: %d rows", len(m.rows))
	}
	settleAll(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if c := Chosen(m); c == nil || c.Pane != "" || c.URL != "https://h/notes/kanban/focus" {
		t.Fatalf("closed pane: %+v", c)
	}
	// The note opens at its own page.
	m = open(t, api, Options{Query: "gait"})
	settleAll(t, m, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter})
	if c := Chosen(m); c == nil || c.URL != "https://h/notes/Projects/Robot" {
		t.Fatalf("note: %+v", c)
	}
}

func TestSearchLooksUpAnOlderRunBeforeDeciding(t *testing.T) {
	// The run is older than the overview covers; its session turns out to be
	// in a pane that is still open here.
	api := &fakeAPI{
		work: []apiclient.WorkHit{{Kind: "chat", Title: "assistant", Body: "old gait talk", Run: "r-old", Project: "robot"}},
		runs: map[string]*apiclient.WorkSnapshot{"r-old": {Runs: []apiclient.WorkRun{{ID: "r-old", Session: "s-open"}}}},
	}
	m := open(t, api, Options{Query: "gait"})
	if m.rows[0].Pane != "" {
		t.Fatalf("not located yet: %+v", m.rows[0])
	}
	settleAll(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(api.looked) != 1 || api.looked[0] != "r-old" {
		t.Fatalf("looked up: %v", api.looked)
	}
	if c := Chosen(m); c == nil || c.Pane != "t7" {
		t.Fatalf("after lookup: %+v", c)
	}
	// A run that cannot be found falls back to the browser, once.
	api = &fakeAPI{work: []apiclient.WorkHit{{Kind: "chat", Title: "assistant", Body: "x gait", Run: "r-lost", Project: "robot"}}}
	m = open(t, api, Options{Query: "gait"})
	settleAll(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if c := Chosen(m); c == nil || c.URL == "" || len(api.looked) != 1 {
		t.Fatalf("lost run: %+v looked %v", c, api.looked)
	}
}

func TestSearchKeepsTheSelectionTheUserMade(t *testing.T) {
	api := &fakeAPI{vault: []apiclient.VaultHit{{Slug: "a", Title: "gait note", Kind: "note", URL: "a"}}}
	screens := []Screen{{Pane: "t3", Where: "robot / sim", Lines: []string{"gait one", "gait two"}}}
	m := open(t, api, Options{Screens: func() []Screen { return screens }})
	typeText(t, m, "gait")
	// note, then two screen lines; select the last screen line.
	settleAll(t, m, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyDown})
	picked := m.rows[m.cursor].Title
	// Server results arriving later push it down the list; it stays selected.
	m.agents = fromWork([]apiclient.WorkHit{{Kind: "commit", Title: "late gait commit", Run: "r-gone"}}, m.places, "https://h/notes")
	m.arrange()
	if m.rows[m.cursor].Title != picked || m.cursor != 3 {
		t.Fatalf("selection moved to %q at %d", m.rows[m.cursor].Title, m.cursor)
	}
	// Typing again starts from the top.
	typeText(t, m, "s")
	if m.cursor != 0 || m.moved {
		t.Fatalf("after typing: cursor %d moved %v", m.cursor, m.moved)
	}
	// One character is not searched on the server.
	n := len(api.asked)
	settleAll(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	typeText(t, m, "g")
	if len(api.asked) != n || len(m.agents) != 0 {
		t.Fatalf("single character searched the server: %v", api.asked[n:])
	}
}
