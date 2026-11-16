package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHookAndHerdrOneRunWithAuthoritativeOutcome(t *testing.T) {
	q, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	at := time.Now()
	pane := Pane{ID: "pane", Agent: "hermes", Provider: "provider", Status: "working"}
	q.Inventory([]Pane{pane}, at)
	h := Lifecycle{ID: "start", At: at, Phase: "start", Provider: "provider", Turn: "turn"}
	if e = q.Lifecycle(h); e != nil {
		t.Fatal(e)
	}
	pane.Status = "idle"
	q.Inventory([]Pane{pane}, at)
	state := q.Snapshot()
	if state.Sessions["pane"].Run == "" {
		t.Fatal("screen idle ended hook-owned turn")
	}
	h.ID = "finish"
	h.Phase = "finish"
	h.Outcome = "failure"
	q.Lifecycle(h)
	q.Lifecycle(h)
	h.ID = "duplicate-finish"
	q.Lifecycle(h)
	h.ID = "duplicate-start"
	h.Phase = "start"
	q.Lifecycle(h)
	starts, finishes := 0, 0
	for _, ev := range q.Snapshot().Events {
		if ev.Kind == "run.start" {
			starts++
		}
		if ev.Kind == "run.finish" {
			finishes++
			if ev.Outcome != "failure" {
				t.Fatal(ev)
			}
		}
	}
	if starts != 1 || finishes != 1 {
		t.Fatal(starts, finishes)
	}
}
func TestHookBeforeSnapshotAndStaleFinish(t *testing.T) {
	q, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	h := Lifecycle{ID: "s", At: time.Now(), Phase: "start", Provider: "provider", Turn: "one"}
	q.Lifecycle(h)
	q.Inventory([]Pane{{ID: "pane", Agent: "hermes", Provider: "provider", Status: "working"}}, time.Now())
	if len(q.Snapshot().Sessions) != 1 {
		t.Fatal("duplicate incarnation")
	}
	h.ID = "new"
	h.Turn = "two"
	q.Lifecycle(h)
	h.ID = "late"
	h.Phase = "finish"
	h.Turn = "one"
	q.Lifecycle(h)
	for _, p := range q.Snapshot().Sessions {
		if p.Run == "" || p.Turn != "two" {
			t.Fatal("late finish ended current turn")
		}
	}
}
func TestLifecycleSpoolReplayAfterRestart(t *testing.T) {
	dir := t.TempDir()
	q, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	h := Lifecycle{ID: "durable", At: time.Now(), Phase: "start", Provider: "provider", Turn: "one"}
	q.Lifecycle(h)
	next := q.Snapshot().Next
	q.Close()
	q, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	spool := filepath.Join(dir, "hooks")
	os.Mkdir(spool, 0700)
	b, _ := json.Marshal(h)
	os.WriteFile(filepath.Join(spool, "pending.json"), b, 0600)
	if e = q.DrainLifecycle(spool); e != nil {
		t.Fatal(e)
	}
	if q.Snapshot().Next != next {
		t.Fatal("replay duplicated committed events")
	}
	entries, _ := os.ReadDir(spool)
	if len(entries) != 0 {
		t.Fatal("committed entry not removed")
	}
}

func TestHermesWaitsForProviderIdentity(t *testing.T) {
	q, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	q.Inventory([]Pane{{ID: "pane", Agent: "hermes", Status: "working"}}, time.Now())
	if len(q.Snapshot().Events) != 0 {
		t.Fatal("unidentified Hermes pane synthesized a duplicate run")
	}
}
