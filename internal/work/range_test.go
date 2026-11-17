package work

import (
	"fmt"
	"testing"
	"time"
)

func TestSnapshotRangeFiltersBeforeLimitsAndClassifiesInputs(t *testing.T) {
	s := testStore(t)
	if _, err := s.Stream("collector", "generation"); err != nil {
		t.Fatal(err)
	}
	seen := event(1, "slot.seen")
	seen.Project = "robot"
	ingest(t, s, seen)
	start := event(2, "run.start")
	start.Project = "robot"
	ingest(t, s, start)
	at := func(value string) time.Time {
		v, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	seq := int64(3)
	artifact := func(key, stamp string) {
		ev := event(seq, "artifact")
		seq++
		ev.At = at(stamp)
		ev.Artifact = &Artifact{Key: key, Kind: "image", Ref: key, Title: key}
		ingest(t, s, ev)
	}
	chat := func(key, stamp string) {
		ev := event(seq, "chat")
		seq++
		ev.At = at(stamp)
		ev.DocumentID = key
		ev.Role = "user"
		ev.Text = key
		ingest(t, s, ev)
	}
	artifact("old", "2026-11-13T04:59:59.999999999Z")
	artifact("input:start:image:0", "2026-11-13T00:00:00-05:00")
	artifact("last", "2026-11-17T04:59:59.999999999Z")
	artifact("next", "2026-11-17T05:00:00Z")
	chat("old", "2026-11-13T04:59:59.999999999Z")
	chat("first", "2026-11-13T05:00:00Z")
	chat("next", "2026-11-17T05:00:00Z")
	for i := 0; i < 501; i++ {
		artifact(fmt.Sprintf("recent-%d", i), "2026-11-18T12:00:00Z")
	}
	latest, err := s.Snapshot("", "robot")
	if err != nil || len(latest.Artifacts) != 500 {
		t.Fatal(err, len(latest.Artifacts))
	}
	data, err := s.SnapshotRange("", "robot", "2026-11-13T00:00:00-05:00", "2026-11-17T00:00:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Artifacts) != 2 || len(data.Documents) != 1 || data.Documents[0].Body != "first" {
		t.Fatalf("unexpected window: %#v", data)
	}
	if data.Artifacts[0].Key != "last" || data.Artifacts[0].Input || !data.Artifacts[1].Input {
		t.Fatal(data.Artifacts)
	}
	if len(data.Runs) != 1 {
		t.Fatal("lost live run", data.Runs)
	}
	other, err := s.SnapshotRange("", "another", "2026-11-13T00:00:00-05:00", "2026-11-17T00:00:00-05:00")
	if err != nil || len(other.Artifacts) != 0 || len(other.Documents) != 0 {
		t.Fatal("workspace leaked", err)
	}
	for _, bounds := range [][2]string{{"", ""}, {"invalid", "2026-11-17T00:00:00Z"}, {"2026-11-17T00:00:00Z", "2026-11-17T00:00:00Z"}, {"2026-11-18T00:00:00Z", "2026-11-17T00:00:00Z"}} {
		if _, err = s.SnapshotRange("", "robot", bounds[0], bounds[1]); err == nil {
			t.Fatal("accepted invalid date range")
		}
	}
}
