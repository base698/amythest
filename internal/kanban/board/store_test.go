package board

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedClock() time.Time { return time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC) }

func TestCreateBoardCreatesNoteBackedBoardAndRejectsDuplicateOrInvalidNames(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)

	created, err := store.CreateBoard("new-project")
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "new-project" || created.Version != 1 || created.DispatchEnabled || len(created.Cards) != 0 {
		t.Fatalf("created board = %#v", created)
	}
	for _, name := range []string{"board.md", "done.md"} {
		payload, err := os.ReadFile(filepath.Join(root, "new-project", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(payload), "dispatch: false") || !strings.Contains(string(payload), `"name": "new-project"`) {
			t.Fatalf("%s does not follow board note conventions:\n%s", name, payload)
		}
	}
	if _, err := store.CreateBoard("new-project"); !errors.Is(err, ErrBoardExists) {
		t.Fatalf("duplicate create error = %v, want ErrBoardExists", err)
	}
	if _, err := store.CreateBoard("../escape"); err == nil {
		t.Fatal("invalid board name was accepted")
	}
}

func TestCardDueDatePersistsInStructuredAndReadableBoardAndCanBeCleared(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("personal"); err != nil {
		t.Fatal(err)
	}

	card, err := store.CreateCard("personal", CardInput{
		Title:   "Draft LinkedIn post",
		Status:  Backlog,
		DueDate: "2026-08-04",
	})
	if err != nil {
		t.Fatal(err)
	}
	if card.DueDate != "2026-08-04" {
		t.Fatalf("created due date = %q", card.DueDate)
	}
	loaded, err := store.Load("personal")
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Cards[0].DueDate; got != "2026-08-04" {
		t.Fatalf("loaded due date = %q", got)
	}
	raw, err := os.ReadFile(filepath.Join(root, "personal", "board.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "- **Due:** 2026-08-04") {
		t.Fatalf("readable board missing due date:\n%s", raw)
	}

	cleared := ""
	updated, err := store.UpdateCard("personal", card.ID, CardPatch{DueDate: &cleared})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DueDate != "" {
		t.Fatalf("cleared due date = %q", updated.DueDate)
	}
	for _, invalid := range []string{"2026-8-4", "2026-02-30", "next Tuesday"} {
		if _, err := store.CreateCard("personal", CardInput{Title: "Invalid due date", Status: Backlog, DueDate: invalid}); err == nil {
			t.Fatalf("invalid due date %q was accepted", invalid)
		}
	}
}

func TestDispatchSettingDefaultsFalseAndSurvivesCardAndArchiveWrites(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("proof"); err != nil {
		t.Fatal(err)
	}
	value, err := store.Load("proof")
	if err != nil {
		t.Fatal(err)
	}
	if value.DispatchEnabled {
		t.Fatal("new board dispatch must default false")
	}
	if _, err := store.UpdateBoardSettings("proof", true); err != nil {
		t.Fatal(err)
	}
	card, err := store.CreateCard("proof", CardInput{Title: "Dispatch me", Status: Ready, Assignee: "Hermes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateCard("proof", card.ID, CardPatch{Status: ptrStatus(Done)}); err != nil {
		t.Fatal(err)
	}
	value, err = store.Load("proof")
	if err != nil {
		t.Fatal(err)
	}
	if !value.DispatchEnabled {
		t.Fatal("card/archive write lost dispatch setting")
	}
	raw, err := os.ReadFile(filepath.Join(root, "proof", "board.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "dispatch: true") {
		t.Fatal("readable frontmatter missing dispatch setting")
	}
}

func TestStoreCreatesReadableBoardAndArchivesDoneCard(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("proof"); err != nil {
		t.Fatal(err)
	}

	card, err := store.CreateCard("proof", CardInput{
		Title:       "Deploy Proof production with login",
		Description: "Deploy the private production service with authentication.",
		Status:      Ready,
		Assignee:    "Justin",
		Labels:      []string{"proof", "deployment", "1.0"},
	})
	if err != nil {
		t.Fatal(err)
	}

	active, err := store.Load("proof")
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Cards) != 1 || active.Cards[0].ID != card.ID {
		t.Fatalf("active cards = %#v", active.Cards)
	}

	boardMD, err := os.ReadFile(filepath.Join(root, "proof", "board.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(boardMD)
	for _, want := range []string{"# Proof Kanban", "## Ready", "Deploy Proof production with login", "`proof`", "**Assignee:** Justin"} {
		if !strings.Contains(text, want) {
			t.Errorf("board.md missing %q", want)
		}
	}

	done, err := store.UpdateCard("proof", card.ID, CardPatch{Status: ptrStatus(Done)})
	if err != nil {
		t.Fatal(err)
	}
	if done.DoneAt == nil {
		t.Fatal("done timestamp was not recorded")
	}

	active, err = store.Load("proof")
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Cards) != 0 {
		t.Fatalf("done card remained active: %#v", active.Cards)
	}

	doneMD, err := os.ReadFile(filepath.Join(root, "proof", "done.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doneMD), card.ID) || !strings.Contains(string(doneMD), "Deploy Proof production with login") {
		t.Fatalf("done.md missing archived card:\n%s", doneMD)
	}
}

func TestMoveCardInsertsBeforeCardAndPersistsOrder(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("operations"); err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateCard("operations", CardInput{Title: "First", Status: Ready})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateCard("operations", CardInput{Title: "Second", Status: Ready})
	if err != nil {
		t.Fatal(err)
	}
	third, err := store.CreateCard("operations", CardInput{Title: "Third", Status: Ready})
	if err != nil {
		t.Fatal(err)
	}

	moved, err := store.MoveCard("operations", third.ID, Ready, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.ID != third.ID || moved.Status != Ready {
		t.Fatalf("moved card = %#v", moved)
	}
	renamed := "Third renamed"
	if _, err := store.UpdateCard("operations", third.ID, CardPatch{Title: &renamed}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("operations")
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{loaded.Cards[0].ID, loaded.Cards[1].ID, loaded.Cards[2].ID}; got[0] != first.ID || got[1] != third.ID || got[2] != second.ID {
		t.Fatalf("persisted order = %#v", got)
	}
	payload, err := os.ReadFile(filepath.Join(root, "operations", "board.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if !(strings.Index(text, "### First") < strings.Index(text, "### Third") && strings.Index(text, "### Third") < strings.Index(text, "### Second")) {
		t.Fatalf("readable board did not preserve order:\n%s", text)
	}
}

func TestMoveCardAcrossColumnsInsertsBeforeOrAppendsAndOrdinaryEditsPreserveOrder(t *testing.T) {
	store := NewStore(t.TempDir(), fixedClock)
	if err := store.EnsureBoard("operations"); err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateCard("operations", CardInput{Title: "First ready", Status: Ready})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateCard("operations", CardInput{Title: "Second ready", Status: Ready})
	if err != nil {
		t.Fatal(err)
	}
	backlog, err := store.CreateCard("operations", CardInput{Title: "Existing backlog", Status: Backlog})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.MoveCard("operations", first.ID, Backlog, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MoveCard("operations", second.ID, Backlog, backlog.ID); err != nil {
		t.Fatal(err)
	}
	newTitle := "Renamed moved card"
	if _, err := store.UpdateCard("operations", second.ID, CardPatch{Title: &newTitle}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("operations")
	if err != nil {
		t.Fatal(err)
	}
	if got := cardsWithStatus(loaded.Cards, Backlog); len(got) != 3 || got[0] != second.ID || got[1] != backlog.ID || got[2] != first.ID {
		t.Fatalf("backlog order after before-card and append moves = %#v", got)
	}
}

func TestMoveCardArchivesWhenStatusIsDone(t *testing.T) {
	store := NewStore(t.TempDir(), fixedClock)
	if err := store.EnsureBoard("operations"); err != nil {
		t.Fatal(err)
	}
	card, err := store.CreateCard("operations", CardInput{Title: "Archive from selector", Status: Verify})
	if err != nil {
		t.Fatal(err)
	}

	moved, err := store.MoveCard("operations", card.ID, Done, "")
	if err != nil {
		t.Fatal(err)
	}
	if moved.Status != Done || moved.DoneAt == nil {
		t.Fatalf("moved card = %#v", moved)
	}
	active, err := store.Load("operations")
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Cards) != 0 {
		t.Fatalf("archived card remained active: %#v", active.Cards)
	}
	archived, err := store.ListArchived("operations", "Archive from selector", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0].ID != card.ID {
		t.Fatalf("archive = %#v", archived)
	}
}

func TestMoveCardToBoardPreservesCardDataAttachmentsAndRecordsAudit(t *testing.T) {
	root := t.TempDir()
	current := fixedClock()
	store := NewStore(root, func() time.Time { return current })
	for _, name := range []string{"source", "destination"} {
		if err := store.EnsureBoard(name); err != nil {
			t.Fatal(err)
		}
	}
	card, err := store.CreateCard("source", CardInput{
		Title: "Keep everything", Description: "Full description", Status: Ready,
		Assignee: "Justin", Labels: []string{"important", "move"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddComment("source", card.ID, "Justin", "Preserve this comment"); err != nil {
		t.Fatal(err)
	}
	payload := []byte("attachment contents")
	attachment, err := store.AddAttachment("source", card.ID, "proof.txt", "text/plain; charset=utf-8", bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Load("source")
	if err != nil {
		t.Fatal(err)
	}
	original := before.Cards[0]
	current = current.Add(time.Hour)

	moved, err := store.MoveCardToBoard("source", "destination", card.ID, "Justin")
	if err != nil {
		t.Fatal(err)
	}
	if moved.ID != original.ID || moved.Title != original.Title || moved.Description != original.Description ||
		moved.Status != original.Status || moved.Assignee != original.Assignee ||
		!moved.CreatedAt.Equal(original.CreatedAt) || !moved.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("moved card lost identity/content/timestamps:\noriginal=%#v\nmoved=%#v", original, moved)
	}
	if len(moved.Labels) != 2 || len(moved.Comments) != 1 || len(moved.Attachments) != 1 || moved.Attachments[0].ID != attachment.ID {
		t.Fatalf("moved card lost related data: %#v", moved)
	}
	if len(moved.Audit) != 1 || moved.Audit[0].Action != "moved_board" || moved.Audit[0].Actor != "Justin" ||
		moved.Audit[0].FromBoard != "source" || moved.Audit[0].ToBoard != "destination" {
		t.Fatalf("move audit = %#v", moved.Audit)
	}
	source, err := store.Load("source")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := store.Load("destination")
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Cards) != 0 || len(destination.Cards) != 1 || destination.Cards[0].ID != card.ID {
		t.Fatalf("source=%#v destination=%#v", source.Cards, destination.Cards)
	}
	file, found, err := store.OpenAttachment("destination", card.ID, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != attachment.ID || !bytes.Equal(got, payload) {
		t.Fatalf("moved attachment = %#v %q", found, got)
	}
}

func TestMoveCardToBoardRollsBackBothBoardsAndAttachmentsWhenSourceWriteFails(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	for _, name := range []string{"source", "destination"} {
		if err := store.EnsureBoard(name); err != nil {
			t.Fatal(err)
		}
	}
	card, err := store.CreateCard("source", CardInput{Title: "Rollback me", Status: Backlog})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("rollback attachment")
	attachment, err := store.AddAttachment("source", card.ID, "rollback.txt", "text/plain", bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	store.writeFile = func(path string, data []byte) error {
		writes++
		if writes == 2 {
			return errors.New("injected source write failure")
		}
		return atomicWrite(path, data)
	}
	if _, err := store.MoveCardToBoard("source", "destination", card.ID, "Justin"); err == nil {
		t.Fatal("move unexpectedly succeeded")
	}
	store.writeFile = atomicWrite
	source, err := store.Load("source")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := store.Load("destination")
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Cards) != 1 || source.Cards[0].ID != card.ID || len(destination.Cards) != 0 {
		t.Fatalf("rollback source=%#v destination=%#v", source.Cards, destination.Cards)
	}
	file, _, err := store.OpenAttachment("source", card.ID, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}

func cardsWithStatus(cards []Card, status Status) []string {
	ids := []string{}
	for _, card := range cards {
		if card.Status == status {
			ids = append(ids, card.ID)
		}
	}
	return ids
}

func TestStorePersistsCommentsLabelsAndValidatesInput(t *testing.T) {
	store := NewStore(t.TempDir(), fixedClock)
	if err := store.EnsureBoard("operations"); err != nil {
		t.Fatal(err)
	}
	card, err := store.CreateCard("operations", CardInput{Title: "Observe processes", Status: Backlog})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := store.UpdateCard("operations", card.ID, CardPatch{Labels: &[]string{"prometheus", "operations"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Labels) != 2 {
		t.Fatalf("labels = %#v", updated.Labels)
	}

	updated, err = store.AddComment("operations", card.ID, "Hermes", "Compare process-exporter with node_exporter textfile collectors.")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Comments) != 1 || updated.Comments[0].Author != "Hermes" {
		t.Fatalf("comments = %#v", updated.Comments)
	}

	if _, err := store.CreateCard("../escape", CardInput{Title: "bad", Status: Ready}); err == nil {
		t.Fatal("expected invalid board name error")
	}
	if _, err := store.CreateCard("operations", CardInput{Title: "", Status: Ready}); err == nil {
		t.Fatal("expected empty title error")
	}
}

func TestLoadRecoversInterruptedDoneArchiveFromJournal(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("proof"); err != nil {
		t.Fatal(err)
	}
	card, err := store.CreateCard("proof", CardInput{Title: "Recover me", Status: Ready})
	if err != nil {
		t.Fatal(err)
	}
	active, err := readBoard(store.boardPath("proof"))
	if err != nil {
		t.Fatal(err)
	}
	done, err := readBoard(store.donePath("proof"))
	if err != nil {
		t.Fatal(err)
	}
	doneCard := active.Cards[0]
	doneCard.Status = Done
	doneAt := fixedClock()
	doneCard.DoneAt = &doneAt
	done.Cards = append([]Card{doneCard}, done.Cards...)
	active.Cards = active.Cards[:0]
	journal := archiveJournal{BoardImage: renderBoard(active, false, fixedClock()), DoneImage: renderBoard(done, true, fixedClock())}
	raw, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.journalPath("proof"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(store.donePath("proof"), journal.DoneImage); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Load("proof")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Cards) != 0 {
		t.Fatalf("active board was not recovered: %#v", recovered.Cards)
	}
	archived, err := readBoard(store.donePath("proof"))
	if err != nil {
		t.Fatal(err)
	}
	if cardIndex(archived.Cards, card.ID) < 0 {
		t.Fatalf("done archive missing %s: %#v", card.ID, archived.Cards)
	}
	if _, err := os.Stat(store.journalPath("proof")); !os.IsNotExist(err) {
		t.Fatalf("journal was not removed after recovery: %v", err)
	}
}

func TestRestoreCardMovesDoneCardBackToActiveBoard(t *testing.T) {
	store := NewStore(t.TempDir(), fixedClock)
	if err := store.EnsureBoard("operations"); err != nil {
		t.Fatal(err)
	}
	card, err := store.CreateCard("operations", CardInput{Title: "Update Costco address", Status: Triage, Assignee: "Justin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateCard("operations", card.ID, CardPatch{Status: ptrStatus(Done)}); err != nil {
		t.Fatal(err)
	}
	restored, err := store.RestoreCard("operations", card.ID, Triage)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != Triage || restored.DoneAt != nil || restored.Assignee != "Justin" {
		t.Fatalf("restored card = %#v", restored)
	}
	active, err := store.Load("operations")
	if err != nil || cardIndex(active.Cards, card.ID) < 0 {
		t.Fatalf("active board missing restored card: %#v err=%v", active.Cards, err)
	}
	archived, err := readBoard(store.donePath("operations"))
	if err != nil || cardIndex(archived.Cards, card.ID) >= 0 {
		t.Fatalf("done archive retained restored card: %#v err=%v", archived.Cards, err)
	}
}

func TestDeleteCardRemovesOnlyRequestedActiveCard(t *testing.T) {
	store := NewStore(t.TempDir(), fixedClock)
	if err := store.EnsureBoard("operations"); err != nil {
		t.Fatal(err)
	}
	remove, err := store.CreateCard("operations", CardInput{Title: "Misplaced card", Status: Ready})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := store.CreateCard("operations", CardInput{Title: "Keep card", Status: Backlog})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteCard("operations", remove.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.ID != remove.ID {
		t.Fatalf("deleted id = %q, want %q", deleted.ID, remove.ID)
	}
	value, err := store.Load("operations")
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Cards) != 1 || value.Cards[0].ID != keep.ID {
		t.Fatalf("remaining cards = %#v", value.Cards)
	}
	if _, err := store.DeleteCard("operations", remove.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second delete error = %v, want os.ErrNotExist", err)
	}
	payload, err := os.ReadFile(filepath.Join(store.root, "operations", "board.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), remove.ID) || strings.Contains(string(payload), remove.Title) {
		t.Fatal("deleted card remains in canonical board Markdown")
	}
}

func TestListArchivedFuzzyMatchesAndSortsNewestFirst(t *testing.T) {
	now := fixedClock()
	store := NewStore(t.TempDir(), func() time.Time { return now })
	if err := store.EnsureBoard("operations"); err != nil {
		t.Fatal(err)
	}
	family, err := store.CreateCard("operations", CardInput{Title: "Family Photo Builder", Description: "movie catalog", Status: Ready})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateCard("operations", family.ID, CardPatch{Status: ptrStatus(Done)}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	prometheus, err := store.CreateCard("operations", CardInput{Title: "Prometheus exporter", Status: Ready})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateCard("operations", prometheus.ID, CardPatch{Status: ptrStatus(Done)}); err != nil {
		t.Fatal(err)
	}
	all, err := store.ListArchived("operations", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != prometheus.ID || all[1].ID != family.ID {
		t.Fatalf("archive order = %#v", all)
	}
	if all[0].DoneAt == nil || all[0].CreatedAt.IsZero() || all[0].UpdatedAt.IsZero() {
		t.Fatalf("archive timestamps missing: %#v", all[0])
	}
	matched, err := store.ListArchived("operations", "fam pho", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].ID != family.ID {
		t.Fatalf("fuzzy archive match = %#v", matched)
	}
	empty, err := store.ListArchived("operations", "does not exist", 100)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(empty)
	if err != nil || string(raw) != "[]" {
		t.Fatalf("empty archive JSON = %s err=%v", raw, err)
	}
}

func TestAddAttachmentStoresFileAndPersistsMetadataOnCard(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	if err := store.EnsureBoard("operations"); err != nil {
		t.Fatal(err)
	}
	card, err := store.CreateCard("operations", CardInput{Title: "Attach evidence", Status: Ready})
	if err != nil {
		t.Fatal(err)
	}

	attachment, err := store.AddAttachment("operations", card.ID, "../Quarterly report.pdf", "application/pdf", bytes.NewReader([]byte("report bytes")), int64(len("report bytes")))
	if err != nil {
		t.Fatal(err)
	}
	if attachment.ID == "" || attachment.Filename != "Quarterly report.pdf" || attachment.Size != int64(len("report bytes")) {
		t.Fatalf("attachment = %#v", attachment)
	}
	loaded, err := store.Load("operations")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cards[0].Attachments) != 1 || loaded.Cards[0].Attachments[0] != attachment {
		t.Fatalf("persisted attachments = %#v", loaded.Cards[0].Attachments)
	}
	file, metadata, err := store.OpenAttachment("operations", card.ID, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if metadata != attachment {
		t.Fatalf("opened metadata = %#v", metadata)
	}
	got := make([]byte, len("report bytes"))
	if _, err := file.Read(got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "report bytes" {
		t.Fatalf("file contents = %q", got)
	}
}

func TestListAndDeleteAttachmentUpdateMetadataAndRemoveOnlyRequestedFile(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	_ = store.EnsureBoard("operations")
	card, _ := store.CreateCard("operations", CardInput{Title: "Evidence", Status: Ready})
	first, err := store.AddAttachment("operations", card.ID, "one.txt", "text/plain; charset=utf-8", bytes.NewReader([]byte("one")), 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddAttachment("operations", card.ID, "two.txt", "text/plain; charset=utf-8", bytes.NewReader([]byte("two")), 3)
	if err != nil {
		t.Fatal(err)
	}

	listed, err := store.ListAttachments("operations", card.ID)
	if err != nil || len(listed) != 2 || listed[0].ID != first.ID || listed[1].ID != second.ID {
		t.Fatalf("listed = %#v err=%v", listed, err)
	}
	if err := store.DeleteAttachment("operations", card.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	listed, err = store.ListAttachments("operations", card.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != second.ID {
		t.Fatalf("after delete = %#v err=%v", listed, err)
	}
	if _, _, err := store.OpenAttachment("operations", card.ID, first.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted attachment open error = %v", err)
	}
	file, _, err := store.OpenAttachment("operations", card.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
}

func TestAddAttachmentCleansUpFileWhenAtomicMetadataWriteFails(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	_ = store.EnsureBoard("operations")
	card, _ := store.CreateCard("operations", CardInput{Title: "Evidence", Status: Ready})
	store.writeFile = func(string, []byte) error { return errors.New("simulated metadata failure") }

	if _, err := store.AddAttachment("operations", card.ID, "proof.txt", "text/plain", bytes.NewReader([]byte("proof")), 5); err == nil {
		t.Fatal("upload succeeded despite metadata write failure")
	}
	entries, err := os.ReadDir(filepath.Join(root, "operations", "attachments", card.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("orphaned files = %#v", entries)
	}
	persisted, err := readBoard(store.boardPath("operations"))
	if err != nil || len(persisted.Cards[0].Attachments) != 0 {
		t.Fatalf("metadata changed = %#v err=%v", persisted.Cards[0].Attachments, err)
	}
}

func TestAttachmentLimitsAndSymlinkConfinement(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	_ = store.EnsureBoard("operations")
	card, _ := store.CreateCard("operations", CardInput{Title: "Evidence", Status: Ready})
	if _, err := store.AddAttachment("operations", card.ID, "huge.bin", "application/octet-stream", bytes.NewReader(nil), MaxAttachmentSize+1); err == nil {
		t.Fatal("oversized attachment was accepted")
	}
	for i := 0; i < MaxAttachmentsPerCard; i++ {
		name := fmt.Sprintf("%d.txt", i)
		if _, err := store.AddAttachment("operations", card.ID, name, "text/plain", bytes.NewReader([]byte("x")), 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AddAttachment("operations", card.ID, "extra.txt", "text/plain", bytes.NewReader([]byte("x")), 1); err == nil {
		t.Fatal("attachment count limit was not enforced")
	}

	other, _ := store.CreateCard("operations", CardInput{Title: "Unsafe", Status: Ready})
	attachmentsRoot := filepath.Join(root, "operations", "attachments")
	if err := os.Symlink(t.TempDir(), filepath.Join(attachmentsRoot, other.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddAttachment("operations", other.ID, "escape.txt", "text/plain", bytes.NewReader([]byte("x")), 1); err == nil {
		t.Fatal("symlink attachment directory was accepted")
	}
}

func TestDeleteCardRemovesItsAttachmentDirectory(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedClock)
	_ = store.EnsureBoard("operations")
	card, _ := store.CreateCard("operations", CardInput{Title: "Evidence", Status: Ready})
	if _, err := store.AddAttachment("operations", card.ID, "proof.txt", "text/plain", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteCard("operations", card.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "operations", "attachments", card.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attachment directory remains after card deletion: %v", err)
	}
}

func ptrStatus(s Status) *Status { return &s }

func TestBlockedFlagPersistsAndClearsThroughTheStore(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, func() time.Time { return time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC) })
	if err := store.EnsureBoard("proof"); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateCard("proof", CardInput{Title: "Waiting on review", Status: Ready, Blocked: true})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Blocked {
		t.Fatal("blocked was not applied at creation")
	}
	reloaded, err := store.Load("proof")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Cards[0].Blocked {
		t.Fatal("blocked did not survive a reload")
	}
	// The human-readable half of the note should say so too.
	raw, err := os.ReadFile(filepath.Join(root, "proof", "board.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "**Blocked:** yes") {
		t.Fatalf("board.md does not record the blocked flag:\n%s", raw)
	}

	unblocked := false
	patched, err := store.UpdateCard("proof", created.ID, CardPatch{Blocked: &unblocked})
	if err != nil {
		t.Fatal(err)
	}
	if patched.Blocked {
		t.Fatal("blocked was not cleared")
	}
	// A patch that does not mention blocked must leave it alone.
	title := "Still waiting"
	blockAgain := true
	if _, err := store.UpdateCard("proof", created.ID, CardPatch{Blocked: &blockAgain}); err != nil {
		t.Fatal(err)
	}
	after, err := store.UpdateCard("proof", created.ID, CardPatch{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if !after.Blocked {
		t.Fatal("an unrelated patch cleared the blocked flag")
	}
	raw, err = os.ReadFile(filepath.Join(root, "proof", "board.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "**Blocked:** yes") != 1 {
		t.Fatalf("blocked flag rendered %d times", strings.Count(string(raw), "**Blocked:** yes"))
	}
}
