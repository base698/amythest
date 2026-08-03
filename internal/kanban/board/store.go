package board

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	dataStart = "<!-- AMYTHEST_KANBAN_DATA_START -->\n```json\n"
	dataEnd   = "\n```\n<!-- AMYTHEST_KANBAN_DATA_END -->"
	// Read-only compatibility with boards written by the predecessor
	// server; rewritten to the current markers on the next mutation.
	legacyDataStart             = "<!-- NETEXPLORE_KANBAN_DATA_START -->\n```json\n"
	legacyDataEnd               = "\n```\n<!-- NETEXPLORE_KANBAN_DATA_END -->"
	MaxAttachmentsPerCard       = 10
	MaxAttachmentSize     int64 = 10 << 20
)

var boardNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

var ErrBoardExists = errors.New("board already exists")
var ErrCardExists = errors.New("card already exists on destination board")

type Store struct {
	root        string
	now         func() time.Time
	mutex       sync.Mutex
	writeFile   func(string, []byte) error
	writeResult func(string, []byte) (bool, error)
}

type archiveJournal struct {
	BoardImage []byte `json:"board_image"`
	DoneImage  []byte `json:"done_image"`
}

func NewStore(root string, clock func() time.Time) *Store {
	if clock == nil {
		clock = time.Now
	}
	return &Store{root: root, now: clock, writeFile: atomicWrite, writeResult: atomicWriteResult}
}

func (s *Store) EnsureBoard(name string) error {
	return s.withLock(name, func() error { return s.ensureUnlocked(name) })
}

// CreateBoard creates both canonical notes while holding the board lock. Unlike
// EnsureBoard, it never adopts or overwrites an existing board.
func (s *Store) CreateBoard(name string) (Board, error) {
	created := Board{Version: 1, Name: name, Cards: []Card{}}
	err := s.withLock(name, func() error {
		for _, file := range []string{s.boardPath(name), s.donePath(name)} {
			if _, err := os.Stat(file); err == nil {
				return ErrBoardExists
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		now := s.now().UTC()
		if err := atomicWrite(s.donePath(name), renderBoard(created, true, now)); err != nil {
			return err
		}
		if err := atomicWrite(s.boardPath(name), renderBoard(created, false, now)); err != nil {
			_ = os.Remove(s.donePath(name))
			return err
		}
		return nil
	})
	return created, err
}

// BoardNames lists board names without taking the store mutex or any board
// lock. It exists for callers that can run *inside* a board's critical
// section: moving a task to a board holds that board's lock across the
// reindex, and the reindex fingerprints the board set — going through
// ListBoards there would re-enter the non-reentrant mutex and deadlock.
//
// Directory names are the board names, so no file needs to be parsed; a
// name list that is a moment stale is harmless to its callers.
func (s *Store) BoardNames() []string {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if !entry.IsDir() || !validBoardName(entry.Name()) {
			continue
		}
		if _, err := os.Stat(s.boardPath(entry.Name())); err != nil {
			continue // a directory without board.md is not a board yet
		}
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out
}

func (s *Store) ListBoards() ([]BoardSummary, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return []BoardSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]BoardSummary, 0)
	for _, entry := range entries {
		if !entry.IsDir() || !validBoardName(entry.Name()) {
			continue
		}
		board, err := s.Load(entry.Name())
		if err != nil {
			return nil, err
		}
		counts := map[Status]int{}
		for _, card := range board.Cards {
			counts[card.Status]++
		}
		out = append(out, BoardSummary{Name: board.Name, Counts: counts})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Store) Load(name string) (Board, error) {
	if !validBoardName(name) {
		return Board{}, errors.New("invalid board name")
	}
	var loaded Board
	err := s.withReadLock(name, func() error {
		var err error
		loaded, err = readBoard(s.boardPath(name))
		return err
	})
	return loaded, err
}

func (s *Store) UpdateBoardSettings(name string, dispatchEnabled bool) (Board, error) {
	var updated Board
	err := s.withLock(name, func() error {
		var err error
		updated, err = readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		updated.DispatchEnabled = dispatchEnabled
		return s.writeActive(name, updated)
	})
	return updated, err
}

func (s *Store) CreateCard(name string, input CardInput) (Card, error) {
	return s.createCard(name, input, true)
}

// CreateCardOnExistingBoard is the task-import path: it must never create a
// board as a side effect of a stale or invalid selection.
func (s *Store) CreateCardOnExistingBoard(name string, input CardInput) (Card, error) {
	return s.createCard(name, input, false)
}

// TaskMoveBoard holds the selected board lock for the complete source/card
// transaction, preventing edits, moves, archives, or deletes from racing its
// compensating rollback.
type TaskMoveBoard struct {
	store *Store
	name  string
}

func (s *Store) WithTaskMoveBoard(name string, fn func(*TaskMoveBoard) error) error {
	if fn == nil {
		return errors.New("task-move transaction callback is required")
	}
	return s.withBoardLocks([]string{name}, func() error {
		if _, err := readBoard(s.boardPath(name)); err != nil {
			return err
		}
		return fn(&TaskMoveBoard{store: s, name: name})
	})
}

func (tx *TaskMoveBoard) Create(input CardInput) (Card, bool, error) {
	return tx.store.createCardOnExistingBoardResultUnlocked(tx.name, input)
}

func (tx *TaskMoveBoard) DeleteExact(expected Card) (Card, bool, error) {
	return tx.store.deleteTaskMoveCardResultUnlocked(tx.name, expected)
}

// CreateCardOnExistingBoardResult is the task-move transaction path. It
// reports whether the board rename committed even when the following
// directory sync reports an error.
func (s *Store) CreateCardOnExistingBoardResult(name string, input CardInput) (Card, bool, error) {
	var created Card
	committed := false
	err := s.withBoardLocks([]string{name}, func() error {
		var err error
		created, committed, err = s.createCardOnExistingBoardResultUnlocked(name, input)
		return err
	})
	return created, committed, err
}

func (s *Store) createCardOnExistingBoardResultUnlocked(name string, input CardInput) (Card, bool, error) {
	var created Card
	if err := validateInput(input); err != nil {
		return created, false, err
	}
	value, err := readBoard(s.boardPath(name))
	if err != nil {
		return created, false, err
	}
	now := s.now().UTC()
	created = Card{
		ID: newID(now), Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description),
		DueDate: strings.TrimSpace(input.DueDate), Milestone: strings.ToLower(strings.TrimSpace(input.Milestone)),
		Status: input.Status, Assignee: strings.TrimSpace(input.Assignee), Agent: strings.TrimSpace(input.Agent), Blocked: input.Blocked, Labels: normalizeLabels(input.Labels),
		Comments: []Comment{}, Attachments: []Attachment{}, CreatedAt: now, UpdatedAt: now,
	}
	if created.Status == "" {
		created.Status = Triage
	}
	value.Cards = append(value.Cards, created)
	committed, err := s.writeResult(s.boardPath(name), renderBoard(value, false, s.now().UTC()))
	return created, committed, err
}

func (s *Store) createCard(name string, input CardInput, ensure bool) (Card, error) {
	var created Card
	operation := func() error {
		if ensure {
			if err := s.ensureUnlocked(name); err != nil {
				return err
			}
		}
		if err := validateInput(input); err != nil {
			return err
		}
		board, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		now := s.now().UTC()
		created = Card{
			ID: newID(now), Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description),
			DueDate: strings.TrimSpace(input.DueDate), Milestone: strings.ToLower(strings.TrimSpace(input.Milestone)),
			Status: input.Status, Assignee: strings.TrimSpace(input.Assignee), Agent: strings.TrimSpace(input.Agent), Blocked: input.Blocked, Labels: normalizeLabels(input.Labels),
			Comments: []Comment{}, Attachments: []Attachment{}, CreatedAt: now, UpdatedAt: now,
		}
		if created.Status == "" {
			created.Status = Triage
		}
		board.Cards = append(board.Cards, created)
		return s.writeActive(name, board)
	}
	var err error
	if ensure {
		err = s.withLock(name, operation)
	} else {
		err = s.withBoardLocks([]string{name}, operation)
	}
	return created, err
}

func (s *Store) UpdateCard(name, id string, patch CardPatch) (Card, error) {
	var updated Card
	err := s.withLock(name, func() error {
		board, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		index := cardIndex(board.Cards, id)
		if index < 0 {
			return os.ErrNotExist
		}
		updated = board.Cards[index]
		if patch.Title != nil {
			updated.Title = strings.TrimSpace(*patch.Title)
		}
		if patch.Description != nil {
			updated.Description = strings.TrimSpace(*patch.Description)
		}
		if patch.DueDate != nil {
			updated.DueDate = strings.TrimSpace(*patch.DueDate)
		}
		if patch.Milestone != nil {
			updated.Milestone = strings.ToLower(strings.TrimSpace(*patch.Milestone))
		}
		if patch.Status != nil {
			updated.Status = *patch.Status
		}
		if patch.Assignee != nil {
			updated.Assignee = strings.TrimSpace(*patch.Assignee)
		}
		if patch.Agent != nil {
			updated.Agent = strings.TrimSpace(*patch.Agent)
		}
		if patch.Blocked != nil {
			updated.Blocked = *patch.Blocked
		}
		if patch.Labels != nil {
			updated.Labels = normalizeLabels(*patch.Labels)
		}
		if err := validateCard(updated); err != nil {
			return err
		}
		updated.UpdatedAt = s.now().UTC()
		if updated.Status == Done {
			doneAt := updated.UpdatedAt
			updated.DoneAt = &doneAt
			doneBoard, err := readBoard(s.donePath(name))
			if err != nil {
				return err
			}
			if cardIndex(doneBoard.Cards, updated.ID) < 0 {
				doneBoard.Cards = append([]Card{updated}, doneBoard.Cards...)
			}
			board.Cards = append(board.Cards[:index], board.Cards[index+1:]...)
			return s.commitArchiveUnlocked(name, archiveJournal{
				BoardImage: renderBoard(board, false, updated.UpdatedAt),
				DoneImage:  renderBoard(doneBoard, true, updated.UpdatedAt),
			})
		} else {
			updated.DoneAt = nil
			board.Cards[index] = updated
		}
		return s.writeActive(name, board)
	})
	return updated, err
}

func (s *Store) MoveCard(name, id string, status Status, beforeID string) (Card, error) {
	if !ValidStatus(status) {
		return Card{}, errors.New("invalid move status")
	}
	if status == Done {
		if beforeID != "" {
			return Card{}, errors.New("done moves cannot specify a before card")
		}
		return s.UpdateCard(name, id, CardPatch{Status: &status})
	}
	var moved Card
	err := s.withLock(name, func() error {
		value, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		index := cardIndex(value.Cards, id)
		if index < 0 {
			return os.ErrNotExist
		}
		moved = value.Cards[index]
		if beforeID == id && moved.Status == status {
			return nil
		}
		value.Cards = append(value.Cards[:index], value.Cards[index+1:]...)
		insertAt := len(value.Cards)
		if beforeID != "" {
			insertAt = cardIndex(value.Cards, beforeID)
			if insertAt < 0 {
				return errors.New("before card does not exist")
			}
			if value.Cards[insertAt].Status != status {
				return errors.New("before card is not in destination status")
			}
		} else {
			for i := range value.Cards {
				if value.Cards[i].Status == status {
					insertAt = i + 1
				}
			}
		}
		moved.Status = status
		moved.DoneAt = nil
		moved.UpdatedAt = s.now().UTC()
		value.Cards = append(value.Cards, Card{})
		copy(value.Cards[insertAt+1:], value.Cards[insertAt:])
		value.Cards[insertAt] = moved
		return s.writeActive(name, value)
	})
	return moved, err
}

// MoveCardToBoard transfers a card and its attachment directory while holding
// both board locks. Card identity and content timestamps are preserved; the
// transfer itself is recorded in the card's audit history.
func (s *Store) MoveCardToBoard(sourceName, destinationName, id, actor string) (Card, error) {
	if !validBoardName(sourceName) || !validBoardName(destinationName) {
		return Card{}, errors.New("invalid board name")
	}
	if sourceName == destinationName {
		return Card{}, errors.New("destination board must be different")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" || len(actor) > 80 {
		return Card{}, errors.New("actor must be 1-80 characters")
	}

	var moved Card
	err := s.withBoardLocks([]string{sourceName, destinationName}, func() error {
		source, err := readBoard(s.boardPath(sourceName))
		if err != nil {
			return err
		}
		destination, err := readBoard(s.boardPath(destinationName))
		if err != nil {
			return err
		}
		sourceIndex := cardIndex(source.Cards, id)
		if sourceIndex < 0 {
			return os.ErrNotExist
		}
		if cardIndex(destination.Cards, id) >= 0 {
			return ErrCardExists
		}

		now := s.now().UTC()
		sourceBefore := renderBoard(source, false, now)
		destinationBefore := renderBoard(destination, false, now)
		moved = source.Cards[sourceIndex]
		moved.Audit = append(moved.Audit, AuditEntry{
			Action: "moved_board", Actor: actor, FromBoard: sourceName,
			ToBoard: destinationName, CreatedAt: now,
		})
		source.Cards = append(source.Cards[:sourceIndex], source.Cards[sourceIndex+1:]...)
		destination.Cards = append(destination.Cards, moved)

		sourceAfter := renderBoard(source, false, now)
		destinationAfter := renderBoard(destination, false, now)

		var sourceAttachments, destinationAttachments string
		if len(moved.Attachments) > 0 {
			sourceAttachments, err = s.attachmentDirUnlocked(sourceName, id, false)
			if err != nil {
				return err
			}
			destinationRoot := filepath.Join(s.root, destinationName, "attachments")
			if err := os.MkdirAll(destinationRoot, 0o750); err != nil {
				return err
			}
			if info, err := os.Lstat(destinationRoot); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("destination attachment directory is unsafe")
			}
			destinationAttachments = filepath.Join(destinationRoot, id)
			if _, err := os.Lstat(destinationAttachments); !errors.Is(err, os.ErrNotExist) {
				if err == nil {
					return ErrCardExists
				}
				return err
			}
			if err := os.Rename(sourceAttachments, destinationAttachments); err != nil {
				return err
			}
		}
		rollbackAttachments := func() error {
			if destinationAttachments == "" {
				return nil
			}
			return os.Rename(destinationAttachments, sourceAttachments)
		}
		if err := s.writeFile(s.boardPath(destinationName), destinationAfter); err != nil {
			if rollbackErr := rollbackAttachments(); rollbackErr != nil {
				return fmt.Errorf("write destination: %v; rollback attachments: %w", err, rollbackErr)
			}
			return err
		}
		if err := s.writeFile(s.boardPath(sourceName), sourceAfter); err != nil {
			destinationRollbackErr := atomicWrite(s.boardPath(destinationName), destinationBefore)
			sourceRollbackErr := atomicWrite(s.boardPath(sourceName), sourceBefore)
			attachmentRollbackErr := rollbackAttachments()
			if destinationRollbackErr != nil || sourceRollbackErr != nil || attachmentRollbackErr != nil {
				return fmt.Errorf("write source: %v; rollback destination: %v; rollback source: %v; rollback attachments: %v", err, destinationRollbackErr, sourceRollbackErr, attachmentRollbackErr)
			}
			return err
		}
		return nil
	})
	return moved, err
}

func (s *Store) DeleteCard(name, id string) (Card, error) {
	var deleted Card
	err := s.withLock(name, func() error {
		value, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		index := cardIndex(value.Cards, id)
		if index < 0 {
			return os.ErrNotExist
		}
		deleted = value.Cards[index]
		var attachmentDir, quarantine string
		if len(deleted.Attachments) > 0 {
			attachmentDir, err = s.attachmentDirUnlocked(name, id, false)
			if err != nil {
				return err
			}
			quarantine = filepath.Join(filepath.Dir(attachmentDir), ".deleting-card-"+id)
			if err := os.Rename(attachmentDir, quarantine); err != nil {
				return err
			}
		}
		value.Cards = append(value.Cards[:index], value.Cards[index+1:]...)
		if err := s.writeActive(name, value); err != nil {
			if quarantine != "" {
				_ = os.Rename(quarantine, attachmentDir)
			}
			return err
		}
		if quarantine != "" {
			return os.RemoveAll(quarantine)
		}
		return nil
	})
	return deleted, err
}

// DeleteTaskMoveCardResult removes a newly-created task card while reporting
// whether the board rename committed. Task-import cards have no attachments;
// if one gained attachments concurrently, retain it and let the source
// reference be reapplied rather than deleting user data.
func (s *Store) DeleteTaskMoveCardResult(name string, expected Card) (Card, bool, error) {
	var deleted Card
	committed := false
	err := s.withLock(name, func() error {
		var err error
		deleted, committed, err = s.deleteTaskMoveCardResultUnlocked(name, expected)
		return err
	})
	return deleted, committed, err
}

func (s *Store) deleteTaskMoveCardResultUnlocked(name string, expected Card) (Card, bool, error) {
	var deleted Card
	value, err := readBoard(s.boardPath(name))
	if err != nil {
		return deleted, false, err
	}
	index := cardIndex(value.Cards, expected.ID)
	if index < 0 {
		return deleted, false, os.ErrNotExist
	}
	deleted = value.Cards[index]
	if !reflect.DeepEqual(deleted, expected) {
		return deleted, false, errors.New("task-move card changed; refusing rollback deletion")
	}
	value.Cards = append(value.Cards[:index], value.Cards[index+1:]...)
	committed, err := s.writeResult(s.boardPath(name), renderBoard(value, false, s.now().UTC()))
	return deleted, committed, err
}

func (s *Store) ListArchived(name, query string, limit int) ([]Card, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("archive limit must be 1-100")
	}
	query = strings.TrimSpace(strings.ToLower(query))
	if len(query) > 200 {
		return nil, errors.New("archive query must be at most 200 characters")
	}
	cards := []Card{}
	err := s.withReadLock(name, func() error {
		value, err := readBoard(s.donePath(name))
		if err != nil {
			return err
		}
		for _, card := range value.Cards {
			if query == "" || fuzzyArchiveMatch(card, query) {
				cards = append(cards, card)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(cards, func(i, j int) bool {
		left, right := cards[i].UpdatedAt, cards[j].UpdatedAt
		if cards[i].DoneAt != nil {
			left = *cards[i].DoneAt
		}
		if cards[j].DoneAt != nil {
			right = *cards[j].DoneAt
		}
		return left.After(right)
	})
	if len(cards) > limit {
		cards = cards[:limit]
	}
	return cards, nil
}

func fuzzyArchiveMatch(card Card, query string) bool {
	haystack := strings.ToLower(strings.Join(append([]string{card.Title, card.Description, card.Assignee}, card.Labels...), " "))
	words := strings.FieldsFunc(haystack, func(r rune) bool { return r < '0' || (r > '9' && r < 'a') || r > 'z' })
	for _, token := range strings.Fields(query) {
		if strings.Contains(haystack, token) {
			continue
		}
		matched := false
		for _, word := range words {
			position := 0
			for _, char := range word {
				if position < len(token) && byte(char) == token[position] {
					position++
				}
			}
			if position == len(token) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// RestoreCard moves an archived card back to an active status using the same
// crash-recoverable two-file transaction as Done archival.
func (s *Store) RestoreCard(name, id string, status Status) (Card, error) {
	if status == Done || !ValidStatus(status) {
		return Card{}, errors.New("restore status must be active")
	}
	var restored Card
	err := s.withLock(name, func() error {
		active, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		archived, err := readBoard(s.donePath(name))
		if err != nil {
			return err
		}
		index := cardIndex(archived.Cards, id)
		if index < 0 {
			return os.ErrNotExist
		}
		if cardIndex(active.Cards, id) >= 0 {
			return errors.New("card already exists on active board")
		}
		restored = archived.Cards[index]
		restored.Status = status
		restored.DoneAt = nil
		restored.UpdatedAt = s.now().UTC()
		if err := validateCard(restored); err != nil {
			return err
		}
		archived.Cards = append(archived.Cards[:index], archived.Cards[index+1:]...)
		active.Cards = append(active.Cards, restored)
		return s.commitArchiveUnlocked(name, archiveJournal{
			BoardImage: renderBoard(active, false, restored.UpdatedAt),
			DoneImage:  renderBoard(archived, true, restored.UpdatedAt),
		})
	})
	return restored, err
}

func (s *Store) AddComment(name, id, author, body string) (Card, error) {
	var updated Card
	err := s.withLock(name, func() error {
		author = strings.TrimSpace(author)
		body = strings.TrimSpace(body)
		if author == "" || len(author) > 80 {
			return errors.New("comment author must be 1-80 characters")
		}
		if body == "" || len(body) > 2000 {
			return errors.New("comment must be 1-2000 characters")
		}
		board, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		index := cardIndex(board.Cards, id)
		if index < 0 {
			return os.ErrNotExist
		}
		now := s.now().UTC()
		comment := Comment{ID: newID(now), Author: author, Body: body, CreatedAt: now}
		board.Cards[index].Comments = append(board.Cards[index].Comments, comment)
		board.Cards[index].UpdatedAt = now
		updated = board.Cards[index]
		return s.writeActive(name, board)
	})
	return updated, err
}

func (s *Store) AddAttachment(name, cardID, filename, contentType string, source io.Reader, size int64) (Attachment, error) {
	var created Attachment
	err := s.withLock(name, func() error {
		value, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		index := cardIndex(value.Cards, cardID)
		if index < 0 {
			return os.ErrNotExist
		}
		if len(value.Cards[index].Attachments) >= MaxAttachmentsPerCard {
			return errors.New("attachment limit reached")
		}
		if size < 0 || size > MaxAttachmentSize {
			return errors.New("attachment size must be at most 10 MiB")
		}
		normalized, err := normalizeAttachmentFilename(filename)
		if err != nil {
			return err
		}
		contentType = strings.TrimSpace(contentType)
		if contentType == "" || len(contentType) > 100 || strings.ContainsAny(contentType, "\r\n") {
			return errors.New("invalid attachment content type")
		}
		now := s.now().UTC()
		created = Attachment{ID: newID(now), Filename: normalized, Size: size, ContentType: contentType, CreatedAt: now}
		dir, err := s.attachmentDirUnlocked(name, cardID, true)
		if err != nil {
			return err
		}
		filePath := filepath.Join(dir, attachmentDiskName(created))
		file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(file, io.LimitReader(source, MaxAttachmentSize+1))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || written != size {
			_ = os.Remove(filePath)
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			return errors.New("attachment size did not match upload")
		}
		value.Cards[index].Attachments = append(value.Cards[index].Attachments, created)
		value.Cards[index].UpdatedAt = now
		if err := s.writeActive(name, value); err != nil {
			_ = os.Remove(filePath)
			return err
		}
		return nil
	})
	return created, err
}

func (s *Store) ListAttachments(name, cardID string) ([]Attachment, error) {
	attachments := []Attachment{}
	err := s.withReadLock(name, func() error {
		value, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		index := cardIndex(value.Cards, cardID)
		if index < 0 {
			return os.ErrNotExist
		}
		attachments = append(attachments, value.Cards[index].Attachments...)
		return nil
	})
	return attachments, err
}

func (s *Store) DeleteAttachment(name, cardID, attachmentID string) error {
	return s.withLock(name, func() error {
		if !validObjectID(attachmentID) {
			return os.ErrNotExist
		}
		value, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		cardAt := cardIndex(value.Cards, cardID)
		if cardAt < 0 {
			return os.ErrNotExist
		}
		attachmentAt := -1
		for i, candidate := range value.Cards[cardAt].Attachments {
			if candidate.ID == attachmentID {
				attachmentAt = i
				break
			}
		}
		if attachmentAt < 0 {
			return os.ErrNotExist
		}
		attachment := value.Cards[cardAt].Attachments[attachmentAt]
		dir, err := s.attachmentDirUnlocked(name, cardID, false)
		if err != nil {
			return err
		}
		filePath := filepath.Join(dir, attachmentDiskName(attachment))
		info, err := os.Lstat(filePath)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("attachment file is unsafe")
		}
		quarantine := filepath.Join(dir, ".deleting-"+attachment.ID)
		if err := os.Rename(filePath, quarantine); err != nil {
			return err
		}
		value.Cards[cardAt].Attachments = append(value.Cards[cardAt].Attachments[:attachmentAt], value.Cards[cardAt].Attachments[attachmentAt+1:]...)
		value.Cards[cardAt].UpdatedAt = s.now().UTC()
		if err := s.writeActive(name, value); err != nil {
			_ = os.Rename(quarantine, filePath)
			return err
		}
		return os.Remove(quarantine)
	})
}

func (s *Store) OpenAttachment(name, cardID, attachmentID string) (*os.File, Attachment, error) {
	var file *os.File
	var found Attachment
	err := s.withReadLock(name, func() error {
		value, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		index := cardIndex(value.Cards, cardID)
		if index < 0 {
			return os.ErrNotExist
		}
		for _, candidate := range value.Cards[index].Attachments {
			if candidate.ID == attachmentID {
				found = candidate
				break
			}
		}
		if found.ID == "" {
			return os.ErrNotExist
		}
		dir, err := s.attachmentDirUnlocked(name, cardID, false)
		if err != nil {
			return err
		}
		file, err = os.OpenFile(filepath.Join(dir, attachmentDiskName(found)), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != found.Size {
			_ = file.Close()
			file = nil
			if statErr != nil {
				return statErr
			}
			return errors.New("attachment file is invalid")
		}
		return nil
	})
	return file, found, err
}

func normalizeAttachmentFilename(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if index := strings.LastIndex(name, "/"); index >= 0 {
		name = name[index+1:]
	}
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		return "", errors.New("invalid attachment filename")
	}
	if len(name) > 180 {
		return "", errors.New("attachment filename must be at most 180 bytes")
	}
	return name, nil
}

func attachmentDiskName(value Attachment) string { return value.ID + "-" + value.Filename }

func (s *Store) attachmentDirUnlocked(name, cardID string, create bool) (string, error) {
	if !validObjectID(cardID) {
		return "", errors.New("invalid card id")
	}
	attachmentsDir := filepath.Join(s.root, name, "attachments")
	cardDir := filepath.Join(attachmentsDir, cardID)
	for _, dir := range []string{attachmentsDir, cardDir} {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := os.Mkdir(dir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
			info, err = os.Lstat(dir)
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("attachment directory is unsafe")
		}
	}
	return cardDir, nil
}

func validObjectID(id string) bool {
	if id == "" || len(id) > 100 {
		return false
	}
	for _, r := range id {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func (s *Store) withLock(name string, fn func() error) error {
	return s.withLockMode(name, true, fn)
}

// withReadLock is the read-path variant: it never creates the board
// directory, so probing arbitrary names (GET /api/boards/...) leaves no
// directories or lock files behind.
func (s *Store) withReadLock(name string, fn func() error) error {
	return s.withLockMode(name, false, fn)
}

func (s *Store) withLockMode(name string, create bool, fn func() error) error {
	if !validBoardName(name) {
		return errors.New("invalid board name")
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	dir := filepath.Join(s.root, name)
	if create {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	} else if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.ErrNotExist
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := s.recoverArchiveUnlocked(name); err != nil {
		return err
	}
	return fn()
}

func (s *Store) withBoardLocks(names []string, fn func() error) error {
	names = append([]string(nil), names...)
	sort.Strings(names)
	s.mutex.Lock()
	defer s.mutex.Unlock()
	locks := make([]*os.File, 0, len(names))
	for _, name := range names {
		if !validBoardName(name) {
			return errors.New("invalid board name")
		}
		dir := filepath.Join(s.root, name)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			if errors.Is(err, os.ErrNotExist) {
				return os.ErrNotExist
			}
			return err
		}
		lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			lock.Close()
			return err
		}
		locks = append(locks, lock)
	}
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			_ = syscall.Flock(int(locks[i].Fd()), syscall.LOCK_UN)
			_ = locks[i].Close()
		}
	}()
	for _, name := range names {
		if err := s.recoverArchiveUnlocked(name); err != nil {
			return err
		}
	}
	return fn()
}

func (s *Store) ensureUnlocked(name string) error {
	if err := os.MkdirAll(filepath.Join(s.root, name), 0o750); err != nil {
		return err
	}
	now := s.now().UTC()
	if _, err := os.Stat(s.boardPath(name)); errors.Is(err, os.ErrNotExist) {
		if err := atomicWrite(s.boardPath(name), renderBoard(Board{Version: 1, Name: name, Cards: []Card{}}, false, now)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(s.donePath(name)); errors.Is(err, os.ErrNotExist) {
		return atomicWrite(s.donePath(name), renderBoard(Board{Version: 1, Name: name, Cards: []Card{}}, true, now))
	} else {
		return err
	}
}

func (s *Store) writeActive(name string, board Board) error {
	return s.writeFile(s.boardPath(name), renderBoard(board, false, s.now().UTC()))
}
func (s *Store) boardPath(name string) string { return filepath.Join(s.root, name, "board.md") }
func (s *Store) donePath(name string) string  { return filepath.Join(s.root, name, "done.md") }
func (s *Store) journalPath(name string) string {
	return filepath.Join(s.root, name, ".done-archive.wal")
}

func (s *Store) commitArchiveUnlocked(name string, journal archiveJournal) error {
	payload, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	if err := atomicWrite(s.journalPath(name), payload); err != nil {
		return err
	}
	return s.applyArchiveJournalUnlocked(name, journal)
}

func (s *Store) recoverArchiveUnlocked(name string) error {
	payload, err := os.ReadFile(s.journalPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal archiveJournal
	if err := json.Unmarshal(payload, &journal); err != nil {
		return s.quarantineArchiveJournalUnlocked(name, fmt.Errorf("parse archive journal: %w", err))
	}
	if len(journal.BoardImage) == 0 || len(journal.DoneImage) == 0 {
		return s.quarantineArchiveJournalUnlocked(name, errors.New("archive journal is incomplete"))
	}
	return s.applyArchiveJournalUnlocked(name, journal)
}

// quarantineArchiveJournalUnlocked sidelines a corrupt WAL so it stops
// blocking every operation on the board; the last committed board/done
// images stay authoritative and the renamed file is kept for forensics.
func (s *Store) quarantineArchiveJournalUnlocked(name string, cause error) error {
	quarantined := fmt.Sprintf("%s.corrupt-%d", s.journalPath(name), s.now().UTC().Unix())
	if err := os.Rename(s.journalPath(name), quarantined); err != nil {
		return fmt.Errorf("quarantine corrupt archive journal (%v): %w", cause, err)
	}
	slog.Warn("kanban archive journal corrupt; quarantined",
		"board", name, "quarantined", quarantined, "err", cause)
	return syncDir(filepath.Dir(s.journalPath(name)))
}

func (s *Store) applyArchiveJournalUnlocked(name string, journal archiveJournal) error {
	if err := atomicWrite(s.donePath(name), journal.DoneImage); err != nil {
		return err
	}
	if err := atomicWrite(s.boardPath(name), journal.BoardImage); err != nil {
		return err
	}
	if err := os.Remove(s.journalPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(filepath.Dir(s.journalPath(name)))
}

func readBoard(path string) (Board, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Board{}, err
	}
	text := string(data)
	startMarker, endMarker := dataStart, dataEnd
	start := strings.Index(text, startMarker)
	if start < 0 {
		startMarker, endMarker = legacyDataStart, legacyDataEnd
		start = strings.Index(text, startMarker)
	}
	if start < 0 {
		return Board{}, fmt.Errorf("%s is missing Kanban data", path)
	}
	start += len(startMarker)
	end := strings.Index(text[start:], endMarker)
	if end < 0 {
		return Board{}, fmt.Errorf("%s has incomplete Kanban data", path)
	}
	var board Board
	if err := json.Unmarshal([]byte(text[start:start+end]), &board); err != nil {
		return Board{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if board.Version != 1 || !validBoardName(board.Name) {
		return Board{}, fmt.Errorf("unsupported or invalid board data in %s", path)
	}
	if board.Cards == nil {
		board.Cards = []Card{}
	}
	return board, nil
}

func renderBoard(board Board, doneOnly bool, updated time.Time) []byte {
	board.Version = 1
	payload, _ := json.MarshalIndent(board, "", "  ")
	title := displayName(board.Name)
	kind := "board"
	heading := title + " Kanban"
	if doneOnly {
		kind = "archive"
		heading = title + " Kanban — Done"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntype: project\ncreated: 2026-07-24\nupdated: %s\ndispatch: %t\ntags: [kanban, %s, %s]\n---\n\n# %s\n\n", updated.Format("2006-01-02"), board.DispatchEnabled, board.Name, kind, heading)
	if doneOnly {
		b.WriteString("> Completed cards are removed from the active board and preserved here.\n\n")
	} else {
		b.WriteString("> Managed by Netexplore Kanban. The structured data and readable board live together in this note.\n\n")
	}
	b.WriteString("## Kanban data\n\n" + dataStart + string(payload) + dataEnd + "\n\n")
	statuses := ActiveStatuses
	if doneOnly {
		statuses = []Status{Done}
	}
	for _, status := range statuses {
		fmt.Fprintf(&b, "## %s\n\n", statusLabel(status))
		found := false
		for _, card := range board.Cards {
			if card.Status != status {
				continue
			}
			found = true
			fmt.Fprintf(&b, "### %s ^card-%s\n\n- **ID:** `%s`\n", card.Title, card.ID, card.ID)
			if card.Assignee != "" {
				fmt.Fprintf(&b, "- **Assignee:** %s\n", card.Assignee)
			}
			if card.Agent != "" {
				fmt.Fprintf(&b, "- **Agent:** %s\n", card.Agent)
			}
			if card.Blocked {
				b.WriteString("- **Blocked:** yes\n")
			}
			if card.DueDate != "" {
				fmt.Fprintf(&b, "- **Due:** %s\n", card.DueDate)
			}
			if card.Milestone != "" {
				fmt.Fprintf(&b, "- **Milestone:** %s\n", card.Milestone)
			}
			if len(card.Labels) > 0 {
				b.WriteString("- **Labels:**")
				for _, label := range card.Labels {
					fmt.Fprintf(&b, " `%s`", label)
				}
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "- **Updated:** %s\n", card.UpdatedAt.Format(time.RFC3339))
			if card.DoneAt != nil {
				fmt.Fprintf(&b, "- **Done:** %s\n", card.DoneAt.Format(time.RFC3339))
			}
			if card.Description != "" {
				fmt.Fprintf(&b, "\n%s\n", card.Description)
			}
			if len(card.Comments) > 0 {
				b.WriteString("\n#### Comments\n\n")
				for _, comment := range card.Comments {
					fmt.Fprintf(&b, "- **%s — %s:** %s\n", comment.CreatedAt.Format("2006-01-02 15:04Z"), comment.Author, strings.ReplaceAll(comment.Body, "\n", " "))
				}
			}
			b.WriteString("\n")
		}
		if !found {
			b.WriteString("_No cards._\n\n")
		}
	}
	return []byte(b.String())
}

func atomicWrite(path string, data []byte) error {
	_, err := atomicWriteResult(path, data)
	return err
}

// atomicWriteResult reports whether rename installed data at path. An error
// from the subsequent directory sync does not make the committed state
// ambiguous to callers coordinating another file mutation.
func atomicWriteResult(path string, data []byte) (bool, error) {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".kanban-*.tmp")
	if err != nil {
		return false, err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o640); err != nil {
		temp.Close()
		return false, err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return false, err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return false, err
	}
	if err := temp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tempName, path); err != nil {
		return false, err
	}
	if err := syncDir(dir); err != nil {
		return true, err
	}
	return true, nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func validateInput(input CardInput) error {
	if input.Status == "" {
		input.Status = Triage
	}
	return validateCard(Card{Title: strings.TrimSpace(input.Title), Description: input.Description, DueDate: strings.TrimSpace(input.DueDate), Milestone: strings.ToLower(strings.TrimSpace(input.Milestone)), Status: input.Status, Assignee: input.Assignee, Agent: input.Agent, Blocked: input.Blocked, Labels: input.Labels})
}
func validateCard(card Card) error {
	if card.Title == "" || len(card.Title) > 200 || strings.ContainsAny(card.Title, "\r\n") {
		return errors.New("title must be 1-200 characters on one line")
	}
	if len(card.Description) > 10000 {
		return errors.New("description exceeds 10000 characters")
	}
	if card.DueDate != "" {
		parsed, err := time.Parse("2006-01-02", card.DueDate)
		if err != nil || parsed.Format("2006-01-02") != card.DueDate {
			return errors.New("due date must be a valid YYYY-MM-DD date")
		}
	}
	if len(card.Milestone) > 32 || strings.ContainsAny(card.Milestone, "\r\n`") {
		return errors.New("milestone must be at most 32 safe characters")
	}
	if !ValidStatus(card.Status) {
		return errors.New("invalid card status")
	}
	if len(card.Assignee) > 80 {
		return errors.New("assignee exceeds 80 characters")
	}
	if len(card.Agent) > 64 || strings.ContainsAny(card.Agent, "\r\n`") {
		return errors.New("agent must be at most 64 safe characters")
	}
	if len(card.Labels) > 10 {
		return errors.New("at most 10 labels are allowed")
	}
	for _, label := range card.Labels {
		if len(label) == 0 || len(label) > 32 || strings.ContainsAny(label, "\r\n`") {
			return errors.New("labels must be 1-32 safe characters")
		}
	}
	return nil
}
func normalizeLabels(labels []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.ToLower(strings.TrimSpace(label))
		if label != "" && !seen[label] {
			seen[label] = true
			out = append(out, label)
		}
	}
	sort.Strings(out)
	return out
}
func newID(now time.Time) string {
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return fmt.Sprintf("k_%x_%s", now.UnixMilli(), hex.EncodeToString(buf))
}
func cardIndex(cards []Card, id string) int {
	for i := range cards {
		if cards[i].ID == id {
			return i
		}
	}
	return -1
}
func validBoardName(name string) bool { return boardNamePattern.MatchString(name) }
func displayName(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + strings.ReplaceAll(name[1:], "-", " ")
}
func statusLabel(status Status) string {
	switch status {
	case Triage:
		return "Triage"
	case Backlog:
		return "Backlog"
	case Ready:
		return "Ready"
	case InProgress:
		return "In progress"
	case Verify:
		return "Verify"
	case Done:
		return "Done"
	default:
		return string(status)
	}
}
