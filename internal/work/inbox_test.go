package work

import (
	"fmt"
	"testing"
	"time"
)

func TestInboxContextDismissAndNewOutput(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	if _, e := s.db.Exec(`UPDATE slots SET tab='Fix the export screen'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AssignProject("project", "session", "Demo", 0); e != nil {
		t.Fatal(e)
	}
	ingest(t, s, event(2, "run.start"))
	output := event(3, "chat")
	output.DocumentID = "answer"
	output.Role = "assistant"
	output.Text = "Export now includes the selected dates."
	finish := event(4, "run.finish")
	finish.Outcome = "success"
	ingest(t, s, output, finish)
	items, e := s.Inbox()
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	a := items[0]
	if a.Title != "Fix the export screen" || a.Project != "Demo" || a.Session != "session" || a.Preview != output.Text || a.OutputCount != 1 || a.Outcome != "success" || a.Finished == "" {
		t.Fatal(a)
	}
	if e = s.Dismiss([]AttentionVersion{{a.Subject, a.Version}}); e != nil {
		t.Fatal(e)
	}
	// Dismissal is durable, and does not falsely certify the result as reviewed.
	original := s.now
	s.now = func() time.Time { return original().Add(30 * 24 * time.Hour) }
	s.Heartbeat("collector", "Demo machine", false, nil)
	items, e = s.Inbox()
	if e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
	d, e := s.RunSnapshot("run")
	if e != nil || len(d.Documents) != 1 || d.Runs[0].Reviewed != 0 {
		t.Fatal(d, e)
	}
	late := event(5, "chat")
	late.DocumentID = "followup"
	late.Role = "assistant"
	late.Text = "Additional validation finished."
	ingest(t, s, late)
	items, e = s.Inbox()
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	if e = s.Dismiss([]AttentionVersion{{a.Subject, a.Version}}); e == nil {
		t.Fatal("stale dismissal hid new output")
	}
	if e = s.Dismiss([]AttentionVersion{{items[0].Subject, items[0].Version}, {"run:missing", "1"}}); e == nil {
		t.Fatal("invalid batch accepted")
	}
	items, e = s.Inbox()
	if e != nil || len(items) != 1 {
		t.Fatal("partial dismissal committed", items, e)
	}
}

func TestRunSnapshotOutsideRecentWindowAndDismissedBacklog(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	tx, e := s.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 1001; i++ {
		_, e = tx.Exec(`INSERT INTO runs(id,collector,session_id,binding_id,binding_version,card_id,project,started,finished,outcome,version) VALUES(?,'collector','session','',0,'','',?,?,'success',1)`, fmt.Sprintf("run-%04d", i), fmt.Sprint(i), fmt.Sprint(i))
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	d, e := s.RunSnapshot("run-0000")
	if e != nil || len(d.Runs) != 1 || d.Runs[0].ID != "run-0000" || !d.Runs[0].Reviewable {
		t.Fatal(d, e)
	}
	items, e := s.Inbox()
	if e != nil || len(items) != 1000 {
		t.Fatal(len(items), e)
	}
	versions := []AttentionVersion{}
	for _, a := range items {
		versions = append(versions, AttentionVersion{a.Subject, a.Version})
	}
	if e = s.Dismiss(versions); e != nil {
		t.Fatal(e)
	}
	items, e = s.Inbox()
	if e != nil || len(items) != 1 {
		t.Fatal("dismissed items starved remaining backlog", len(items), e)
	}
}

func TestDismissStateChangesReappear(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	s.Heartbeat("collector", "Demo machine", false, nil)
	blocked := event(2, "slot.status")
	blocked.Status = "blocked"
	ingest(t, s, blocked)
	items, e := s.Inbox()
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	if e = s.Dismiss([]AttentionVersion{{items[0].Subject, items[0].Version}}); e != nil {
		t.Fatal(e)
	}
	if items, e = s.Inbox(); e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
	if _, e = s.db.Exec(`UPDATE slots SET last_output='new output'`); e != nil {
		t.Fatal(e)
	}
	if items, e = s.Inbox(); e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	s.now = func() time.Time { return time.Now().Add(time.Hour) }
	items, e = s.Inbox()
	if e != nil || len(items) != 1 || items[0].Reason != "offline" {
		t.Fatal(items, e)
	}
	if e = s.Dismiss([]AttentionVersion{{items[0].Subject, items[0].Version}}); e != nil {
		t.Fatal(e)
	}
	if items, e = s.Inbox(); e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
}
