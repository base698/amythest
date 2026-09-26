package tui

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/herdr"
	"github.com/base698/amythest/internal/tasks"
)

var wikilinkRe = regexp.MustCompile(`\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|([^\]]*))?\]\]`)

// noteView is the simplified read view of one note: wrapped markdown,
// / search, tab-cycling through [[wikilinks]] with enter to follow, "b" for
// backlinks, "e" to edit in $EDITOR (saved back with a version lock), and
// "a" to hand the note to a running herdr agent as context.

type noteEditorDoneMsg struct {
	slug, path string
	err        error
}

type noteSavedMsg struct{ note *apiclient.Note }

// noteReloadedMsg re-reads a note after something else rewrote it.
type noteReloadedMsg struct{ note *apiclient.Note }

// taskBlock is a ```tasks fenced query in the note, rendered as live
// results instead of raw query text — the dataview experience the web UI
// gives these blocks.
type taskBlock struct {
	start, end int // 0-based line span, fences included
	query      string
	groups     []apiclient.TaskGroup
	loaded     bool
	err        string
}

type noteTasksMsg struct {
	slug   string
	block  int
	groups []apiclient.TaskGroup
	err    string
}

// parseTaskBlocks finds ```tasks fences.
func parseTaskBlocks(lines []string) []taskBlock {
	var blocks []taskBlock
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```tasks" {
			continue
		}
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				end = j
				break
			}
		}
		if end == -1 {
			break
		}
		blocks = append(blocks, taskBlock{
			start: i, end: end,
			query: strings.Join(lines[i+1:end], "\n"),
		})
		i = end
	}
	return blocks
}

type noteView struct {
	client *apiclient.Client
	note   *apiclient.Note
	lines  []string
	links  []noteLink
	linkAt int // focused link index, -1 = none
	offset int
	busy   bool
	find   finder
	agents agentPicker

	tags     []string // from the content index, shown in the header
	blOpen   bool     // backlinks panel
	blLinks  []string
	blCursor int

	blocks []taskBlock // live-rendered ```tasks queries

	// Checkbox lines in the note body are actionable, not just text: t
	// grabs them, j/k walk them, space toggles, D cancels then deletes. The
	// tasks are parsed from the note's own markdown, so their Line/Text
	// match exactly what the server's mutation guards compare against;
	// Version is the note's whole-file hash, which is the same lock.
	tasks     []tasks.Task
	taskAt    int // focused task index, -1 = none
	del       confirm
	delTarget tasks.Task
}

// noteTasks parses a note's checkbox lines, stamped with the note's
// whole-file version so a mutation carries the right optimistic lock.
func noteTasks(note *apiclient.Note) []tasks.Task {
	parsed, _, _ := tasks.ParseFile(note.Slug, note.Path, []byte(note.Markdown))
	for i := range parsed {
		parsed[i].Version = note.Version
	}
	return parsed
}

type noteMetaMsg struct {
	slug      string
	tags      []string
	backlinks []string
}

type noteLink struct {
	target string // resolution target (before any |alias)
	label  string // display text
	line   int
}

type noteAgentsMsg struct {
	slug   string
	agents []herdr.Agent
}

func newNoteView(client *apiclient.Client, note *apiclient.Note) *noteView {
	v := &noteView{client: client, find: newFinder()}
	v.rebuild(note)
	return v
}

// rebuild re-derives everything the view reads from the note's markdown.
// Used on open and after any write, so a mutation can never leave stale
// line numbers or a stale version lock behind.
func (v *noteView) rebuild(note *apiclient.Note) {
	v.note = note
	v.lines = strings.Split(strings.TrimRight(note.Markdown, "\n"), "\n")
	v.links = nil
	for i, line := range v.lines {
		for _, m := range wikilinkRe.FindAllStringSubmatch(line, -1) {
			label := m[1]
			if m[2] != "" {
				label = m[2]
			}
			v.links = append(v.links, noteLink{target: strings.TrimSpace(m[1]), label: label, line: i})
		}
	}
	v.linkAt = -1
	v.blocks = parseTaskBlocks(v.lines)
	v.tasks = noteTasks(note)
	v.taskAt = -1
}

func (v *noteView) Title() string   { return v.note.Title }
func (v *noteView) Busy() bool      { return v.busy }
func (v *noteView) Capturing() bool { return v.find.active() || v.agents.active || v.del.active }

// focusedTask is the task the cursor is on, or nil when none is focused.
func (v *noteView) focusedTask() *tasks.Task {
	if v.taskAt < 0 || v.taskAt >= len(v.tasks) {
		return nil
	}
	return &v.tasks[v.taskAt]
}

// firstTaskFrom picks the task to grab when t is pressed: the first one
// at or below what is currently on screen, so focus lands where you are
// reading rather than jumping to the top of a long daily note.
func (v *noteView) firstTaskFrom(offset int) int {
	for i, t := range v.tasks {
		if t.Line-1 >= offset {
			return i
		}
	}
	return 0
}

// scrollToTask brings the focused task into view; task lines are 1-based.
// The view has no height here, so it only guarantees the task is at or
// after the top of the body — the render clamps the rest.
func (v *noteView) scrollToTask() {
	t := v.focusedTask()
	if t == nil {
		return
	}
	if line := t.Line - 1; line < v.offset {
		v.offset = max(0, line-2)
	}
}

// reloadCmd re-reads the note after a write so line numbers and the
// version lock match the file again.
func (v *noteView) reloadCmd() tea.Cmd {
	client, slug := v.client, v.note.Slug
	v.busy = true
	return func() tea.Msg {
		note, err := client.GetNote(context.Background(), slug)
		if err != nil {
			return fail(err)
		}
		return noteReloadedMsg{note: note}
	}
}

// Init fetches tags + backlinks from the cached content index — cheap, and
// the panel/header appear as soon as it lands.
func (v *noteView) Init() tea.Cmd {
	client, slug := v.client, v.note.Slug
	if client == nil {
		return nil
	}
	cmds := []tea.Cmd{func() tea.Msg {
		ctx := context.Background()
		index, err := client.ContentIndex(ctx)
		if err != nil {
			return noteMetaMsg{slug: slug} // header just stays plain
		}
		backlinks, _ := client.Backlinks(ctx, slug)
		return noteMetaMsg{slug: slug, tags: index[slug].Tags, backlinks: backlinks}
	}}
	for i, block := range v.blocks {
		idx, query := i, block.query
		cmds = append(cmds, func() tea.Msg {
			groups, err := client.ListTasks(context.Background(), query)
			if err != nil {
				return noteTasksMsg{slug: slug, block: idx, err: err.Error()}
			}
			return noteTasksMsg{slug: slug, block: idx, groups: groups}
		})
	}
	return tea.Batch(cmds...)
}

func (v *noteView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case openNoteMsg:
		// A followed link resolved; push the new note on the stack.
		v.busy = false
		return v, pushView(newNoteView(v.client, msg.note))

	case noteMetaMsg:
		if msg.slug != v.note.Slug {
			return v, nil
		}
		v.tags = msg.tags
		v.blLinks = msg.backlinks
		return v, nil

	case noteTasksMsg:
		if msg.slug != v.note.Slug || msg.block >= len(v.blocks) {
			return v, nil
		}
		v.blocks[msg.block].loaded = true
		v.blocks[msg.block].groups = msg.groups
		v.blocks[msg.block].err = msg.err
		return v, nil

	case noteEditorDoneMsg:
		if msg.slug != v.note.Slug {
			return v, nil
		}
		edited, readErr := os.ReadFile(msg.path)
		if msg.err != nil {
			os.Remove(msg.path)
			return v, func() tea.Msg { return fail(msg.err) }
		}
		if readErr != nil {
			os.Remove(msg.path)
			return v, func() tea.Msg { return fail(readErr) }
		}
		if string(edited) == v.note.Markdown {
			os.Remove(msg.path)
			return v, flash("no changes")
		}
		v.busy = true
		client, slug, version := v.client, v.note.Slug, v.note.Version
		content, path := string(edited), msg.path
		return v, func() tea.Msg {
			if err := client.SaveNote(context.Background(), slug, content, version); err != nil {
				return fail(fmt.Errorf("%w — your edit is kept at %s", err, path))
			}
			os.Remove(path)
			note, err := client.GetNote(context.Background(), slug)
			if err != nil {
				return fail(err)
			}
			return noteSavedMsg{note: note}
		}

	case noteSavedMsg:
		if msg.note.Slug != v.note.Slug {
			return v, nil
		}
		v.busy = false
		v.rebuild(msg.note)
		return v, tea.Batch(v.Init(), flash("note saved ✓"))

	case noteReloadedMsg:
		if msg.note.Slug != v.note.Slug {
			return v, nil
		}
		v.busy = false
		v.rebuild(msg.note)
		return v, v.Init()

	// A task mutation rewrote the file: our line numbers and version lock
	// are now stale, so re-read rather than patch in place.
	case taskToggledMsg:
		if msg.slug != v.note.Slug {
			return v, nil
		}
		return v, v.reloadCmd()

	case taskCancelledMsg:
		if msg.slug != v.note.Slug {
			return v, nil
		}
		return v, v.reloadCmd()

	case taskPurgedMsg:
		if msg.slug != v.note.Slug {
			return v, nil
		}
		return v, v.reloadCmd()

	case noteAgentsMsg:
		if msg.slug != v.note.Slug {
			return v, nil
		}
		v.busy = false
		v.agents.open(v.note.Title, msg.agents)
		return v, nil

	case agentPromptSentMsg:
		if msg.id != v.note.Slug {
			return v, nil
		}
		v.busy = false
		return v, flash("sent to agent ✓")

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.del.active {
			if v.del.handleKey(msg) {
				v.busy = true
				return v, deleteTaskCmd(v.client, v.delTarget)
			}
			return v, nil
		}
		if v.find.active() {
			committed, cmd := v.find.handleKey(msg)
			if committed {
				return v, v.searchJump(1)
			}
			return v, cmd
		}
		if v.agents.active {
			agent := v.agents.handleKey(msg)
			if agent == nil {
				return v, nil
			}
			v.busy = true
			return v, sendToAgentCmd(*agent, v.note.Slug, v.note.Title, agentContextPrompt(v.note, v.client.Endpoint()))
		}
		if v.blOpen {
			switch msg.String() {
			case "esc", "b", "q":
				v.blOpen = false
				return v, nil
			case "j", "down":
				if v.blCursor < len(v.blLinks)-1 {
					v.blCursor++
				}
			case "k", "up":
				if v.blCursor > 0 {
					v.blCursor--
				}
			case "enter":
				if v.blCursor < len(v.blLinks) {
					v.busy = true
					v.blOpen = false
					return v, openNoteCmd(v.client, v.blLinks[v.blCursor])
				}
			}
			return v, nil
		}
		switch msg.String() {
		case "b":
			if len(v.blLinks) == 0 {
				return v, flash("no backlinks to this note")
			}
			v.blOpen = true
			v.blCursor = 0
			return v, nil
		case "t":
			if len(v.tasks) == 0 {
				return v, flash("no tasks in this note")
			}
			if v.taskAt >= 0 {
				v.taskAt = -1 // t again releases the tasks, j/k scroll as usual
				return v, nil
			}
			v.taskAt = v.firstTaskFrom(v.offset)
			v.scrollToTask()
			return v, nil
		case " ":
			t := v.focusedTask()
			if t == nil {
				return v, flash("press t to grab this note's tasks, then space toggles")
			}
			if v.busy {
				return v, nil
			}
			switch t.Status {
			case tasks.StatusOpen:
				v.busy = true
				return v, toggleTaskCmd(v.client, *t, true)
			case tasks.StatusDone:
				v.busy = true
				return v, toggleTaskCmd(v.client, *t, false)
			default:
				return v, flash("only open/done tasks can be toggled")
			}
		case "D":
			t := v.focusedTask()
			if t == nil {
				return v, flash("press t to grab this note's tasks, then D deletes")
			}
			v.delTarget = *t
			if t.Status == tasks.StatusCancelled {
				v.del.open(fmt.Sprintf("permanently delete cancelled task %q?", t.Text))
			} else {
				v.del.open(fmt.Sprintf("cancel task %q? (D again on it deletes permanently)", t.Text))
			}
			return v, nil
		}
		switch msg.String() {
		case "j", "down":
			if v.taskAt >= 0 {
				if v.taskAt < len(v.tasks)-1 {
					v.taskAt++
					v.scrollToTask()
				}
				return v, nil
			}
			if v.offset < len(v.lines)-1 {
				v.offset++
			}
		case "k", "up":
			if v.taskAt >= 0 {
				if v.taskAt > 0 {
					v.taskAt--
					v.scrollToTask()
				}
				return v, nil
			}
			if v.offset > 0 {
				v.offset--
			}
		case "g":
			v.offset = 0
		case "G":
			v.offset = max(0, len(v.lines)-10)
		case "tab":
			if len(v.links) > 0 {
				v.linkAt = (v.linkAt + 1) % len(v.links)
				v.offset = clampOffset(v.links[v.linkAt].line, v.offset)
			}
		case "shift+tab":
			if len(v.links) > 0 {
				if v.linkAt <= 0 {
					v.linkAt = len(v.links)
				}
				v.linkAt--
				v.offset = clampOffset(v.links[v.linkAt].line, v.offset)
			}
		case "enter":
			if v.linkAt >= 0 && v.linkAt < len(v.links) {
				v.busy = true
				return v, openNoteCmd(v.client, v.links[v.linkAt].target)
			}
		case "e":
			if v.busy {
				return v, nil
			}
			tmp, err := os.CreateTemp("", "amy-note-*.md")
			if err != nil {
				return v, func() tea.Msg { return fail(err) }
			}
			if _, err := tmp.WriteString(v.note.Markdown); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				return v, func() tea.Msg { return fail(err) }
			}
			tmp.Close()
			slug := v.note.Slug
			return v, tea.ExecProcess(editorExec(tmp.Name()), func(err error) tea.Msg {
				return noteEditorDoneMsg{slug: slug, path: tmp.Name(), err: err}
			})
		case "a":
			v.busy = true
			slug := v.note.Slug
			return v, listAgentsCmd(func(agents []herdr.Agent) tea.Msg {
				return noteAgentsMsg{slug: slug, agents: agents}
			})
		case "/":
			return v, v.find.start()
		case "n", "N":
			if v.find.query != "" {
				dir := 1
				if msg.String() == "N" {
					dir = -1
				}
				return v, v.searchJump(dir)
			}
		}
	}
	return v, nil
}

func clampOffset(line, offset int) int {
	if line < offset {
		return line
	}
	return offset
}

const maxAgentContextBytes = 32 * 1024

// agentContextPrompt frames the note so the receiving agent knows what it is
// and where it came from; oversized notes are truncated with a pointer back.
func agentContextPrompt(note *apiclient.Note, endpoint string) string {
	markdown := note.Markdown
	if len(markdown) > maxAgentContextBytes {
		markdown = markdown[:maxAgentContextBytes] + "\n\n[truncated — full note at the link above]"
	}
	return fmt.Sprintf("Context from my amythest note %q (%s/%s):\n\n%s",
		note.Title, endpoint, note.Slug, markdown)
}

func (v *noteView) searchJump(dir int) tea.Cmd {
	hit := findMatch(v.lines, v.offset, dir, v.find.query)
	if hit < 0 {
		return flash("no match: " + v.find.query)
	}
	v.offset = hit
	return nil
}

func (v *noteView) View(width, height int) string {
	if v.agents.active {
		return v.agents.view()
	}
	if v.blOpen {
		var b strings.Builder
		b.WriteString(" " + columnTitleStyle.Render("Backlinks") + "  " + dimStyle.Render("→ "+v.note.Title) + "\n\n")
		for i, slug := range v.blLinks {
			if i == v.blCursor {
				b.WriteString(cursorStyle.Render(" ▸ "+slug) + "\n")
			} else {
				b.WriteString("   " + slug + "\n")
			}
		}
		b.WriteString("\n" + dimStyle.Render(" enter open · j/k choose · b/esc close"))
		return b.String()
	}
	var b strings.Builder
	header := " " + columnTitleStyle.Render(v.note.Title) + "  " + dimStyle.Render(v.note.Path)
	if len(v.tags) > 0 {
		header += "  " + dueStyle.Render("#"+strings.Join(v.tags, " #"))
	}
	if len(v.blLinks) > 0 {
		header += "  " + dimStyle.Render(fmt.Sprintf("← %d backlinks (b)", len(v.blLinks)))
	}
	b.WriteString(header + "\n\n")
	bodyWidth := max(30, width-4)
	bodyHeight := height - 4
	if v.del.active {
		bodyHeight-- // the confirm bar takes a row from the body
	}
	rendered := 0
	for i := v.offset; i < len(v.lines) && rendered < bodyHeight; i++ {
		if blk := v.blockStartingAt(i); blk != nil {
			for _, row := range v.renderTaskBlock(blk, bodyWidth) {
				if rendered >= bodyHeight {
					break
				}
				b.WriteString("  " + row + "\n")
				rendered++
			}
			i = blk.end
			continue
		}
		if v.insideBlock(i) {
			continue // offset landed mid-block; its start already rendered
		}
		for _, row := range wrapLine(v.lines[i], bodyWidth) {
			if rendered >= bodyHeight {
				break
			}
			b.WriteString("  " + v.renderNoteLine(row, i) + "\n")
			rendered++
		}
	}
	hint := " tab links · enter follow · a send to agent · / search · j/k scroll · esc back"
	if len(v.tasks) > 0 {
		hint = fmt.Sprintf(" tab links · t tasks (%d) · a agent · / search · j/k scroll · esc back", len(v.tasks))
	}
	if len(v.links) > 0 && v.linkAt >= 0 {
		hint = fmt.Sprintf(" link %d/%d: %s · enter opens · a send to agent", v.linkAt+1, len(v.links), v.links[v.linkAt].label)
	}
	if t := v.focusedTask(); t != nil {
		hint = fmt.Sprintf(" task %d/%d · j/k move · space toggle · D delete · t release", v.taskAt+1, len(v.tasks))
	}
	if bar := v.find.bar(); bar != "" {
		hint = " " + bar
	}
	if v.del.active {
		b.WriteString(v.del.bar() + "\n")
	}
	b.WriteString(dimStyle.Render(hint))
	return b.String()
}

func (v *noteView) blockStartingAt(line int) *taskBlock {
	for i := range v.blocks {
		if v.blocks[i].start == line {
			return &v.blocks[i]
		}
	}
	return nil
}

func (v *noteView) insideBlock(line int) bool {
	for _, blk := range v.blocks {
		if line > blk.start && line <= blk.end {
			return true
		}
	}
	return false
}

// renderTaskBlock replaces a ```tasks fence with its live results.
func (v *noteView) renderTaskBlock(blk *taskBlock, width int) []string {
	switch {
	case blk.err != "":
		return []string{dimStyle.Render("┄ tasks query"), blockedStyle.Render("  " + blk.err)}
	case !blk.loaded:
		return []string{dimStyle.Render("┄ tasks query — loading…")}
	}
	total := 0
	for _, g := range blk.groups {
		total += len(g.Tasks)
	}
	rows := []string{dimStyle.Render(fmt.Sprintf("┄ tasks query — %d result(s)", total))}
	for gi := range blk.groups {
		g := &blk.groups[gi]
		if g.Name != "" {
			rows = append(rows, groupHeaderStyle.Render(g.Name))
		}
		for ti := range g.Tasks {
			rows = append(rows, renderTaskLine(&g.Tasks[ti], false, width))
		}
	}
	if total == 0 {
		rows = append(rows, dimStyle.Render("  (no matching tasks)"))
	}
	return rows
}

// renderNoteLine styles a display row: wikilinks are underlined, the focused
// link and search hits are highlighted.
func (v *noteView) renderNoteLine(row string, line int) string {
	// The focused task owns its whole line: checkbox syntax is not a
	// wikilink, and marking it up as a link would be misleading.
	if t := v.focusedTask(); t != nil && t.Line-1 == line {
		return cursorStyle.Render(row)
	}
	styled := wikilinkRe.ReplaceAllStringFunc(row, func(match string) string {
		m := wikilinkRe.FindStringSubmatch(match)
		label := m[1]
		if m[2] != "" {
			label = m[2]
		}
		if v.linkAt >= 0 && v.linkAt < len(v.links) && v.links[v.linkAt].line == line &&
			strings.TrimSpace(m[1]) == v.links[v.linkAt].target {
			return searchHitStyle.Render("[[" + label + "]]")
		}
		return linkStyle.Render("[[" + label + "]]")
	})
	if v.find.query != "" && !strings.Contains(styled, "\x1b") {
		styled = highlight(styled, v.find.query)
	}
	return styled
}
