package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
)

func fixtureNote() *apiclient.Note {
	return &apiclient.Note{
		Slug:  "Projects/Garden",
		Title: "Garden",
		Path:  "Projects/Garden.md",
		Markdown: "# Garden\n\nSee [[Watering Schedule]] and [[Projects/Compost|the compost note]].\n\n" +
			strings.Repeat("filler line\n", 5) + "Also [[Seed Inventory#spring]] here.\n",
	}
}

func TestNoteViewExtractsWikilinksWithAliasesAndFragments(t *testing.T) {
	v := newNoteView(nil, fixtureNote())
	if len(v.links) != 3 {
		t.Fatalf("links = %+v", v.links)
	}
	if v.links[0].target != "Watering Schedule" || v.links[0].label != "Watering Schedule" {
		t.Fatalf("link0 = %+v", v.links[0])
	}
	if v.links[1].target != "Projects/Compost" || v.links[1].label != "the compost note" {
		t.Fatalf("link1 = %+v", v.links[1])
	}
	if v.links[2].target != "Seed Inventory" {
		t.Fatalf("link2 = %+v", v.links[2])
	}
}

func TestNoteViewTabCyclesLinksAndEnterFollows(t *testing.T) {
	v := newNoteView(nil, fixtureNote())
	next, _ := v.Update(tea.KeyMsg{Type: tea.KeyTab})
	nv := next.(*noteView)
	if nv.linkAt != 0 {
		t.Fatalf("linkAt = %d", nv.linkAt)
	}
	nv.Update(tea.KeyMsg{Type: tea.KeyTab})
	if nv.linkAt != 1 {
		t.Fatalf("linkAt after 2 tabs = %d", nv.linkAt)
	}
	_, cmd := nv.Update(enterMsg())
	if cmd == nil || !nv.Busy() {
		t.Fatal("enter on a focused link must fetch the note")
	}
	out := nv.View(100, 30)
	if !strings.Contains(out, "link 2/3") {
		t.Fatalf("hint missing link position:\n%s", out)
	}
}

func TestNoteViewRendersLinksAndWraps(t *testing.T) {
	note := fixtureNote()
	note.Markdown = "one " + strings.Repeat("word ", 60) + "\n[[Target]]"
	v := newNoteView(nil, note)
	out := stripANSI(v.View(50, 40))
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, " tab links") {
			continue // hint bar is chrome, not note content
		}
		if lipgloss.Width(line) > 52 {
			t.Fatalf("unwrapped line: %q", line)
		}
	}
	if !strings.Contains(out, "[[Target]]") {
		t.Fatalf("link not rendered:\n%s", out)
	}
}

func TestAgentContextPromptFramesAndTruncates(t *testing.T) {
	note := fixtureNote()
	prompt := agentContextPrompt(note, "https://host/notes")
	if !strings.Contains(prompt, `note "Garden"`) || !strings.Contains(prompt, "https://host/notes/Projects/Garden") {
		t.Fatalf("prompt framing:\n%s", prompt[:120])
	}
	if !strings.Contains(prompt, "# Garden") {
		t.Fatal("note body missing")
	}
	note.Markdown = strings.Repeat("x", maxAgentContextBytes+100)
	long := agentContextPrompt(note, "https://host/notes")
	if len(long) > maxAgentContextBytes+300 || !strings.Contains(long, "[truncated") {
		t.Fatalf("truncation failed: len=%d", len(long))
	}
}

func TestNotesViewSearchFlow(t *testing.T) {
	v := newNotesView(nil)
	v.Init()
	if !v.Capturing() {
		t.Fatal("notes view should start in the search input")
	}
	for _, r := range "garden" {
		v.Update(keyMsg(string(r)))
	}
	next, cmd := v.Update(enterMsg())
	nv := next.(*notesView)
	if cmd == nil || !nv.Busy() {
		t.Fatal("enter must run the search")
	}
	next, _ = nv.Update(notesFoundMsg{query: "garden", results: []apiclient.SearchResult{
		{Slug: "Projects/Garden", Title: "Garden", Excerpt: "the <b>garden</b> &amp; beds"},
	}})
	nv = next.(*notesView)
	out := nv.View(100, 30)
	if !strings.Contains(out, "Garden") || !strings.Contains(out, "the garden & beds") {
		t.Fatalf("results render:\n%s", out)
	}
	_, cmd = nv.Update(enterMsg())
	if cmd == nil || !nv.Busy() {
		t.Fatal("enter on a result must open the note")
	}
}
