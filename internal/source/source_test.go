package source

import (
	"context"
	"errors"
	"testing"
)

type fakeSource struct {
	name  string
	items []Item
	err   error
}

func (f *fakeSource) Name() string                            { return f.name }
func (f *fakeSource) Health(context.Context) Health           { return Health{State: StateOK} }
func (f *fakeSource) AgentContext(Item) (string, string, error) { return "", "", nil }
func (f *fakeSource) DueItems(context.Context, string, bool) ([]Item, error) {
	return f.items, f.err
}

func TestRegistryMergesInOrderAndIsolatesFailures(t *testing.T) {
	a := &fakeSource{name: "amythest", items: []Item{{Source: "amythest", ID: "t1"}}}
	b := &fakeSource{name: "jira", err: errors.New("boom")}
	c := &fakeSource{name: "azdo", items: []Item{{Source: "azdo", ID: "w1"}}}
	reg := NewRegistry(a, b, c)

	items, errs := reg.DueItems(context.Background(), "2026-08-24", false)
	if len(items) != 2 || items[0].ID != "t1" || items[1].ID != "w1" {
		t.Fatalf("items = %+v", items)
	}
	if len(errs) != 1 || errs["jira"] == nil {
		t.Fatalf("errs = %v", errs)
	}
	if s, ok := reg.Get("jira"); !ok || s.Name() != "jira" {
		t.Fatal("Get(jira) failed")
	}
	if _, ok := reg.Get("linear"); ok {
		t.Fatal("Get(linear) should miss")
	}
}

func TestSectionForCoversAllStates(t *testing.T) {
	day := "2026-08-24"
	cases := []struct {
		it   Item
		want string
	}{
		{Item{Due: "2026-08-20"}, "Overdue"},
		{Item{Due: day}, "Due today"},
		{Item{Due: ""}, "Due today"},
		{Item{Due: "2026-08-20", Focused: true}, "Focus"},
		{Item{Due: day, Done: true}, "Done today"},
		{Item{Focused: true, Done: true}, "Done today"},
	}
	for _, c := range cases {
		if got := SectionFor(c.it, day); got != c.want {
			t.Fatalf("SectionFor(%+v) = %q, want %q", c.it, got, c.want)
		}
	}
}
