package work

import (
	"testing"
	"time"
)

func TestMilestonesOpenEndAndCollectTheirWindow(t *testing.T) {
	s := testStore(t)
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for _, bad := range [][2]string{{"", "deploy"}, {"amythest", ""}, {"amythest", " padded "}, {"amythest", "a\nb"}} {
		if _, e := s.StartMilestone(bad[0], bad[1]); e == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	m, e := s.StartMilestone("amythest", "production deploy")
	if e != nil || !m.Open() || m.Started == "" {
		t.Fatalf("start: %+v %v", m, e)
	}
	now = now.Add(time.Minute)
	if again, _ := s.StartMilestone("amythest", "production deploy"); again.ID != m.ID {
		t.Fatal("starting an open milestone made another")
	}
	other, _ := s.StartMilestone("robot", "demo day")
	if open, _ := s.Milestones("amythest", true); len(open) != 1 || open[0].ID != m.ID {
		t.Fatalf("open on amythest: %+v", open)
	}
	if all, _ := s.Milestones("", false); len(all) != 2 {
		t.Fatalf("all: %d", len(all))
	}
	// Open, its window runs to now.
	if got, d, e := s.Collect(m.ID); e != nil || got.ID != m.ID || d.Projects == nil {
		t.Fatalf("collect open: %+v %v", got, e)
	}
	now = now.Add(time.Hour)
	ended, e := s.EndMilestone(m.ID)
	if e != nil || ended.Open() || ended.Ended == "" {
		t.Fatalf("end: %+v %v", ended, e)
	}
	if again, _ := s.EndMilestone(m.ID); again.Ended != ended.Ended {
		t.Fatal("ending twice moved the end")
	}
	if open, _ := s.Milestones("amythest", true); len(open) != 0 {
		t.Fatal("ended milestone still open")
	}
	// A new one with the same name is a new stretch of work.
	next, _ := s.StartMilestone("amythest", "production deploy")
	if next.ID == m.ID {
		t.Fatal("reopened the ended milestone")
	}
	if _, e = s.Milestone("missing"); e == nil {
		t.Fatal("found a missing milestone")
	}
	if _, _, e = s.Collect(other.ID); e != nil {
		t.Fatal(e)
	}
}
