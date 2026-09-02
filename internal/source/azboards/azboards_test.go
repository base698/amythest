package azboards

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeAZ writes an executable script standing in for the az CLI (via AMY_AZ)
// that appends each invocation to a log file and prints the given stdout.
// Exit code 1 plus stderr simulates failures.
func fakeAZ(t *testing.T, stdout, stderr string, exitCode int) (azPath, logPath string) {
	t.Helper()
	dir := t.TempDir()
	azPath = filepath.Join(dir, "az")
	logPath = filepath.Join(dir, "calls.log")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %q\ncat <<'STDOUT'\n%s\nSTDOUT\n", logPath, stdout)
	if stderr != "" {
		script += fmt.Sprintf("cat >&2 <<'STDERR'\n%s\nSTDERR\n", stderr)
	}
	script += fmt.Sprintf("exit %d\n", exitCode)
	if err := os.WriteFile(azPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMY_AZ", azPath)
	return azPath, logPath
}

func callCount(t *testing.T, logPath string) int {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Split(strings.TrimSpace(string(raw)), "\n"))
}

func testConfig() Config {
	return Config{Org: "https://dev.azure.com/demo", Project: "My Project",
		Boards: []BoardConfig{{Name: "team", Area: `My Project\Team`, Type: "User Story"}}}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cli.yaml")
	yaml := `
endpoint: http://localhost:8787
sources:
  azboards:
    org: https://dev.azure.com/demo
    project: My Project
    boards:
      - name: team
        area: My Project\Team
        type: User Story
        columns: [New, Active, Closed]
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, ok, err := LoadConfig(path)
	if err != nil || !ok {
		t.Fatalf("LoadConfig: ok=%v err=%v", ok, err)
	}
	if cfg.Org != "https://dev.azure.com/demo" || cfg.Project != "My Project" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(cfg.Boards) != 1 || cfg.Boards[0].Area != `My Project\Team` ||
		!reflect.DeepEqual(cfg.Boards[0].Columns, []string{"New", "Active", "Closed"}) {
		t.Fatalf("boards = %+v", cfg.Boards)
	}

	if _, ok, err := LoadConfig(filepath.Join(dir, "missing.yaml")); ok || err != nil {
		t.Fatalf("missing file: ok=%v err=%v", ok, err)
	}
	noSources := filepath.Join(dir, "plain.yaml")
	os.WriteFile(noSources, []byte("endpoint: http://x\n"), 0o644)
	if _, ok, err := LoadConfig(noSources); ok || err != nil {
		t.Fatalf("no sources: ok=%v err=%v", ok, err)
	}
	incomplete := filepath.Join(dir, "incomplete.yaml")
	os.WriteFile(incomplete, []byte("sources:\n  azboards:\n    org: https://dev.azure.com/demo\n"), 0o644)
	if _, _, err := LoadConfig(incomplete); err == nil {
		t.Fatal("missing project should error")
	}
}

func TestBoardItemsParsesAndCaches(t *testing.T) {
	_, logPath := fakeAZ(t, `[
  {"id": 101, "title": "First story", "state": "Active", "assigned": "Ada Lovelace"},
  {"id": 102, "title": "Second story", "state": "New", "assigned": null}
]`, "", 0)
	src := New(testConfig())
	items, err := src.BoardItems(context.Background(), src.cfg.Boards[0], false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != 101 || items[0].Assignee != "Ada Lovelace" || items[1].State != "New" {
		t.Fatalf("items = %+v", items)
	}
	if _, err := src.BoardItems(context.Background(), src.cfg.Boards[0], false); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, logPath); n != 1 {
		t.Fatalf("cached call still hit az: %d calls", n)
	}
	if _, err := src.BoardItems(context.Background(), src.cfg.Boards[0], true); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, logPath); n != 2 {
		t.Fatalf("force refresh should hit az: %d calls", n)
	}
}

func TestBoardItemsCacheExpires(t *testing.T) {
	_, logPath := fakeAZ(t, `[]`, "", 0)
	src := New(testConfig())
	now := time.Now()
	src.now = func() time.Time { return now }
	if _, err := src.BoardItems(context.Background(), src.cfg.Boards[0], false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(listTTL + time.Second)
	if _, err := src.BoardItems(context.Background(), src.cfg.Boards[0], false); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, logPath); n != 2 {
		t.Fatalf("expired cache should re-query: %d calls", n)
	}
}

func TestNotLoggedInDetection(t *testing.T) {
	fakeAZ(t, "", "ERROR: TF400813: The user is not authorized to access this resource.", 1)
	src := New(testConfig())
	_, err := src.BoardItems(context.Background(), src.cfg.Boards[0], false)
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("err = %v", err)
	}
	health := src.Health(context.Background())
	if health.State != "missing-credentials" || !strings.Contains(health.Detail, "az devops login --organization https://dev.azure.com/demo") {
		t.Fatalf("health = %+v", health)
	}
}

func TestSetStateInvalidatesCaches(t *testing.T) {
	_, logPath := fakeAZ(t, `[{"id": 101, "title": "T", "state": "New", "assigned": null}]`, "", 0)
	src := New(testConfig())
	if _, err := src.BoardItems(context.Background(), src.cfg.Boards[0], false); err != nil {
		t.Fatal(err)
	}
	// SetState discards stdout, so the list-shaped fake output is fine.
	if err := src.SetState(context.Background(), 101, "Active"); err != nil {
		t.Fatal(err)
	}
	before := callCount(t, logPath)
	if _, err := src.BoardItems(context.Background(), src.cfg.Boards[0], false); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, logPath); n != before+1 {
		t.Fatalf("mutation should invalidate the list cache: %d → %d calls", before, n)
	}
}

func TestItemParsesDetail(t *testing.T) {
	_, logPath := fakeAZ(t, `{"id": 55, "title": "Story", "state": "Active", "assigned": "Ada Lovelace", "comments": 3, "description": "<div>Hello <b>world</b>&nbsp;&amp; more</div>"}`, "", 0)
	src := New(testConfig())
	item, err := src.Item(context.Background(), 55, false)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != 55 || item.CommentCount != 3 {
		t.Fatalf("item = %+v", item)
	}
	if got := StripHTML(item.Description); got != "Hello world & more" {
		t.Fatalf("StripHTML = %q", got)
	}
	if _, err := src.Item(context.Background(), 55, false); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, logPath); n != 1 {
		t.Fatalf("detail cache miss: %d calls", n)
	}
}

func TestColumns(t *testing.T) {
	configured := BoardConfig{Columns: []string{"A", "B"}}
	if got := Columns(configured, nil); !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Fatalf("configured columns = %v", got)
	}
	items := []WorkItem{{State: "Done"}, {State: "Zeta"}, {State: "New"}, {State: "Alpha"}}
	got := Columns(BoardConfig{}, items)
	want := []string{"New", "Done", "Alpha", "Zeta"} // defaults order, extras sorted
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("discovered columns = %v, want %v", got, want)
	}
	if got := Columns(BoardConfig{}, nil); !reflect.DeepEqual(got, []string{"New", "Active", "Resolved"}) {
		t.Fatalf("empty fallback = %v", got)
	}
}

func TestWebURL(t *testing.T) {
	src := New(testConfig())
	if got := src.WebURL(42); got != "https://dev.azure.com/demo/My%20Project/_workitems/edit/42" {
		t.Fatalf("WebURL = %q", got)
	}
}

func TestWIQLEscapesQuotes(t *testing.T) {
	_, logPath := fakeAZ(t, `[]`, "", 0)
	cfg := testConfig()
	cfg.Boards[0].Area = "It's a Team"
	src := New(cfg)
	if _, err := src.BoardItems(context.Background(), cfg.Boards[0], false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(logPath)
	if !strings.Contains(string(raw), "It''s a Team") {
		t.Fatalf("WIQL quote not escaped: %s", raw)
	}
}
