package work

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, e := Open(Config{DB: filepath.Join(dir, "work.db"), MediaDir: filepath.Join(dir, "media")})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func event(seq int64, kind string) Event {
	return Event{Seq: seq, Kind: kind, At: time.Now(), Pane: "pane", Session: "session", Run: "run"}
}
func ingest(t *testing.T, s *Store, events ...Event) Ack {
	t.Helper()
	a, e := s.Ingest("collector", Batch{Generation: "generation", Events: events})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func seed(t *testing.T, s *Store) {
	t.Helper()
	if _, e := s.Stream("collector", "generation"); e != nil {
		t.Fatal(e)
	}
	ingest(t, s, event(1, "slot.seen"))
}
func TestAtomicReplayGapMalformedAndRestart(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	start := event(2, "run.start")
	a := ingest(t, s, start)
	if a.Acked != 2 {
		t.Fatal(a)
	}
	output := event(3, "chat")
	output.DocumentID = "turn"
	output.Role = "assistant"
	output.Text = "new output"
	bad := event(4, "unsupported")
	_, e := s.Ingest("collector", Batch{"generation", []Event{output, bad}})
	var f *Fault
	if !errors.As(e, &f) || f.Status != 422 || f.Seq != 4 || f.Acked != 2 {
		t.Fatal(e)
	}
	d, _ := s.Snapshot("", "")
	if len(d.Documents) != 0 {
		t.Fatal("partial batch applied")
	}
	gap := output
	gap.Seq = 4
	_, e = s.Ingest("collector", Batch{"generation", []Event{gap}})
	if !errors.As(e, &f) || f.Status != 409 || f.Expected != 3 {
		t.Fatal(e)
	}
	if a = ingest(t, s, start, output); a.Acked != 3 {
		t.Fatal(a)
	}
	if a = ingest(t, s, start, output); a.Acked != 3 {
		t.Fatal(a)
	}
	if e = s.Prune(time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if a = ingest(t, s, start, output); a.Acked != 3 {
		t.Fatal(a)
	}
	d, _ = s.Snapshot("", "")
	if len(d.Documents) != 0 {
		t.Fatal("pruned output resurrected")
	}
	cfg := s.cfg
	s.Close()
	next, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	a, e = next.Stream("collector", "generation")
	if e != nil || a.Acked != 3 {
		t.Fatal(a, e)
	}
	if _, e = next.Stream("collector", "another"); e == nil {
		t.Fatal("unapproved generation reset")
	}
	if e = next.Reset("collector"); e != nil {
		t.Fatal(e)
	}
	if _, e = next.Stream("collector", "generation"); e == nil {
		t.Fatal("closed generation reopened")
	}
	if _, e = next.Stream("collector", "another"); e != nil {
		t.Fatal(e)
	}
}
func TestBindingSnapshotsReviewAndOwnership(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	b, e := s.Bind("owner", "request", "collector", "session", "card-a", 0)
	if e != nil {
		t.Fatal(e)
	}
	start := event(2, "run.start")
	start.Binding = b.ID
	start.BindingVersion = b.Version
	ingest(t, s, start)
	next, e := s.Bind("owner", "rebind", "collector", "session", "card-b", 1)
	if e != nil || next.Version != 2 {
		t.Fatal(next, e)
	}
	out := event(3, "artifact")
	out.Binding = b.ID
	out.BindingVersion = b.Version
	out.Artifact = &Artifact{Key: "commit:repo:sha", Kind: "commit", Ref: "sha", Title: "a change", Attribution: "exact"}
	finish := event(4, "run.finish")
	finish.Binding = b.ID
	finish.BindingVersion = b.Version
	finish.Outcome = "success"
	ingest(t, s, out, finish)
	d, e := s.Snapshot("card-a", "")
	if e != nil || len(d.Runs) != 1 || len(d.Artifacts) != 1 {
		t.Fatal(d, e)
	}
	if d.Runs[0].Version != 2 {
		t.Fatal(d.Runs)
	}
	if e = s.Review("run", 1); e != nil {
		t.Fatal(e)
	}
	items, _ := s.Inbox()
	if len(items) != 1 {
		t.Fatal("finish newer than displayed review must remain", items)
	}
	s.Review("run", 2)
	items, _ = s.Inbox()
	if len(items) != 0 {
		t.Fatal(items)
	}
	late := event(5, "chat")
	late.Binding = b.ID
	late.BindingVersion = b.Version
	late.DocumentID = "late"
	late.Text = "late output"
	late.Role = "assistant"
	ingest(t, s, late)
	items, _ = s.Inbox()
	if len(items) != 1 {
		t.Fatal("late output must reopen", items)
	}
	s.Stream("foreign", "gen")
	_, e = s.Ingest("foreign", Batch{"gen", []Event{event(1, "slot.seen")}})
	if e == nil {
		t.Fatal("foreign collector claimed session")
	}
	replacement := event(6, "slot.seen")
	replacement.Session = "new-session"
	ingest(t, s, replacement)
	if _, e = s.Bind("owner", "stale", "collector", "session", "card-c", 2); e == nil {
		t.Fatal("bound stale incarnation")
	}
	d, e = s.Snapshot("card-b", "")
	if e != nil || len(d.Runs) != 0 {
		t.Fatal("rebind rewrote existing run", d, e)
	}
}
func TestHistoricalConnectivitySearchAndMedia(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	start := event(2, "run.start")
	start.Historical = true
	out := event(3, "chat")
	out.Role = "assistant"
	out.DocumentID = "old"
	out.Text = "robot movement"
	finish := event(4, "run.finish")
	finish.Outcome = "success"
	ingest(t, s, start, out, finish)
	items, _ := s.Inbox()
	if len(items) != 0 {
		t.Fatal("backfill caused live alert", items)
	}
	hits, e := s.Search(SearchOptions{Query: "robot"})
	if e != nil || len(hits) != 1 {
		t.Fatal(hits, e)
	}
	hits, e = s.Search(SearchOptions{Query: "robot", Card: "another"})
	if e != nil || len(hits) != 0 {
		t.Fatal("scope leaked", hits, e)
	}
	now := time.Now()
	s.now = func() time.Time { return now }
	s.Heartbeat("collector", "Laptop", false, nil)
	now = now.Add(10 * time.Minute)
	items, _ = s.Inbox()
	if len(items) != 1 || items[0].Reason != "offline" {
		t.Fatal(items)
	}
	data := []byte("<html>not executable</html>")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	m, e := s.Upload("collector", digest, bytes.NewReader(data))
	if e != nil || m.Type != "application/octet-stream" {
		t.Fatal(m, e)
	}
	if _, e = s.Upload("collector", digest, bytes.NewReader([]byte("wrong"))); e == nil {
		t.Fatal("accepted wrong hash")
	}
	file, _, e := s.OpenMedia(digest)
	if e != nil {
		t.Fatal(e)
	}
	file.Close()
	if _, _, e = s.OpenMedia("../../auth.db"); e == nil {
		t.Fatal("path traversal")
	}
}

func TestSharedArtifactSearchManualFilingAndQuarantine(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	ingest(t, s, event(2, "run.start"))
	a := event(3, "artifact")
	a.Artifact = &Artifact{Key: "shared", Kind: "commit", Ref: "sha", Title: "unique searchable artifact", Attribution: "suggested"}
	end := event(4, "run.finish")
	end.Outcome = "success"
	ingest(t, s, a, end)
	start := event(5, "run.start")
	start.Run = "second"
	a.Seq = 6
	a.Run = "second"
	ingest(t, s, start, a)
	hits, e := s.Search(SearchOptions{Query: "searchable"})
	if e != nil || len(hits) != 1 {
		t.Fatal("artifact text duplicated", hits, e)
	}
	d, e := s.Snapshot("", "")
	if e != nil || len(d.Artifacts) != 2 {
		t.Fatal(d, e)
	}
	if e = s.File("file-request", d.Artifacts[0].LinkID, "card", true); e != nil {
		t.Fatal(e)
	}
	hits, e = s.Search(SearchOptions{Query: "searchable", Card: "card"})
	if e != nil || len(hits) != 1 {
		t.Fatal(hits, e)
	}
	a.Seq = 7
	ingest(t, s, a)
	d, e = s.Snapshot("card", "")
	if e != nil || len(d.Artifacts) != 1 || d.Artifacts[0].Attribution != "manual" || !d.Artifacts[0].Featured {
		t.Fatal("collector overwrote manual association", d, e)
	}
	if e = s.Quarantine("collector", "generation", 9, "skip gap"); e == nil {
		t.Fatal("quarantine skipped missing sequence")
	}
	if e = s.Quarantine("collector", "generation", 8, "bad record"); e != nil {
		t.Fatal(e)
	}
	bad := event(8, "bad")
	if a := ingest(t, s, bad); a.Acked != 8 {
		t.Fatal(a)
	}
}
func TestReviewDoesNotOfferToAcknowledgeOmittedOutput(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	ingest(t, s, event(2, "run.start"))
	batch := []Event{}
	for i := int64(3); i < 205; i++ {
		e := event(i, "chat")
		e.DocumentID = fmt.Sprint(i)
		e.Role = "assistant"
		e.Text = "output"
		batch = append(batch, e)
	}
	ingest(t, s, batch...)
	d, e := s.Snapshot("", "")
	if e != nil || len(d.Documents) != 200 || d.Runs[0].Reviewable {
		t.Fatal("view offered review for omitted output", e)
	}
}

func TestMediaPendingUploadAndEviction(t *testing.T) {
	s := testStore(t)
	s.cfg.MediaMaxFile = 10
	s.cfg.MediaMaxTotal = 10
	put := func(b string) string {
		sum := sha256.Sum256([]byte(b))
		h := hex.EncodeToString(sum[:])
		if _, e := s.Upload("collector", h, strings.NewReader(b)); e != nil {
			t.Fatal(e)
		}
		return h
	}
	old := put("first123")
	next := put("second12")
	if _, _, e := s.OpenMedia(old); e == nil {
		t.Fatal("quota did not evict old unfeatured file")
	}
	seed(t, s)
	ingest(t, s, event(2, "run.start"))
	a := event(3, "artifact")
	a.Artifact = &Artifact{Key: "file-observation", Kind: "image", MediaHash: next, Attribution: "exact"}
	ingest(t, s, a)
	d, _ := s.Snapshot("", "")
	if !d.Artifacts[0].Available {
		t.Fatal("uploaded media unavailable")
	}
	s.File("pin", d.Artifacts[0].LinkID, "", true)
	sum := sha256.Sum256([]byte("third123"))
	_, e := s.Upload("collector", hex.EncodeToString(sum[:]), strings.NewReader("third123"))
	var f *Fault
	if !errors.As(e, &f) || f.Status != 507 {
		t.Fatal("evicted featured bytes", e)
	}
	pending := sha256.Sum256([]byte("tiny"))
	a.Seq = 4
	a.Artifact = &Artifact{Key: "pending-file", Kind: "image", MediaHash: hex.EncodeToString(pending[:]), Attribution: "exact"}
	ingest(t, s, a)
	d, _ = s.Snapshot("", "")
	for _, a := range d.Artifacts {
		if a.Key == "pending-file" && a.Available {
			t.Fatal("pending bytes exposed")
		}
	}
}
