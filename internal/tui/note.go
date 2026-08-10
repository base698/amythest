package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/herdr"
)

var wikilinkRe = regexp.MustCompile(`\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|([^\]]*))?\]\]`)

// noteView is the simplified read view of one note: wrapped markdown,
// / search, tab-cycling through [[wikilinks]] with enter to follow, and
// "a" to hand the note to a running herdr agent as context.
type noteView struct {
	client *apiclient.Client
	note   *apiclient.Note
	lines  []string
	links  []noteLink
	linkAt int // focused link index, -1 = none
	offset int
	busy   bool
	find   finder

	pickingAgent bool
	agents       []herdr.Agent
	agentCursor  int
}

type noteLink struct {
	target string // resolution target (before any |alias)
	label  string // display text
	line   int
}

type agentsListedMsg struct{ agents []herdr.Agent }
type notePromptedMsg struct{ title string }

func newNoteView(client *apiclient.Client, note *apiclient.Note) *noteView {
	v := &noteView{client: client, note: note, linkAt: -1, find: newFinder()}
	v.lines = strings.Split(strings.TrimRight(note.Markdown, "\n"), "\n")
	for i, line := range v.lines {
		for _, m := range wikilinkRe.FindAllStringSubmatch(line, -1) {
			label := m[1]
			if m[2] != "" {
				label = m[2]
			}
			v.links = append(v.links, noteLink{target: strings.TrimSpace(m[1]), label: label, line: i})
		}
	}
	return v
}

func (v *noteView) Title() string   { return v.note.Title }
func (v *noteView) Busy() bool      { return v.busy }
func (v *noteView) Capturing() bool { return v.find.active() || v.pickingAgent }
func (v *noteView) Init() tea.Cmd   { return nil }

func (v *noteView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case openNoteMsg:
		// A followed link resolved; push the new note on the stack.
		v.busy = false
		return v, pushView(newNoteView(v.client, msg.note))

	case agentsListedMsg:
		v.busy = false
		if len(msg.agents) == 0 {
			return v, flash("no running herdr agents")
		}
		v.pickingAgent = true
		v.agents = msg.agents
		v.agentCursor = 0
		return v, nil

	case notePromptedMsg:
		v.busy = false
		return v, flash("note sent to agent ✓")

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.find.active() {
			committed, cmd := v.find.handleKey(msg)
			if committed {
				return v, v.searchJump(1)
			}
			return v, cmd
		}
		if v.pickingAgent {
			return v.handleAgentKey(msg)
		}
		switch msg.String() {
		case "j", "down":
			if v.offset < len(v.lines)-1 {
				v.offset++
			}
		case "k", "up":
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
		case "a":
			v.busy = true
			return v, func() tea.Msg {
				agents, err := herdr.List(context.Background())
				if err != nil {
					return fail(err)
				}
				return agentsListedMsg{agents}
			}
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

func (v *noteView) handleAgentKey(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		v.pickingAgent = false
		return v, nil
	case "j", "down":
		if v.agentCursor < len(v.agents)-1 {
			v.agentCursor++
		}
	case "k", "up":
		if v.agentCursor > 0 {
			v.agentCursor--
		}
	case "enter":
		agent := v.agents[v.agentCursor]
		v.pickingAgent = false
		v.busy = true
		note := v.note
		endpoint := v.client.Endpoint()
		return v, func() tea.Msg {
			text := agentContextPrompt(note, endpoint)
			if err := herdr.Prompt(context.Background(), agent.PaneID, text); err != nil {
				return fail(err)
			}
			return notePromptedMsg{title: note.Title}
		}
	}
	return v, nil
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
	if v.pickingAgent {
		return v.agentPickerView()
	}
	var b strings.Builder
	b.WriteString(" " + columnTitleStyle.Render(v.note.Title) + "  " + dimStyle.Render(v.note.Path) + "\n\n")
	bodyWidth := max(30, width-4)
	bodyHeight := height - 4
	rendered := 0
	for i := v.offset; i < len(v.lines) && rendered < bodyHeight; i++ {
		for _, row := range wrapLine(v.lines[i], bodyWidth) {
			if rendered >= bodyHeight {
				break
			}
			b.WriteString("  " + v.renderNoteLine(row, i) + "\n")
			rendered++
		}
	}
	hint := " tab links · enter follow · a send to agent · / search · j/k scroll · esc back"
	if len(v.links) > 0 && v.linkAt >= 0 {
		hint = fmt.Sprintf(" link %d/%d: %s · enter opens · a send to agent", v.linkAt+1, len(v.links), v.links[v.linkAt].label)
	}
	if bar := v.find.bar(); bar != "" {
		hint = " " + bar
	}
	b.WriteString(dimStyle.Render(hint))
	return b.String()
}

// renderNoteLine styles a display row: wikilinks are underlined, the focused
// link and search hits are highlighted.
func (v *noteView) renderNoteLine(row string, line int) string {
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

func (v *noteView) agentPickerView() string {
	var b strings.Builder
	b.WriteString(" " + columnTitleStyle.Render("Send note to agent") + "  " + dimStyle.Render(v.note.Title) + "\n\n")
	for i, agent := range v.agents {
		prefix := "   "
		title := agent.Title
		if title == "" {
			title = agent.Cwd
		}
		line := fmt.Sprintf("%s [%s] %s  %s", agent.Agent, agent.Status, title, dimStyle.Render(agent.Cwd))
		if i == v.agentCursor {
			prefix = cursorStyle.Render(" ▸ ")
			line = cursorStyle.Render(fmt.Sprintf("%s [%s] %s", agent.Agent, agent.Status, title)) + "  " + dimStyle.Render(agent.Cwd)
		}
		b.WriteString(prefix + line + "\n")
	}
	b.WriteString("\n" + dimStyle.Render(" enter send · j/k choose · esc cancel"))
	return b.String()
}
