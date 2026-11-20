package work

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMilestonesOpenEndAndCollectTheirWindow(t *testing.T) {
	s := testStore(t)
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for _, bad := range [][2]string{{"", "deploy"}, {"amythest", ""}, {"amythest", " padded "}, {"amythest", "a\nb"}} {
		if _, e := s.StartMilestone(bad[0], bad[1], "", ""); e == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	m, e := s.StartMilestone("amythest", "production deploy", "", "")
	if e != nil || !m.Open() || m.Started == "" {
		t.Fatalf("start: %+v %v", m, e)
	}
	now = now.Add(time.Minute)
	if again, _ := s.StartMilestone("amythest", "production deploy", "", ""); again.ID != m.ID {
		t.Fatal("starting an open milestone made another")
	}
	other, _ := s.StartMilestone("robot", "demo day", "", "")
	if open, _ := s.Milestones("amythest", true); len(open) != 1 || open[0].ID != m.ID {
		t.Fatalf("open on amythest: %+v", open)
	}
	if all, _ := s.Milestones("", false); len(all) != 2 {
		t.Fatalf("all: %d", len(all))
	}
	// Open, its window runs to now.
	if got, d, _, e := s.Collect(m.ID); e != nil || got.ID != m.ID || d.Projects == nil {
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
	next, _ := s.StartMilestone("amythest", "production deploy", "", "")
	if next.ID == m.ID {
		t.Fatal("reopened the ended milestone")
	}
	if _, e = s.Milestone("missing"); e == nil {
		t.Fatal("found a missing milestone")
	}
	if _, _, _, e = s.Collect(other.ID); e != nil {
		t.Fatal(e)
	}
}

func TestBackdatedMilestonesOverlapAndChange(t *testing.T) {
	s := testStore(t)
	s.now = func() time.Time { return time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC) }
	janus, e := s.StartMilestone("janus", "janus integration", "2026-10-01T00:00:00-05:00", "2026-11-01T00:00:00-05:00")
	if e != nil || janus.Open() || janus.Started != "2026-10-01T05:00:00.000000000Z" || janus.Ended != "2026-11-01T05:00:00.000000000Z" {
		t.Fatalf("backdated: %+v %v", janus, e)
	}
	// Past stretches overlap, and a finished one never stands in for an open one.
	prod, e := s.StartMilestone("janus", "production", "2026-10-30T00:00:00Z", "")
	if e != nil || !prod.Open() || prod.Started != "2026-10-30T00:00:00.000000000Z" {
		t.Fatalf("open from a past date: %+v %v", prod, e)
	}
	if again, _ := s.StartMilestone("janus", "production", "2026-10-01T00:00:00Z", ""); again.ID != prod.ID || again.Started != prod.Started {
		t.Fatalf("starting the open one again changed it: %+v", again)
	}
	if done, _ := s.StartMilestone("janus", "production", "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z"); done.ID == prod.ID {
		t.Fatal("a finished stretch returned the open one")
	}
	for _, bad := range [][2]string{{"2026-10-02T00:00:00Z", "2026-10-01T00:00:00Z"}, {"October", ""}, {"2026-10-01T00:00:00Z", "soon"}} {
		if _, e := s.StartMilestone("janus", "bad", bad[0], bad[1]); e == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	// Edits move dates, rename, reopen, and keep the end after the start.
	early, name, open := "2026-09-30T00:00:00Z", "janus rollout", ""
	m, e := s.UpdateMilestone(janus.ID, MilestoneChange{Started: &early, Name: &name})
	if e != nil || m.Started != "2026-09-30T00:00:00.000000000Z" || m.Name != name || m.Ended != janus.Ended {
		t.Fatalf("edit: %+v %v", m, e)
	}
	late := "2026-12-01T00:00:00Z"
	if _, e = s.UpdateMilestone(janus.ID, MilestoneChange{Started: &late}); e == nil {
		t.Fatal("moved the start past the end")
	}
	if m, e = s.UpdateMilestone(janus.ID, MilestoneChange{Ended: &open}); e != nil || !m.Open() {
		t.Fatalf("reopen: %+v %v", m, e)
	}
	if _, e = s.UpdateMilestone("missing", MilestoneChange{Name: &name}); e == nil {
		t.Fatal("edited a missing milestone")
	}
}

func TestMilestoneLinks(t *testing.T) {
	s := testStore(t)
	m, _ := s.StartMilestone("janus", "janus integration", "2026-10-01T00:00:00Z", "2026-11-01T00:00:00Z")
	if _, e := s.db.Exec(`INSERT INTO artifacts VALUES('a1','pr:https://github.com/acme/janus/pull/42','pr','https://github.com/acme/janus/pull/42','Add flags [MERGED]','acme/janus','','','2026-10-20T00:00:00.000000000Z')`); e != nil {
		t.Fatal(e)
	}
	for _, l := range [][2]string{{"message", "msg-1"}, {"message", "msg-1"}, {"pr", "https://github.com/acme/janus/pull/42"}, {"pr", "acme/janus#42"}, {"pr", "https://git.example.com/acme/janus/pull/7?x=1"}} {
		if _, e := s.LinkMilestone(m.ID, l[0], l[1], "owner"); e != nil {
			t.Fatalf("link %v: %v", l, e)
		}
	}
	links, e := s.MilestoneLinks(m.ID)
	if e != nil || len(links) != 3 {
		t.Fatalf("links: %+v %v", links, e)
	}
	refs := map[string]MilestoneLink{}
	for _, l := range links {
		refs[l.Ref] = l
	}
	if pr := refs["acme/janus#42"]; pr.Artifact == nil || pr.Artifact.Title != "Add flags [MERGED]" {
		t.Fatalf("pull request not resolved to its artifact: %+v", pr)
	}
	if _, ok := refs["https://git.example.com/acme/janus/pull/7"]; !ok || refs["msg-1"].AddedBy != "owner" {
		t.Fatalf("refs: %+v", refs)
	}
	for _, bad := range [][2]string{{"pr", "janus#1"}, {"pr", "http://github.com/a/b/pull/1"}, {"card", "x"}, {"message", ""}, {"message", "a b"}} {
		if _, e := s.LinkMilestone(m.ID, bad[0], bad[1], "owner"); e == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	if _, e := s.LinkMilestone("missing", "message", "msg-1", "owner"); e == nil {
		t.Fatal("linked to a missing milestone")
	}
	if e = s.UnlinkMilestone(m.ID, "pr", "https://github.com/acme/janus/pull/42"); e != nil {
		t.Fatal(e)
	}
	if e = s.UnlinkMilestone(m.ID, "pr", "acme/janus#42"); e != nil {
		t.Fatal("unlinking twice failed:", e)
	}
	if _, _, links, e = s.Collect(m.ID); e != nil || len(links) != 2 {
		t.Fatalf("collect links: %+v %v", links, e)
	}
}

func TestSchema3UpgradesToLinksKeepingMilestones(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DB: filepath.Join(dir, "work.db"), MediaDir: filepath.Join(dir, "media")}
	s, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	m, _ := s.StartMilestone("janus", "production", "", "")
	// Put the database back as schema 3 left it.
	if _, e = s.db.Exec(`DROP TABLE milestone_links; PRAGMA user_version=3`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if s, e = Open(cfg); e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if got, e := s.Milestone(m.ID); e != nil || got.Name != "production" {
		t.Fatalf("milestone lost: %+v %v", got, e)
	}
	if _, e = s.LinkMilestone(m.ID, "message", "msg-1", "owner"); e != nil {
		t.Fatal("no links after upgrade:", e)
	}
}
