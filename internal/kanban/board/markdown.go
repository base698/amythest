package board

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The v4 board format: a regular markdown note.
//
//	---
//	kanban: 4
//	name: personal
//	displayName: Personal
//	meta:                      # machine bookkeeping, keyed by card id
//	  k_abc: {created: …, updated: …}
//	---
//
//	# In progress
//
//	## Fix the deploy pipeline ^k_abc
//	due:: 2026-09-10
//	priority:: p1
//	labels:: #infra #urgent
//
//	Free-form markdown description.
//
//	### Comments
//
//	- **2026-09-01T10:00:00Z — justin:** looks good
//
// H1 headings are columns, H2 headings are cards (the trailing ^block-id
// is the stable card id), `key:: value` lines directly under a card
// heading are its structured fields, and everything else is description.
// Timestamps, attachments, audit entries, and comment ids live in the
// frontmatter `meta` map so card bodies stay human. The server re-renders
// the file canonically on every write; hand edits are parsed first, so
// content survives even though formatting is normalized.

// boardFrontmatter is the YAML block at the top of a v4 board file.
type boardFrontmatter struct {
	Kanban          int                 `yaml:"kanban"`
	Name            string              `yaml:"name"`
	DisplayName     string              `yaml:"displayName,omitempty"`
	Description     string              `yaml:"description,omitempty"`
	Icon            string              `yaml:"icon,omitempty"`
	Color           string              `yaml:"color,omitempty"`
	SortOrder       int                 `yaml:"sortOrder,omitempty"`
	Pinned          bool                `yaml:"pinned,omitempty"`
	Archived        bool                `yaml:"archived,omitempty"`
	FocusCard       string              `yaml:"focusCard,omitempty"`
	DispatchEnabled bool                `yaml:"dispatchEnabled,omitempty"`
	Meta            map[string]cardMeta `yaml:"meta,omitempty"`
}

// cardMeta is the per-card machine bookkeeping humans never hand-edit.
type cardMeta struct {
	Created     time.Time     `yaml:"created,omitempty"`
	Updated     time.Time     `yaml:"updated,omitempty"`
	Done        *time.Time    `yaml:"done,omitempty"`
	Comments    []commentMeta `yaml:"comments,omitempty"` // in body order
	Attachments []Attachment  `yaml:"attachments,omitempty"`
	Audit       []AuditEntry  `yaml:"audit,omitempty"`
}

// commentMeta keeps a comment's id and full-precision timestamp; the
// readable bullet in the body shows seconds only.
type commentMeta struct {
	ID string    `yaml:"id"`
	At time.Time `yaml:"at"`
}

var statusByLabel = func() map[string]Status {
	out := map[string]Status{}
	for _, status := range append(append([]Status{}, ActiveStatuses...), Done) {
		out[strings.ToLower(statusLabel(status))] = status
		out[string(status)] = status // "in_progress" also accepted
	}
	return out
}()

const commentTimeLayout = time.RFC3339

// renderBoardMarkdown serializes a board as the v4 markdown note.
func renderBoardMarkdown(board Board, doneOnly bool, now time.Time) []byte {
	fm := boardFrontmatter{
		Kanban: 4, Name: board.Name, DisplayName: board.DisplayName,
		Description: board.Description, Icon: board.Icon, Color: board.Color,
		SortOrder: board.SortOrder, Pinned: board.Pinned, Archived: board.Archived,
		FocusCard: board.FocusCardID, DispatchEnabled: board.DispatchEnabled,
	}
	for _, card := range board.Cards {
		meta := cardMeta{
			Created: card.CreatedAt, Updated: card.UpdatedAt, Done: card.DoneAt,
			Attachments: card.Attachments, Audit: card.Audit,
		}
		// Hand-added cards have no timestamps yet; stamp at write time.
		if meta.Created.IsZero() {
			meta.Created = now
		}
		if meta.Updated.IsZero() {
			meta.Updated = meta.Created
		}
		for _, comment := range card.Comments {
			meta.Comments = append(meta.Comments, commentMeta{ID: comment.ID, At: comment.CreatedAt})
		}
		if fm.Meta == nil {
			fm.Meta = map[string]cardMeta{}
		}
		fm.Meta[card.ID] = meta
	}
	fmRaw, _ := yaml.Marshal(fm)

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmRaw)
	b.WriteString("---\n")

	// The section headings ARE the data: every status with cards must be
	// rendered or those cards would be lost. The primary set additionally
	// renders empty (so an active board always shows its five columns for
	// hand-adding); the rest appear only when occupied.
	primary := map[Status]bool{}
	for _, status := range ActiveStatuses {
		primary[status] = !doneOnly
	}
	primary[Done] = doneOnly
	counts := map[Status]int{}
	for _, card := range board.Cards {
		counts[card.Status]++
	}
	order := append(append([]Status{}, ActiveStatuses...), Done)
	if doneOnly {
		order = append([]Status{Done}, ActiveStatuses...)
	}
	for _, status := range order {
		if !primary[status] && counts[status] == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n# %s\n", statusLabel(status))
		for _, card := range board.Cards {
			if card.Status != status {
				continue
			}
			fmt.Fprintf(&b, "\n## %s ^%s\n", sanitizeHeadingText(card.Title), card.ID)
			writeField(&b, "due", card.DueDate)
			writeField(&b, "milestone", card.Milestone)
			writeField(&b, "priority", string(card.Priority))
			writeField(&b, "assignee", card.Assignee)
			writeField(&b, "agent", card.Agent)
			if card.Blocked {
				writeField(&b, "blocked", "yes")
			}
			if len(card.Labels) > 0 {
				tags := make([]string, len(card.Labels))
				for i, label := range card.Labels {
					tags[i] = "#" + label
				}
				writeField(&b, "labels", strings.Join(tags, " "))
			}
			if description := cleanMarkdownText(card.Description); description != "" {
				b.WriteString("\n" + sanitizeDescription(description) + "\n")
			}
			if len(card.Comments) > 0 {
				b.WriteString("\n### Comments\n\n")
				for _, comment := range card.Comments {
					lines := strings.Split(cleanMarkdownText(comment.Body), "\n")
					fmt.Fprintf(&b, "- **%s — %s:** %s\n",
						comment.CreatedAt.UTC().Format(commentTimeLayout), comment.Author, lines[0])
					for _, line := range lines[1:] {
						b.WriteString("  " + line + "\n")
					}
				}
			}
		}
	}
	return []byte(b.String())
}

func writeField(b *strings.Builder, key, value string) {
	if strings.TrimSpace(value) != "" {
		fmt.Fprintf(b, "%s:: %s\n", key, strings.TrimSpace(value))
	}
}

// sanitizeHeadingText keeps a card title on its heading line.
func sanitizeHeadingText(title string) string {
	title = strings.ReplaceAll(title, "\n", " ")
	return strings.TrimSpace(title)
}

// sanitizeDescription demotes H1/H2 headings inside descriptions — they
// would otherwise parse back as columns or cards.
func sanitizeDescription(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") {
			lines[i] = "###" + strings.TrimLeft(line, "#")
		}
	}
	return strings.Join(lines, "\n")
}

// parseBoardMarkdown parses a v4 board note back into the typed board.
func parseBoardMarkdown(data []byte, path string) (Board, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	rest, fmRaw, ok := splitFrontmatter(text)
	if !ok {
		return Board{}, fmt.Errorf("%s: missing frontmatter", path)
	}
	var fm boardFrontmatter
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return Board{}, fmt.Errorf("parse %s frontmatter: %w", path, err)
	}
	board := Board{
		Version: 4, Name: fm.Name, DisplayName: fm.DisplayName,
		Description: fm.Description, Icon: fm.Icon, Color: fm.Color,
		SortOrder: fm.SortOrder, Pinned: fm.Pinned, Archived: fm.Archived,
		FocusCardID: fm.FocusCard, DispatchEnabled: fm.DispatchEnabled,
		Cards: []Card{},
	}

	var current *Card
	status := Status("")
	inComments := false
	var descriptionLines []string
	flush := func() {
		if current == nil {
			return
		}
		current.Description = strings.TrimRight(strings.TrimPrefix(strings.Join(descriptionLines, "\n"), "\n"), "\n")
		current.Description = strings.TrimSpace(current.Description)
		board.Cards = append(board.Cards, *current)
		current, descriptionLines, inComments = nil, nil, false
	}

	for _, line := range strings.Split(rest, "\n") {
		switch {
		case strings.HasPrefix(line, "# "):
			flush()
			label := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "# ")))
			mapped, ok := statusByLabel[label]
			if !ok {
				return Board{}, fmt.Errorf("%s: unknown column heading %q (use %s)", path,
					strings.TrimSpace(strings.TrimPrefix(line, "# ")), knownColumnLabels())
			}
			status = mapped
		case strings.HasPrefix(line, "## "):
			flush()
			if status == "" {
				return Board{}, fmt.Errorf("%s: card %q appears before any column heading", path, strings.TrimPrefix(line, "## "))
			}
			title, id := splitCardHeading(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			card := Card{Title: title, ID: id, Status: status,
				Labels: []string{}, Comments: []Comment{}, Attachments: []Attachment{}}
			current = &card
		case current != nil && strings.HasPrefix(line, "### ") &&
			strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "### ")), "comments"):
			inComments = true
		case current != nil && inComments:
			if strings.HasPrefix(line, "- ") {
				comment, ok := parseCommentBullet(line)
				if !ok {
					return Board{}, fmt.Errorf("%s: unparseable comment under card %q: %s", path, current.Title, line)
				}
				current.Comments = append(current.Comments, comment)
			} else if trimmed := strings.TrimPrefix(line, "  "); trimmed != line && len(current.Comments) > 0 {
				last := &current.Comments[len(current.Comments)-1]
				last.Body += "\n" + trimmed
			}
		case current != nil:
			if key, value, ok := parseFieldLine(line); ok && len(descriptionLines) == 0 {
				applyCardField(current, key, value)
				continue
			}
			if len(descriptionLines) > 0 || strings.TrimSpace(line) != "" {
				descriptionLines = append(descriptionLines, line)
			}
		}
	}
	flush()

	// Attach the machine bookkeeping from frontmatter meta.
	for i := range board.Cards {
		card := &board.Cards[i]
		if card.ID == "" {
			card.ID = deterministicCardID(board.Name, card.Status, card.Title, i)
		}
		meta := fm.Meta[card.ID]
		card.CreatedAt, card.UpdatedAt, card.DoneAt = meta.Created, meta.Updated, meta.Done
		card.Attachments = meta.Attachments
		if card.Attachments == nil {
			card.Attachments = []Attachment{}
		}
		card.Audit = meta.Audit
		for j := range card.Comments {
			if j < len(meta.Comments) && meta.Comments[j].ID != "" {
				card.Comments[j].ID = meta.Comments[j].ID
				// The bullet shows seconds; meta keeps full precision.
				if at := meta.Comments[j].At; !at.IsZero() &&
					at.Truncate(time.Second).Equal(card.Comments[j].CreatedAt) {
					card.Comments[j].CreatedAt = at
				}
			} else {
				card.Comments[j].ID = deterministicCommentID(card.Comments[j])
			}
		}
	}
	return board, nil
}

// splitFrontmatter returns (body, frontmatter, ok).
func splitFrontmatter(text string) (string, string, bool) {
	if !strings.HasPrefix(text, "---\n") {
		return "", "", false
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return "", "", false
	}
	return text[4+end+5:], text[4 : 4+end+1], true
}

// splitCardHeading separates "Title ^k_id" into its parts.
func splitCardHeading(heading string) (title, id string) {
	if at := strings.LastIndex(heading, " ^"); at >= 0 && !strings.ContainsAny(heading[at+2:], " \t") {
		return strings.TrimSpace(heading[:at]), heading[at+2:]
	}
	return heading, ""
}

var fieldKeys = map[string]bool{
	"due": true, "milestone": true, "priority": true, "assignee": true,
	"agent": true, "blocked": true, "labels": true,
}

func parseFieldLine(line string) (key, value string, ok bool) {
	key, value, found := strings.Cut(line, ":: ")
	key = strings.ToLower(strings.TrimSpace(key))
	if !found || !fieldKeys[key] {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

func applyCardField(card *Card, key, value string) {
	switch key {
	case "due":
		card.DueDate = value
	case "milestone":
		card.Milestone = value
	case "priority":
		card.Priority = Priority(strings.ToLower(value))
	case "assignee":
		card.Assignee = value
	case "agent":
		card.Agent = value
	case "blocked":
		card.Blocked = value == "yes" || value == "true"
	case "labels":
		card.Labels = []string{}
		for _, tag := range strings.Fields(value) {
			if label := strings.TrimPrefix(tag, "#"); label != "" {
				card.Labels = append(card.Labels, label)
			}
		}
	}
}

var commentBulletPrefix = "- **"

func parseCommentBullet(line string) (Comment, bool) {
	body, ok := strings.CutPrefix(line, commentBulletPrefix)
	if !ok {
		return Comment{}, false
	}
	stamp, rest, ok := strings.Cut(body, " — ")
	if !ok {
		return Comment{}, false
	}
	author, text, ok := strings.Cut(rest, ":** ")
	if !ok {
		// A comment with an empty first line renders as "author:**".
		author, ok = strings.CutSuffix(rest, ":**")
		if !ok {
			return Comment{}, false
		}
	}
	at, err := time.Parse(commentTimeLayout, strings.TrimSpace(stamp))
	if err != nil {
		return Comment{}, false
	}
	return Comment{Author: author, Body: text, CreatedAt: at.UTC()}, true
}

// deterministicCardID names a hand-added card the same way on every read
// until a server write persists the id into the file.
func deterministicCardID(name string, status Status, title string, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d", name, status, title, index)))
	return "k_h" + hex.EncodeToString(sum[:])[:10]
}

func deterministicCommentID(comment Comment) string {
	sum := sha256.Sum256([]byte(comment.CreatedAt.UTC().Format(commentTimeLayout) + "|" + comment.Author + "|" + comment.Body))
	return "cmt_h" + hex.EncodeToString(sum[:])[:8]
}

func knownColumnLabels() string {
	var labels []string
	for _, status := range append(append([]Status{}, ActiveStatuses...), Done) {
		labels = append(labels, statusLabel(status))
	}
	sort.Strings(labels)
	return strings.Join(labels, ", ")
}
