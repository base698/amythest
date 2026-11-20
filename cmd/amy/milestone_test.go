package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/messages"
)

type fakeMessages struct {
	tagged []messages.Tagged
	byID   map[string]messages.Message
	convs  []messages.Listing
}

func (f fakeMessages) Tagged(context.Context, string, int) ([]messages.Tagged, error) {
	return f.tagged, nil
}
func (f fakeMessages) Message(_ context.Context, id string) (messages.Message, error) {
	if m, ok := f.byID[id]; ok {
		return m, nil
	}
	return messages.Message{}, errors.New("message not found")
}
func (f fakeMessages) Conversations(context.Context) ([]messages.Listing, error) { return f.convs, nil }

func day(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func TestCollectCountsMessagesBySendTimeAndMarksLinks(t *testing.T) {
	old := prLookup
	defer func() { prLookup = old }()
	prLookup = func(_ context.Context, url string) (string, string, string, error) {
		if url == "https://github.com/acme/janus/pull/42" {
			return "Add flags", "MERGED", "2026-10-20T15:00:00Z", nil
		}
		return "", "", "", errors.New("gh: not found")
	}
	chat := messages.Conversation{ID: "c1", Name: "#janus"}
	// Sent in October, put on the wall in November: it belongs to October.
	octSent := messages.Tagged{Tag: messages.Tag{Message: "m-oct", At: day("2026-11-19T00:00:00Z")},
		Message: messages.Message{ID: "m-oct", Body: "flags are live", At: day("2026-10-02T12:00:00Z")}, Conversation: chat}
	novSent := messages.Tagged{Tag: messages.Tag{Message: "m-nov", At: day("2026-11-19T00:00:00Z")},
		Message: messages.Message{ID: "m-nov", Body: "prod cutover", At: day("2026-11-12T12:00:00Z")}, Conversation: chat}
	src := fakeMessages{tagged: []messages.Tagged{octSent, novSent},
		byID:  map[string]messages.Message{"m-off": {ID: "m-off", Conversation: "c1", Body: "kickoff", At: day("2026-09-28T09:00:00Z")}},
		convs: []messages.Listing{{Conversation: chat}}}
	m := apiclient.Milestone{ID: "ms", Project: "janus", Name: "janus integration", Started: "2026-10-01T00:00:00Z", Ended: "2026-11-01T00:00:00Z"}
	snap := &apiclient.WorkSnapshot{Artifacts: []apiclient.WorkArtifact{
		{Kind: "pr", Ref: "https://github.com/acme/janus/pull/42", Title: "Add flags [OPEN]"},
		{Kind: "pr", Ref: "https://github.com/acme/janus/pull/43", Title: "Docs"}}}
	links := []apiclient.MilestoneLink{{Kind: "message", Ref: "m-off"}, {Kind: "message", Ref: "m-oct"}, {Kind: "message", Ref: "gone"},
		{Kind: "pr", Ref: "acme/janus#42"}, {Kind: "pr", Ref: "acme/janus#99"}}
	c := collection(context.Background(), m, snap, links, src)

	var got []string
	for _, x := range c.Messages {
		got = append(got, x.Message.ID+":"+strings.Join(x.Via, "+"))
	}
	if want := []string{"gone:linked", "m-off:linked", "m-oct:window+linked"}; !slices.Equal(got, want) {
		t.Fatalf("messages: %v, want %v", got, want)
	}
	if c.Messages[1].Conversation.Name != "#janus" || c.Messages[0].Error == "" || c.Messages[2].Tag == nil {
		t.Fatalf("message details: %+v", c.Messages)
	}
	got = nil
	for _, p := range c.PRs {
		got = append(got, p.Ref+":"+strings.Join(p.Via, "+")+":"+p.Title)
	}
	if want := []string{"acme/janus#42:window+linked:Add flags", "acme/janus#99:linked:", "acme/janus#43:window:Docs"}; !slices.Equal(got, want) {
		t.Fatalf("pull requests: %v, want %v", got, want)
	}
	if c.PRs[0].MergedAt == "" || c.PRs[0].Artifact == nil || c.PRs[1].Error == "" {
		t.Fatalf("pull request details: %+v", c.PRs)
	}
	var out bytes.Buffer
	if err := c.report(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Pull requests (3)", "acme/janus#42 Add flags [merged] merged", "## Messages (3)", "flags are live (linked)", "msg:gone ("} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("report lacks %q:\n%s", want, out.String())
		}
	}
	// The other milestone, open from Oct 30, holds the November message only.
	prod := apiclient.Milestone{ID: "p", Project: "janus", Name: "production", Started: "2026-10-30T00:00:00Z"}
	c = collection(context.Background(), prod, &apiclient.WorkSnapshot{}, nil, src)
	if len(c.Messages) != 1 || c.Messages[0].Message.ID != "m-nov" || c.Messages[0].Via[0] != "window" {
		t.Fatalf("production: %+v", c.Messages)
	}
}

func TestMilestoneDatesAndLinkRefs(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("CDT", -5*3600)
	defer func() { time.Local = old }()
	for _, c := range []struct {
		in   string
		end  bool
		want string
	}{{"2026-10-01", false, "2026-10-01T05:00:00Z"}, {"2026-10-31", true, "2026-11-01T05:00:00Z"},
		{"2026-10-01 14:30", true, "2026-10-01T19:30:00Z"}, {"2026-10-01T00:00:00Z", true, "2026-10-01T00:00:00Z"}, {"", false, ""}} {
		if got, err := milestoneDate(c.in, c.end); err != nil || got != c.want {
			t.Fatalf("%q: %q %v, want %q", c.in, got, err, c.want)
		}
	}
	if _, err := milestoneDate("October", false); err == nil {
		t.Fatal("accepted a bad date")
	}
	for in, want := range map[string]string{"msg:abc": "message abc", "pr:acme/janus#4": "pr acme/janus#4",
		"https://github.com/acme/janus/pull/4": "pr https://github.com/acme/janus/pull/4", "janus": " "} {
		k, r, _ := linkRef(in)
		if k+" "+r != want {
			t.Fatalf("%q: %q %q", in, k, r)
		}
	}
}
