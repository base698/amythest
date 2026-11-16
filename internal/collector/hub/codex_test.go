package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexOptInCheckpointPartialAndPrivacy(t *testing.T) {
	dir := t.TempDir()
	q, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "rollout.jsonl")
	config := filepath.Join(dir, "codex.json")
	meta := `{"type":"session_meta","payload":{"id":"chat"}}` + "\n"
	start := `{"timestamp":"2026-11-16T20:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn"}}` + "\n"
	private := `{"type":"response_item","payload":{"type":"message","content":"PRIVATE-PROMPT"}}` + "\n"
	finish := `{"timestamp":"2026-11-16T20:10:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn","last_agent_message":"PRIVATE-RESPONSE"}}` + "\n"
	os.WriteFile(path, []byte(meta+start+private+finish[:30]), 0600)
	b, _ := json.Marshal([]CodexWatch{{Session: "chat", Path: path, Project: "Amythest", Title: "Plan"}})
	os.WriteFile(config, b, 0600)
	if e = q.PollCodex(config); e != nil {
		t.Fatal(e)
	}
	state := q.Snapshot()
	if len(state.Sessions) != 1 || len(state.Events) != 3 {
		t.Fatal(state)
	}
	for _, s := range state.Sessions {
		if s.Pane.Agent != "codex" || s.Run == "" {
			t.Fatal(s)
		}
	}
	saved, _ := os.ReadFile(filepath.Join(dir, "hub.json"))
	if strings.Contains(string(saved), "PRIVATE") {
		t.Fatal("private contents persisted")
	}
	q.Close()
	q, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	os.WriteFile(path, []byte(meta+start+private+finish), 0600)
	if e = q.PollCodex(config); e != nil {
		t.Fatal(e)
	}
	state = q.Snapshot()
	if len(state.Events) != 5 {
		t.Fatal(state.Events)
	}
	for _, s := range state.Sessions {
		if s.Run != "" || s.Pane.Status != "idle" {
			t.Fatal(s)
		}
	}
	// Force checkpoint replay, including a finish event, to model a crash window.
	q.Update(func(s *State) error { s.CodexOffsets = nil; return nil })
	if e = q.PollCodex(config); e != nil || len(q.Snapshot().Events) != 5 {
		t.Fatal(e, q.Snapshot().Events)
	}
	b, _ = json.Marshal([]CodexWatch{{Session: "another-chat", Path: path}})
	os.WriteFile(config, b, 0600)
	if e = q.PollCodex(config); e == nil {
		t.Fatal("watched wrong session")
	}
}
func TestLifecycleAgentNamespace(t *testing.T) {
	q, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	for _, agent := range []string{"hermes", "codex"} {
		h := Lifecycle{Agent: agent, ID: agent + "-start", At: time.Now(), Phase: "start", Provider: "same", Turn: "same"}
		if e = q.Lifecycle(h); e != nil {
			t.Fatal(e)
		}
		h.ID = agent + "-finish"
		h.Phase = "finish"
		h.Outcome = "success"
		if e = q.Lifecycle(h); e != nil {
			t.Fatal(e)
		}
	}
	if len(q.Snapshot().Sessions) != 2 {
		t.Fatal(q.Snapshot())
	}
	for _, s := range q.Snapshot().Sessions {
		if s.Run != "" || s.Pane.Status != "idle" {
			t.Fatal(s)
		}
	}
}
