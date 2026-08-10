package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/herdr"
	"github.com/base698/amythest/internal/kanban/board"
)

func editFixtureCard() board.Card {
	return board.Card{
		ID: "c1", Title: "Weekend chores", Status: board.Ready, Priority: board.P2,
		DueDate: "2026-08-10", Labels: []string{"home"}, Blocked: false,
		Description: "Prep list\n\n- [ ] refill bird feeder",
	}
}

func TestCardEditRoundTripUnchangedIsEmptyPatch(t *testing.T) {
	card := editFixtureCard()
	patch, err := parseCardEdit(serializeCardEdit(card), card)
	if err != nil {
		t.Fatal(err)
	}
	if !emptyPatch(patch) {
		t.Fatalf("unchanged edit produced patch %+v", patch)
	}
}

func TestCardEditChangedFieldsProducePartialPatch(t *testing.T) {
	card := editFixtureCard()
	doc := serializeCardEdit(card)
	doc = strings.Replace(doc, "title: Weekend chores", "title: Weekday chores", 1)
	doc = strings.Replace(doc, "priority: p2", "priority: p0", 1)
	doc = strings.Replace(doc, "labels: home", "labels: home, garden", 1)
	doc += "\n- [ ] new item"
	patch, err := parseCardEdit(doc, card)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Title == nil || *patch.Title != "Weekday chores" {
		t.Fatalf("title patch = %v", patch.Title)
	}
	if patch.Priority == nil || *patch.Priority != board.P0 {
		t.Fatalf("priority patch = %v", patch.Priority)
	}
	if patch.Labels == nil || len(*patch.Labels) != 2 {
		t.Fatalf("labels patch = %v", patch.Labels)
	}
	if patch.Description == nil || !strings.Contains(*patch.Description, "new item") {
		t.Fatalf("description patch = %v", patch.Description)
	}
	// Untouched fields stay nil so they can't clobber concurrent updates.
	if patch.Status != nil || patch.DueDate != nil || patch.Blocked != nil {
		t.Fatalf("unexpected patches: %+v", patch)
	}
}

func TestCardEditRejectsBadValues(t *testing.T) {
	card := editFixtureCard()
	for _, mutate := range []func(string) string{
		func(d string) string { return strings.Replace(d, "status: ready", "status: doing", 1) },
		func(d string) string { return strings.Replace(d, "priority: p2", "priority: urgent", 1) },
		func(d string) string { return strings.Replace(d, "due: 2026-08-10", "due: tomorrow", 1) },
		func(d string) string { return strings.Replace(d, "title: Weekend chores", "title:", 1) },
		func(d string) string { return strings.Replace(d, "---", "===", 1) },
	} {
		if _, err := parseCardEdit(mutate(serializeCardEdit(card)), card); err == nil {
			t.Fatalf("expected error for mutation %q", mutate(serializeCardEdit(card))[:60])
		}
	}
}

func TestCardEditDescriptionMayContainSeparator(t *testing.T) {
	card := editFixtureCard()
	card.Description = "---\nrule first"
	patch, err := parseCardEdit(serializeCardEdit(card), card)
	if err != nil {
		t.Fatal(err)
	}
	if !emptyPatch(patch) {
		t.Fatalf("hr-leading description round trip produced %+v", patch)
	}
}

func TestWrapLineBreaksAtSpacesAndKeepsIndent(t *testing.T) {
	rows := wrapLine("- [ ] a rather long checklist item that certainly needs to wrap", 30)
	if len(rows) < 2 {
		t.Fatalf("rows = %v", rows)
	}
	for _, row := range rows {
		if len(row) > 30 {
			t.Fatalf("row too wide: %q", row)
		}
	}
	if !strings.HasPrefix(rows[1], "  ") {
		t.Fatalf("continuation not indented: %q", rows[1])
	}
	if got := strings.Join(strings.Fields(strings.Join(rows, " ")), " "); got != "- [ ] a rather long checklist item that certainly needs to wrap" {
		t.Fatalf("content lost: %q", got)
	}
}

func TestCardViewShowsCommentsAndWrapsLongLines(t *testing.T) {
	card := editFixtureCard()
	card.Description = "one " + strings.Repeat("verylongword ", 20)
	card.Comments = []board.Comment{{ID: "cm1", Author: "sam", Body: "first comment"}}
	v := newCardView(nil, "personal", card)
	out := v.View(60, 40)
	for _, line := range strings.Split(stripANSI(out), "\n") {
		if lipgloss.Width(line) > 62 {
			t.Fatalf("unwrapped line: %q", line)
		}
	}
	if !strings.Contains(out, "Comments (1)") || !strings.Contains(out, "first comment") {
		t.Fatalf("comments missing:\n%s", out)
	}
}

func TestCardContextPromptIncludesMetadataDescriptionAndComments(t *testing.T) {
	card := editFixtureCard()
	card.Blocked = true
	card.Comments = []board.Comment{{Author: "sam", Body: "check the feeder"}}
	prompt := cardContextPrompt(card, "personal", "https://host/notes")
	for _, want := range []string{
		`card "Weekend chores"`, "board personal", "https://host/notes/kanban/",
		"status: ready", "priority: p2", "due: 2026-08-10", "labels: home", "BLOCKED",
		"refill bird feeder", "Comments:", "sam", "check the feeder",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestCardViewAKeyOpensAgentPickerAndEscCloses(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	v := newCardView(client, "personal", editFixtureCard())
	_, cmd := v.Update(keyMsg("a"))
	if cmd == nil || !v.Busy() {
		t.Fatal("a must fetch the agent list")
	}
	v.Update(cardAgentsMsg{cardID: "c1", agents: []herdr.Agent{{PaneID: "w1:p1", Agent: "claude", Status: "idle", Title: "helper"}}})
	if !v.agents.active || !v.Capturing() {
		t.Fatal("agent picker should be open and capturing")
	}
	out := v.View(120, 40)
	if !strings.Contains(out, "Send to agent") || !strings.Contains(out, "claude [idle] helper") {
		t.Fatalf("picker render:\n%s", out)
	}
	v.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.agents.active {
		t.Fatal("esc must close the agent picker")
	}
	// Enter after reopening picks the agent and starts the send.
	v.busy = false
	v.Update(keyMsg("a"))
	v.Update(cardAgentsMsg{cardID: "c1", agents: []herdr.Agent{{PaneID: "w1:p1"}}})
	_, cmd = v.Update(enterMsg())
	if cmd == nil || !v.Busy() {
		t.Fatal("enter must start the send")
	}
}

func TestCardViewCommentKeyOpensPromptAndPosts(t *testing.T) {
	v := newCardView(nil, "personal", editFixtureCard())
	next, _ := v.Update(keyMsg("c"))
	cv := next.(*cardView)
	if !cv.Capturing() {
		t.Fatal("comment prompt should capture keys")
	}
	for _, r := range "lgtm" {
		cv.Update(keyMsg(string(r)))
	}
	_, cmd := cv.Update(enterMsg())
	if cmd == nil || !cv.Busy() {
		t.Fatal("enter must post the comment")
	}
}
