package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQueueRestartBindingAndLateOutput(t *testing.T) {
	dir := t.TempDir()
	q, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := Open(dir); e == nil {
		other.Close()
		t.Fatal("two queue writers")
	}
	p := Pane{ID: "pane", Provider: "provider-session", Agent: "claude", Status: "idle"}
	now := time.Now()
	if e = q.Inventory([]Pane{p}, now); e != nil {
		t.Fatal(e)
	}
	s := q.Snapshot()
	session := s.Sessions[p.ID].ID
	q.Bind([]Binding{{ID: "a", Version: 1, Session: session, Card: "card-a"}})
	p.Status = "working"
	q.Inventory([]Pane{p}, now)
	q.Bind([]Binding{{ID: "b", Version: 2, Session: session, Card: "card-b"}})
	p.Status = "done"
	q.Inventory([]Pane{p}, now)
	s = q.Snapshot()
	if len(s.Jobs) != 1 || s.Jobs[0].Session.RunBinding.ID != "a" {
		t.Fatal("run binding changed", s.Jobs)
	}
	job := s.Jobs[0]
	p.Status = "working"
	q.Inventory([]Pane{p}, now)
	s = q.Snapshot()
	if s.Sessions[p.ID].RunBinding.ID != "b" {
		t.Fatal("new run missed rebind")
	}
	q.Close()
	q, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	if e = q.Complete(job, []Artifact{{Key: "key", Kind: "commit", Ref: "sha", Attribution: "suggested"}}); e != nil {
		t.Fatal(e)
	}
	s = q.Snapshot()
	var finishes, starts int
	for i, ev := range s.Events {
		if ev.Seq != int64(i+1) {
			t.Fatal("gap", ev.Seq)
		}
		if ev.Kind == "run.finish" {
			finishes++
		}
		if ev.Kind == "run.start" {
			starts++
			if starts == 2 && finishes != 1 {
				t.Fatal("started next run before ending previous")
			}
		}
	}
	if finishes != 1 {
		t.Fatal("finish duplicated", finishes)
	}
	if last := s.Events[len(s.Events)-1]; last.Kind != "artifact" || last.Binding != "a" {
		t.Fatal(last)
	}
	if e = q.Ack(s.Generation, s.Next); e == nil {
		t.Fatal("ack beyond queue accepted")
	}
	if e = q.Ack(s.Generation, s.Next-1); e != nil {
		t.Fatal(e)
	}
	if len(q.Snapshot().Events) != 0 {
		t.Fatal("ack did not remove prefix")
	}
	p.Provider = "reused-pane"
	q.Inventory([]Pane{p}, now)
	if q.Snapshot().Sessions[p.ID].ID == session {
		t.Fatal("pane reused session")
	}
}
func TestFailedUpdateDoesNotChangeQueue(t *testing.T) {
	q, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	before, _ := json.Marshal(q.Snapshot())
	e = q.Update(func(s *State) error { s.Next = 100; return os.ErrPermission })
	after, _ := json.Marshal(q.Snapshot())
	if e == nil || string(before) != string(after) {
		t.Fatal("failed transaction changed state")
	}
	mode, e := os.Stat(filepath.Join(filepath.Dir(q.path), "hub.json"))
	if e != nil || mode.Mode().Perm() != 0600 {
		t.Fatal("queue permissions", e)
	}
}
