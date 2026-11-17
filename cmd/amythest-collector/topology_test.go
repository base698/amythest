package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/base698/amythest/internal/collector/herdr"
	"github.com/base698/amythest/internal/collector/hub"
)

// The shapes below are what herdr-core 0.1.21 sends: status is an object.
const hcFixture = `[
 {"address":"t1","state":"live","cwd":"/w/app","created_ms":100,"status":{"app":"claude-code","state":"working"},
  "labels":{"where":"robot / gait"},"foreground":{"cwd":"/w/app/sub","pid":4242,"argv":["claude"]}},
 {"address":"t2","state":"live","cwd":"/w","created_ms":200,"status":null,"labels":{"where":"robot / shell"}},
 {"address":"t3","state":"finished","cwd":"/w","created_ms":300,"status":{"app":"claude-code","state":"done"},"labels":{"where":"robot / old"}},
 {"address":"vps/t4","state":"live","cwd":"/srv","created_ms":400,"status":{"app":"claude-code","state":"idle"},"labels":{"where":"remote / 1"}},
 {"address":"t5","state":"live","cwd":"/w","created_ms":500,"status":{"app":"codex","state":"working"},"labels":{"where":"robot / codex"}},
 {"address":"t6","state":"live","cwd":"/w/app","created_ms":600,"parent":"t1","status":{"app":"claude-code","state":"error"},"labels":{}},
 {"address":"t7","state":"live","cwd":"/h","created_ms":700,"status":{"app":"hermes","state":"blocked"},"labels":{"where":"Hermes / cron"}}
]`

func fixture(t *testing.T) []herdr.HCTerminal {
	t.Helper()
	var terms []herdr.HCTerminal
	if e := json.Unmarshal([]byte(hcFixture), &terms); e != nil {
		t.Fatal(e)
	}
	return terms
}

func TestHCPanesKeepsOnlyLocalLiveAgents(t *testing.T) {
	got := hcPanes(fixture(t), nil, false)
	want := []hub.Pane{
		{ID: "t1", Provider: "hc:t1@100", Agent: "claude", Project: "robot", Tab: "gait", Dir: "/w/app/sub", Status: "working"},
		// A helper with no place of its own is filed under its parent's; an
		// unknown state is reported as idle.
		{ID: "t6", Provider: "hc:t6@600", Agent: "claude", Project: "robot", Tab: "gait", Dir: "/w/app", Status: "idle"},
		{ID: "t7", Provider: "hc:t7@700", Agent: "hermes", Project: "Hermes", Tab: "cron", Dir: "/h", Status: "blocked"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d panes %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pane %d\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
	for _, p := range hcPanes(fixture(t), nil, true) {
		if p.Agent == "hermes" {
			t.Errorf("skipHermes kept %+v", p)
		}
	}
}

func TestHCStatusAcceptsBareState(t *testing.T) {
	var term herdr.HCTerminal
	if e := json.Unmarshal([]byte(`{"address":"t1","status":"done"}`), &term); e != nil {
		t.Fatal(e)
	}
	if term.StatusString() != "done" {
		t.Fatalf("status %q", term.StatusString())
	}
}

func TestClaudeSessionFromProcessRecordThenCommandLine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if e := os.MkdirAll(filepath.Join(dir, "sessions"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "sessions", "4242.json"), []byte(`{"pid":4242,"sessionId":"from-record"}`), 0600); e != nil {
		t.Fatal(e)
	}
	terms := fixture(t)
	if got := claudeSession(terms[0]); got != "from-record" {
		t.Errorf("process record: got %q", got)
	}
	// A record left by another process must not be believed.
	if e := os.WriteFile(filepath.Join(dir, "sessions", "4242.json"), []byte(`{"pid":7,"sessionId":"stale"}`), 0600); e != nil {
		t.Fatal(e)
	}
	terms[0].Fg.Argv = []string{"claude", "--resume", "from-argv"}
	if got := claudeSession(terms[0]); got != "from-argv" {
		t.Errorf("command line: got %q", got)
	}
	terms[0].Fg.Argv = []string{"claude"}
	if got := claudeSession(terms[0]); got != "" {
		t.Errorf("unknown session: got %q", got)
	}
	if got := claudeSession(terms[6]); got != "" { // not Claude
		t.Errorf("hermes: got %q", got)
	}
}

type recorder struct{ calls [][]hub.Pane }

func (r *recorder) Inventory(p []hub.Pane, _ time.Time) error {
	r.calls = append(r.calls, append([]hub.Pane(nil), p...))
	return nil
}

func ids(p []hub.Pane) string {
	s := ""
	for _, x := range p {
		s += x.ID + " "
	}
	return s
}

func TestTopologyWaitsForEveryRuntimeThenMerges(t *testing.T) {
	r := &recorder{}
	now := time.Unix(1000, 0)
	topo := newTopology(r, "tui", "hc")
	topo.now = func() time.Time { return now }
	tui := []hub.Pane{{ID: "w1:p1", Agent: "claude", Status: "idle"}}
	hc := []hub.Pane{{ID: "t1", Agent: "claude", Status: "working"}}

	if e := topo.report("tui", tui); e != nil {
		t.Fatal(e)
	}
	if len(r.calls) != 0 {
		t.Fatalf("inventoried before herdr-core was accounted for: %v", r.calls)
	}
	if e := topo.report("hc", hc); e != nil {
		t.Fatal(e)
	}
	if len(r.calls) != 1 || ids(r.calls[0]) != "w1:p1 t1 " {
		t.Fatalf("merged inventory: %v", r.calls)
	}
	// An unchanged topology is not re-sent until the refresh interval.
	topo.report("hc", hc)
	if len(r.calls) != 1 {
		t.Fatalf("re-sent an unchanged inventory")
	}
	now = now.Add(inventoryRefresh)
	topo.report("hc", hc)
	if len(r.calls) != 2 {
		t.Fatalf("did not refresh after %v", inventoryRefresh)
	}
	// A change is sent at once.
	hc[0].Status = "done"
	topo.report("hc", append([]hub.Pane(nil), hc...))
	if len(r.calls) != 3 || r.calls[2][1].Status != "done" {
		t.Fatalf("status change not delivered: %v", r.calls)
	}
}

func TestTopologyRuntimeThatIsNotInstalled(t *testing.T) {
	r := &recorder{}
	now := time.Unix(1000, 0)
	topo := newTopology(r, "tui", "hc")
	topo.now = func() time.Time { return now }
	topo.report("tui", []hub.Pane{{ID: "w1:p1", Agent: "hermes", Provider: "x", Status: "idle"}})
	topo.unreachable("hc", true)
	if len(r.calls) != 0 {
		t.Fatalf("inventoried inside the grace period: %v", r.calls)
	}
	now = now.Add(absentGrace)
	topo.unreachable("hc", true)
	if len(r.calls) != 1 || ids(r.calls[0]) != "w1:p1 " {
		t.Fatalf("a machine without herdr-core must still inventory terminal herdr: %v", r.calls)
	}
}

func TestTopologyKeepsPanesWhileARuntimeRestarts(t *testing.T) {
	r := &recorder{}
	now := time.Unix(1000, 0)
	topo := newTopology(r, "tui", "hc")
	topo.now = func() time.Time { return now }
	topo.report("tui", []hub.Pane{})
	topo.report("hc", []hub.Pane{{ID: "t1", Agent: "claude", Status: "working"}})

	// Socket gone for less than the grace period: an update or a restart.
	now = now.Add(10 * time.Second)
	topo.unreachable("hc", true)
	now = now.Add(absentGrace - time.Second)
	topo.report("tui", []hub.Pane{})
	last := r.calls[len(r.calls)-1]
	if ids(last) != "t1 " {
		t.Fatalf("pane dropped during a short outage: %v", last)
	}
	// A failure that is not "nothing is listening" proves nothing, however long.
	topo.unreachable("hc", false)
	now = now.Add(10 * absentGrace)
	topo.report("tui", []hub.Pane{})
	last = r.calls[len(r.calls)-1]
	if ids(last) != "t1 " {
		t.Fatalf("pane dropped on an inconclusive failure: %v", last)
	}
	// Not running for the whole grace period: its panes are gone.
	topo.unreachable("hc", true)
	now = now.Add(absentGrace)
	topo.unreachable("hc", true)
	last = r.calls[len(r.calls)-1]
	if ids(last) != "" {
		t.Fatalf("pane kept after herdr-core stopped: %v", last)
	}
}

// The queue must see a herdr-core terminal as one continuing session across
// reports, start a run when it works and finish it when its turn is done.
func TestHCPanesDriveQueueRuns(t *testing.T) {
	q, e := hub.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	topo := newTopology(q, "hc")
	terms := fixture(t)[:1]
	for _, state := range []string{"working", "working", "done"} {
		terms[0].Status.State = state
		if e := topo.report("hc", hcPanes(terms, nil, false)); e != nil {
			t.Fatal(e)
		}
	}
	s := q.Snapshot()
	kinds := ""
	sessions := map[string]bool{}
	for _, ev := range s.Events {
		kinds += ev.Kind + " "
		sessions[ev.Session] = true
	}
	if kinds != "slot.seen run.start slot.status slot.status run.finish " {
		t.Fatalf("events: %s", kinds)
	}
	if len(sessions) != 1 {
		t.Fatalf("one terminal produced %d sessions", len(sessions))
	}
	if len(s.Jobs) != 1 || s.Jobs[0].Outcome != "success" || s.Jobs[0].Session.Pane.Dir != "/w/app/sub" {
		t.Fatalf("job: %+v", s.Jobs)
	}
}
