package work

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
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

func TestAddingMediaToExistingArtifactReopensReviewOnce(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	ingest(t, s, event(2, "run.start"))
	a := event(3, "artifact")
	a.Artifact = &Artifact{Key: "image", Kind: "image", Ref: "result.png"}
	ingest(t, s, a)
	finish := event(4, "run.finish")
	finish.Outcome = "success"
	ingest(t, s, finish)
	if e := s.Review("run", 2); e != nil {
		t.Fatal(e)
	}
	a.Seq = 5
	a.Artifact.MediaHash = strings.Repeat("a", 64)
	ingest(t, s, a)
	items, e := s.Inbox()
	if e != nil || len(items) != 1 || items[0].Version != "3" {
		t.Fatal(items, e)
	}
	a.Seq = 6
	ingest(t, s, a)
	d, e := s.RunSnapshot("run")
	if e != nil || len(d.Artifacts) != 1 || d.Runs[0].Version != 3 {
		t.Fatal(d, e)
	}
}

func TestCommitDiffRenditionDoesNotPoisonReplay(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	ingest(t, s, event(2, "run.start"))
	a := event(3, "artifact")
	a.Artifact = &Artifact{Key: "commit", Kind: "commit", Ref: strings.Repeat("c", 40), MediaHash: strings.Repeat("a", 64)}
	ingest(t, s, a)
	a.Seq = 4
	a.Artifact.MediaHash = strings.Repeat("b", 64)
	ingest(t, s, a)
	d, e := s.RunSnapshot("run")
	if e != nil || len(d.Artifacts) != 1 || d.Artifacts[0].MediaHash != strings.Repeat("a", 64) || d.Runs[0].Version != 1 {
		t.Fatal(d, e)
	}
	// This exception is specific to commit renderings; image identities remain immutable.
	a.Seq = 5
	a.Artifact = &Artifact{Key: "image", Kind: "image", Ref: "photo.jpg", MediaHash: strings.Repeat("a", 64)}
	ingest(t, s, a)
	a.Seq = 6
	a.Artifact.MediaHash = strings.Repeat("b", 64)
	if _, e = s.Ingest("collector", Batch{Generation: "generation", Events: []Event{a}}); e == nil {
		t.Fatal("conflicting image content accepted")
	}
}

func TestCommitDiffSharedAcrossCollectorRenderings(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	ingest(t, s, event(2, "run.start"))
	upload := func(collector, text string) string {
		sum := sha256.Sum256([]byte(text))
		digest := hex.EncodeToString(sum[:])
		if _, e := s.Upload(collector, digest, bytes.NewBufferString(text)); e != nil {
			t.Fatal(e)
		}
		return digest
	}
	first := upload("collector", "first diff")
	second := upload("other", "another rendering")
	a := event(3, "artifact")
	a.Artifact = &Artifact{Key: "shared-commit", Kind: "commit", Ref: strings.Repeat("c", 40), MediaHash: first}
	ingest(t, s, a)
	if _, e := s.Stream("other", "generation"); e != nil {
		t.Fatal(e)
	}
	_, e := s.Ingest("other", Batch{Generation: "generation", Events: []Event{
		{Seq: 1, At: time.Now(), Kind: "slot.seen", Pane: "other-pane", Session: "other-session"},
		{Seq: 2, At: time.Now(), Kind: "run.start", Pane: "other-pane", Session: "other-session", Run: "other-run"},
		{Seq: 3, At: time.Now(), Kind: "artifact", Pane: "other-pane", Session: "other-session", Run: "other-run", Artifact: &Artifact{Key: "shared-commit", Kind: "commit", Ref: strings.Repeat("c", 40), MediaHash: second}},
	}})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.RunSnapshot("other-run")
	if e != nil || len(d.Artifacts) != 1 || !d.Artifacts[0].Available || d.Artifacts[0].MediaHash != first {
		t.Fatal(d, e)
	}
	// An unrelated artifact cannot claim a blob merely by guessing its hash.
	_, e = s.Ingest("other", Batch{Generation: "generation", Events: []Event{{Seq: 4, At: time.Now(), Kind: "artifact", Pane: "other-pane", Session: "other-session", Run: "other-run", Artifact: &Artifact{Key: "unowned", Kind: "commit", Ref: "other", MediaHash: first}}}})
	if e != nil {
		t.Fatal(e)
	}
	d, e = s.RunSnapshot("other-run")
	if e != nil {
		t.Fatal(e)
	}
	for _, artifact := range d.Artifacts {
		if artifact.Key == "unowned" && artifact.Available {
			t.Fatal("unowned blob became available")
		}
	}
}
