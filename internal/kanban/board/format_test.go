package board

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// richBoard exercises every persisted field.
func richBoard() Board {
	at := time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)
	done := at.Add(48 * time.Hour)
	return Board{
		Version: 4, Name: "rich", DisplayName: "Rich Board",
		Description: "Line one.\nLine two.",
		Icon:        "🚀", Color: "#8b5cf6", SortOrder: 4, Pinned: true,
		Archived: false, FocusCardID: "card0001", DispatchEnabled: true,
		Cards: []Card{
			{
				ID: "card0002", Title: "Bare card", Status: Triage, Priority: P2,
				Labels: []string{}, Comments: []Comment{}, Attachments: []Attachment{},
				CreatedAt: at, UpdatedAt: at,
			},
			{
				ID: "card0001", Title: "Everything card", Status: InProgress, Priority: P1,
				Description: "Multi-line **markdown**.\n\n- bullet\n- [ ] task box",
				DueDate:     "2026-08-15", Milestone: "v2", Assignee: "ada", Agent: "claude/opus",
				Blocked: true, Labels: []string{"infra", "urgent"},
				Comments: []Comment{
					{ID: "cmt1", Author: "grace", Body: "Ship it\nsoon", CreatedAt: at},
				},
				Attachments: []Attachment{
					{ID: "att1", Filename: "spec.pdf", Size: 1234, ContentType: "application/pdf", CreatedAt: at},
				},
				Audit:     []AuditEntry{{Action: "moved-board", Actor: "ada", FromBoard: "old", ToBoard: "rich", CreatedAt: at}},
				CreatedAt: at, UpdatedAt: at.Add(time.Hour), DoneAt: &done,
			},
		},
	}
}

func TestYAMLRoundTripPreservesEveryField(t *testing.T) {
	original := richBoard()
	rendered := renderBoard(original, false, time.Now())
	parsed, err := parseBoard(rendered, "board.yaml")
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeBoard(parsed, "board.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, normalized) {
		t.Fatalf("round trip diverged:\noriginal:  %#v\nround-trip: %#v", original, normalized)
	}
}

// The same struct serializes to JSON (wire) and YAML (disk) with identical
// key names, so what you hand-edit is exactly what the API speaks.
func TestJSONAndYAMLShareKeyNames(t *testing.T) {
	b := richBoard()
	jsonRaw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	yamlRaw, err := yaml.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var fromJSON, fromYAML map[string]any
	if err := json.Unmarshal(jsonRaw, &fromJSON); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(yamlRaw, &fromYAML); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]any) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		return out
	}
	for _, key := range keys(fromYAML) {
		if _, ok := fromJSON[key]; !ok {
			t.Errorf("yaml key %q not present in json serialization", key)
		}
	}
	card := fromYAML["cards"].([]any)[0].(map[string]any)
	jsonCard := fromJSON["cards"].([]any)[0].(map[string]any)
	for key := range card {
		if _, ok := jsonCard[key]; !ok {
			t.Errorf("yaml card key %q not present in json card", key)
		}
	}
}

// The wire contract promises arrays, never null — even when the YAML file
// omits empty collections.
func TestOmittedCollectionsStayArraysOnTheWire(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "hand"), 0o755)
	handWritten := `
version: 3
name: hand
cards:
  - id: card9999
    title: Hand-written card
    status: backlog
    createdAt: 2026-08-01T10:30:00Z
    updatedAt: 2026-08-01T10:30:00Z
`
	os.WriteFile(filepath.Join(dir, "hand", "board.yaml"), []byte(handWritten), 0o644)
	loaded, err := NewStore(dir, fixedClock).Load("hand")
	if err != nil {
		t.Fatal(err)
	}
	card := loaded.Cards[0]
	if card.Labels == nil || card.Comments == nil || card.Attachments == nil {
		t.Fatalf("nil collections leaked from hand-written yaml: %#v", card)
	}
	if card.Priority != P2 {
		t.Fatalf("priority default = %q", card.Priority)
	}
	if loaded.DisplayName != "Hand" {
		t.Fatalf("display name default = %q", loaded.DisplayName)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"labels":[]`, `"comments":[]`, `"attachments":[]`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("wire json missing %s: %s", want, raw)
		}
	}
}

// installLegacyBoard copies the frozen pre-YAML fixtures into a board dir.
func installLegacyBoard(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for fixture, target := range map[string]string{
		"testdata/legacy-board.md": boardFile,
		"testdata/legacy-done.md":  doneFile,
	} {
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		// The fixtures were generated for a board named "fixture"; rename in
		// the payload so multiple boards can host them.
		data = []byte(strings.ReplaceAll(string(data), `"fixture"`, `"`+name+`"`))
		if err := os.WriteFile(filepath.Join(dir, target), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// canonicalOrder reorders cards to column order (the v4 file groups cards
// by status section, which canonicalizes cross-column array order).
func canonicalOrder(b Board) Board {
	rank := map[Status]int{}
	for i, status := range append(append([]Status{}, ActiveStatuses...), Done) {
		rank[status] = i
	}
	sort.SliceStable(b.Cards, func(i, j int) bool {
		return rank[b.Cards[i].Status] < rank[b.Cards[j].Status]
	})
	return b
}

// requireV4 asserts a board file has been rewritten in the v4 markdown
// format: frontmatter marker present, embedded-JSON markers gone.
func requireV4(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s not written by migration: %v", path, err)
	}
	text := string(raw)
	if !strings.Contains(text, "kanban: 4") {
		t.Fatalf("%s is not v4 markdown:\n%s", path, text)
	}
	if strings.Contains(text, "KANBAN_DATA_START") {
		t.Fatalf("%s still carries embedded JSON markers", path)
	}
}

func TestOpeningEmbeddedJSONBoardMigratesToMarkdown(t *testing.T) {
	root := t.TempDir()
	installLegacyBoard(t, root, "fixture")
	store := NewStore(root, fixedClock)

	before, err := store.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Cards) != 2 || before.Cards[0].ID != "card0001" || !before.DispatchEnabled {
		t.Fatalf("legacy data mangled: %#v", before)
	}
	if before.Cards[0].Comments[0].Body != "Ship it\nsoon" {
		t.Fatalf("comment body = %q", before.Cards[0].Comments[0].Body)
	}

	// The open rewrote both files as v4 markdown in place.
	requireV4(t, filepath.Join(root, "fixture", boardFile))
	requireV4(t, filepath.Join(root, "fixture", doneFile))

	// And the migrated board reads back equivalent data. The v2 renderer
	// flattened comment newlines on the readable side but the JSON kept
	// them; the parsed data must match exactly.
	after, err := store.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if before = canonicalOrder(before); !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed the data:\nbefore: %#v\nafter:  %#v", before, after)
	}
	archived, err := store.ListArchived("fixture", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0].ID != "card0003" {
		t.Fatalf("archive lost in migration: %#v", archived)
	}
}

func TestOpeningYAMLBoardMigratesToMarkdown(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for fixture, target := range map[string]string{
		"testdata/legacy-board.yaml": yamlBoardFile,
		"testdata/legacy-done.yaml":  yamlDoneFile,
	} {
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, target), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(root, fixedClock)
	before, err := store.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Cards) != 2 || before.Cards[0].Comments[0].ID != "cmt1" {
		t.Fatalf("yaml data mangled: %#v", before)
	}
	requireV4(t, filepath.Join(dir, boardFile))
	requireV4(t, filepath.Join(dir, doneFile))
	for _, name := range []string{yamlBoardFile, yamlDoneFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still present after migration", name)
		}
	}
	after, err := store.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if before = canonicalOrder(before); !reflect.DeepEqual(before, after) {
		t.Fatalf("yaml migration changed the data:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestMutatingEmbeddedJSONBoardMigratesToMarkdown(t *testing.T) {
	root := t.TempDir()
	installLegacyBoard(t, root, "fixture")
	store := NewStore(root, fixedClock)

	if _, err := store.CreateCardOnExistingBoard("fixture", CardInput{Title: "Fresh card", Status: Triage}); err != nil {
		t.Fatal(err)
	}
	requireV4(t, filepath.Join(root, "fixture", boardFile))
	loaded, err := store.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cards) != 3 {
		t.Fatalf("cards = %d, want legacy 2 + new 1", len(loaded.Cards))
	}
}

func TestListBoardsMigratesAndMixesFormats(t *testing.T) {
	root := t.TempDir()
	installLegacyBoard(t, root, "oldie")
	store := NewStore(root, fixedClock)
	if _, err := store.CreateBoard("newie"); err != nil {
		t.Fatal(err)
	}

	if got := store.BoardNames(); !reflect.DeepEqual(got, []string{"newie", "oldie"}) {
		t.Fatalf("board names = %v", got)
	}
	boards, err := store.ListBoards()
	if err != nil {
		t.Fatal(err)
	}
	if len(boards) != 2 {
		t.Fatalf("boards = %#v", boards)
	}
	// Listing opens every board, so it migrates the legacy one in place.
	requireV4(t, filepath.Join(root, "oldie", boardFile))
}

func TestCreateBoardRefusesToShadowLegacyBoard(t *testing.T) {
	root := t.TempDir()
	installLegacyBoard(t, root, "fixture")
	if _, err := NewStore(root, fixedClock).CreateBoard("fixture"); err != ErrBoardExists {
		t.Fatalf("err = %v, want ErrBoardExists", err)
	}
}

func TestPredecessorMarkersStillParse(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "netex")
	os.MkdirAll(dir, 0o755)
	payload := `{"version": 1, "name": "netex", "cards": [{"id": "k_old", "title": "Old", "status": "ready", "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"}]}`
	doc := "# Old note\n\n" + legacyDataStart + payload + legacyDataEnd + "\n"
	os.WriteFile(filepath.Join(dir, boardFile), []byte(doc), 0o644)
	os.WriteFile(filepath.Join(dir, doneFile), []byte(doc), 0o644)

	loaded, err := NewStore(root, fixedClock).Load("netex")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Pinned || loaded.Version != 4 || loaded.Cards[0].Priority != P2 {
		t.Fatalf("v1 normalization failed: %#v", loaded)
	}
}

// A crash-recovery journal can restore pre-migration bytes into the YAML
// path; the reader sniffs content instead of trusting the extension.
func TestReaderSniffsLegacyContentAtYAMLPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sniff")
	os.MkdirAll(dir, 0o755)
	payload := `{"version": 2, "name": "sniff", "cards": []}`
	doc := "# Note\n\n" + dataStart + payload + dataEnd + "\n"
	os.WriteFile(filepath.Join(dir, boardFile), []byte(doc), 0o644)

	loaded, err := readBoard(filepath.Join(dir, boardFile))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "sniff" {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestHandEditedYAMLSurvivesServerRewrite(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("hand"); err != nil {
		t.Fatal(err)
	}
	card, err := store.CreateCard("hand", CardInput{Title: "Original title", Status: Triage})
	if err != nil {
		t.Fatal(err)
	}

	// Hand-edit the file the way a human would: retitle the heading, move
	// the card to another column, write a description under it.
	path := filepath.Join(root, "hand", boardFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw),
		"\n# Triage\n\n## Original title ^"+card.ID+"\npriority:: p2\n",
		"\n# Triage\n\n# Ready\n\n## Edited by hand ^"+card.ID+"\npriority:: p2\n\nFirst line\nSecond line\n", 1)
	if edited == string(raw) {
		t.Fatalf("edit did not apply; file was:\n%s", raw)
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load("hand")
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Cards[0]
	if got.Title != "Edited by hand" || got.Status != Ready || got.Description != "First line\nSecond line" {
		t.Fatalf("hand edit lost: %#v", got)
	}

	// A server mutation rewrites the file without losing the hand edits.
	if _, err := store.UpdateCard("hand", card.ID, CardPatch{DueDate: strPtr("2026-09-30")}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load("hand")
	if err != nil {
		t.Fatal(err)
	}
	got = reloaded.Cards[0]
	if got.Title != "Edited by hand" || got.Description != "First line\nSecond line" || got.DueDate != "2026-09-30" {
		t.Fatalf("server rewrite lost hand edits: %#v", got)
	}
}

func TestMalformedYAMLReportsParseError(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "broken")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, boardFile), []byte("cards: [unclosed\n"), 0o644)
	if _, err := NewStore(root, fixedClock).Load("broken"); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("err = %v", err)
	}
}

func strPtr(s string) *string { return &s }

func TestHandAddedCardGetsStableIDAndTimestamps(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("hand"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "hand", boardFile)
	raw, _ := os.ReadFile(path)
	edited := strings.Replace(string(raw), "\n# Triage\n",
		"\n# Triage\n\n## Brand new card\nlabels:: #fresh\n\nWritten by a human.\n", 1)
	os.WriteFile(path, []byte(edited), 0o644)

	first, err := store.Load("hand")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Load("hand")
	if err != nil {
		t.Fatal(err)
	}
	card := first.Cards[0]
	if card.ID == "" || card.ID != second.Cards[0].ID {
		t.Fatalf("hand-added card id unstable: %q vs %q", card.ID, second.Cards[0].ID)
	}
	if card.Title != "Brand new card" || card.Labels[0] != "fresh" || card.Description != "Written by a human." {
		t.Fatalf("card = %#v", card)
	}
	// A server write persists the id into the file and stamps timestamps.
	if _, err := store.CreateCard("hand", CardInput{Title: "Server card", Status: Backlog}); err != nil {
		t.Fatal(err)
	}
	after, err := store.Load("hand")
	if err != nil {
		t.Fatal(err)
	}
	var persisted Card
	for _, c := range after.Cards {
		if c.Title == "Brand new card" {
			persisted = c
		}
	}
	if persisted.ID != card.ID {
		t.Fatalf("persisted id %q != first-read id %q", persisted.ID, card.ID)
	}
	if persisted.CreatedAt.IsZero() || persisted.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not stamped on write: %#v", persisted)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "^"+card.ID) {
		t.Fatalf("id not written into the file:\n%s", raw)
	}
}

func TestUnknownColumnHeadingIsAHelpfulError(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("hand"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "hand", boardFile)
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(raw), "# Triage", "# Doing", 1)), 0o644)
	_, err := store.Load("hand")
	if err == nil || !strings.Contains(err.Error(), `unknown column heading "Doing"`) ||
		!strings.Contains(err.Error(), "In progress") {
		t.Fatalf("err = %v", err)
	}
}

func TestHeadingsInsideDescriptionsAreDemotedNotParsed(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("hand"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCard("hand", CardInput{
		Title: "Card with headings", Status: Triage,
		Description: "# Big heading\n## Sub heading\nbody",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("hand")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cards) != 1 {
		t.Fatalf("description headings split into cards/columns: %#v", loaded.Cards)
	}
	if !strings.Contains(loaded.Cards[0].Description, "### Big heading") {
		t.Fatalf("description = %q", loaded.Cards[0].Description)
	}
}

func TestStatusTokenHeadingsAlsoAccepted(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tok")
	os.MkdirAll(dir, 0o755)
	doc := "---\nkanban: 4\nname: tok\n---\n\n# in_progress\n\n## Token column card ^k_tok1\n"
	os.WriteFile(filepath.Join(dir, boardFile), []byte(doc), 0o644)
	loaded, err := NewStore(root, fixedClock).Load("tok")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Cards[0].Status != InProgress {
		t.Fatalf("status = %q", loaded.Cards[0].Status)
	}
}

// The server's canonical rewrite must preserve comment content exactly
// (modulo trailing whitespace per line, which is trimmed once) — including
// multi-paragraph bodies with blank lines, which span bullet continuation
// lines in the markdown.
func TestServerRewritePreservesMultiParagraphComments(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("hand"); err != nil {
		t.Fatal(err)
	}
	card, err := store.CreateCard("hand", CardInput{Title: "Card", Status: Triage})
	if err != nil {
		t.Fatal(err)
	}
	body := "First paragraph line one.\nLine two with trailing spaces.   \n\nSecond paragraph after a blank line.\n\nThird — with **markdown**, links https://example.com and \"quotes\"."
	if _, err := store.AddComment("hand", card.ID, "ada", body); err != nil {
		t.Fatal(err)
	}
	// Force several rewrite cycles: each mutation parses the file and
	// re-renders it; the comment must survive every pass.
	for i := 0; i < 3; i++ {
		if _, err := store.UpdateCard("hand", card.ID, CardPatch{Title: strPtr(fmt.Sprintf("Card v%d", i))}); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := store.Load("hand")
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Cards[0].Comments
	if len(got) != 1 {
		t.Fatalf("comments = %#v", got)
	}
	want := "First paragraph line one.\nLine two with trailing spaces.\n\nSecond paragraph after a blank line.\n\nThird — with **markdown**, links https://example.com and \"quotes\"."
	if got[0].Body != want {
		t.Fatalf("comment body after rewrites:\ngot:  %q\nwant: %q", got[0].Body, want)
	}
	if got[0].Author != "ada" || got[0].ID == "" {
		t.Fatalf("comment identity lost: %#v", got[0])
	}
}
