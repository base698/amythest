package board

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
		Version: 3, Name: "rich", DisplayName: "Rich Board",
		Description: "Line one.\nLine two.",
		Icon:        "🚀", Color: "#8b5cf6", SortOrder: 4, Pinned: true,
		Archived: false, FocusCardID: "card0001", DispatchEnabled: true,
		Cards: []Card{
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
			{
				ID: "card0002", Title: "Bare card", Status: Triage, Priority: P2,
				Labels: []string{}, Comments: []Comment{}, Attachments: []Attachment{},
				CreatedAt: at, UpdatedAt: at,
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
		"testdata/legacy-board.md": legacyBoardFile,
		"testdata/legacy-done.md":  legacyDoneFile,
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

func TestOpeningLegacyBoardMigratesToYAML(t *testing.T) {
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

	// The open migrated both files.
	for _, name := range []string{boardFile, doneFile} {
		if _, err := os.Stat(filepath.Join(root, "fixture", name)); err != nil {
			t.Fatalf("%s not written by migration: %v", name, err)
		}
	}
	for _, name := range []string{legacyBoardFile, legacyDoneFile} {
		if _, err := os.Stat(filepath.Join(root, "fixture", name)); !os.IsNotExist(err) {
			t.Fatalf("%s still present after migration", name)
		}
	}

	// And the migrated board reads back byte-for-byte equivalent data.
	after, err := store.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed the data:\nbefore: %#v\nafter:  %#v", before, after)
	}
	// Archived cards survived into done.yaml.
	archived, err := store.ListArchived("fixture", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0].ID != "card0003" {
		t.Fatalf("archive lost in migration: %#v", archived)
	}
}

func TestMutatingLegacyBoardMigratesToYAML(t *testing.T) {
	root := t.TempDir()
	installLegacyBoard(t, root, "fixture")
	store := NewStore(root, fixedClock)

	if _, err := store.CreateCardOnExistingBoard("fixture", CardInput{Title: "Fresh card", Status: Triage}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "fixture", boardFile)); err != nil {
		t.Fatalf("mutation did not write yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "fixture", legacyBoardFile)); !os.IsNotExist(err) {
		t.Fatal("legacy board.md still present after mutation")
	}
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
	// Listing opens every board, so it migrates the legacy one.
	if _, err := os.Stat(filepath.Join(root, "oldie", legacyBoardFile)); !os.IsNotExist(err) {
		t.Fatal("list did not migrate the legacy board")
	}
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
	os.WriteFile(filepath.Join(dir, legacyBoardFile), []byte(doc), 0o644)
	os.WriteFile(filepath.Join(dir, legacyDoneFile), []byte(doc), 0o644)

	loaded, err := NewStore(root, fixedClock).Load("netex")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Pinned || loaded.Version != 3 || loaded.Cards[0].Priority != P2 {
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

	// Hand-edit the file the way a human would: retitle, move column, add a
	// multi-line description.
	path := filepath.Join(root, "hand", boardFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "title: Original title", "title: Edited by hand\n      description: |-\n        First line\n        Second line", 1)
	edited = strings.Replace(edited, "status: triage", "status: ready", 1)
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
