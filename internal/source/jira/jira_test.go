package jira

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stubSource(t *testing.T) *Source {
	t.Helper()
	s := New(Config{Stub: true}, filepath.Join(t.TempDir(), "no-env"))
	s.now = func() time.Time { return time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC) }
	s.client = newStub(s.now)
	return s
}

func TestStubDueItemsSplitOverdueAndToday(t *testing.T) {
	s := stubSource(t)
	items, err := s.DueItems(context.Background(), "2026-08-24", false)
	if err != nil {
		t.Fatal(err)
	}
	// DEMO-101 (yesterday) and DEMO-205 (today); 212 is +3, 198 has no due.
	if len(items) != 2 || items[0].ID != "DEMO-101" || items[1].ID != "DEMO-205" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Kind != "issue" || items[0].Source != "jira" {
		t.Fatalf("item shape = %+v", items[0])
	}
	if !strings.Contains(items[0].URL, "/browse/DEMO-101") {
		t.Fatalf("url = %q", items[0].URL)
	}
}

func TestStubListCommentAndAgentContext(t *testing.T) {
	s := stubSource(t)
	items, err := s.List(context.Background())
	if err != nil || len(items) != 4 {
		t.Fatalf("list = %d err=%v", len(items), err)
	}
	var csv *itemAlias
	for i := range items {
		if items[i].ID == "DEMO-198" {
			csv = &itemAlias{items[i].ID, i}
		}
	}
	if csv == nil {
		t.Fatal("DEMO-198 missing")
	}
	if err := s.Comment(context.Background(), items[csv.idx], "checked the repro"); err != nil {
		t.Fatal(err)
	}
	items, _ = s.List(context.Background())
	subject, body, err := s.AgentContext(items[csv.idx])
	if err != nil || subject != "DEMO-198" {
		t.Fatalf("subject=%q err=%v", subject, err)
	}
	for _, want := range []string{"jira issue DEMO-198", "CSV export", "checked the repro"} {
		if !strings.Contains(body, want) {
			t.Fatalf("agent context missing %q:\n%s", want, body)
		}
	}
}

type itemAlias struct {
	id  string
	idx int
}

func TestHealthMatrix(t *testing.T) {
	env := filepath.Join(t.TempDir(), "env")
	stub := New(Config{Stub: true}, env)
	if h := stub.Health(context.Background()); h.State != "stubbed" {
		t.Fatalf("stub health = %+v", h)
	}
	real := New(Config{URL: "https://x.atlassian.net"}, env)
	if h := real.Health(context.Background()); h.State != "missing-credentials" {
		t.Fatalf("no-creds health = %+v", h)
	}
	os.WriteFile(env, []byte("JIRA_EMAIL=a@example.com\nJIRA_API_TOKEN=tok\n"), 0o600)
	real = New(Config{URL: "https://x.atlassian.net"}, env)
	if h := real.Health(context.Background()); h.State != "connected" {
		t.Fatalf("creds health = %+v", h)
	}
	// The unimplemented HTTP client errors clearly.
	if _, err := real.List(context.Background()); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not-implemented error, got %v", err)
	}
}

func TestLoadConfigParsesSourcesSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cli.yaml")

	if _, ok, err := LoadConfig(path); ok || err != nil {
		t.Fatalf("absent file: ok=%v err=%v", ok, err)
	}
	os.WriteFile(path, []byte("endpoint: http://x\n"), 0o600)
	if _, ok, _ := LoadConfig(path); ok {
		t.Fatal("no sources section should mean no jira")
	}
	os.WriteFile(path, []byte("endpoint: http://x\nsources:\n  jira:\n    url: https://co.atlassian.net\n    stub: true\n"), 0o600)
	cfg, ok, err := LoadConfig(path)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !cfg.Stub || cfg.URL != "https://co.atlassian.net" || cfg.JQL == "" {
		t.Fatalf("cfg = %+v", cfg)
	}
	os.WriteFile(path, []byte("sources: ["), 0o600)
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("malformed yaml should error")
	}
}
