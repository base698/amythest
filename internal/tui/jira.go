package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/herdr"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/source"
	"github.com/base698/amythest/internal/source/jira"
)

// sourceView is the dedicated browsing view for an external source (key 5:
// jira today, azdo tomorrow — it is generic over source.Source). Issues can
// be opened in the browser, commented on, pulled onto an amythest board as a
// card, and sent to a herdr agent. No state ever writes back to the source.
type sourceView struct {
	client *apiclient.Client
	src    source.Source
	items  []source.Item
	cursor int
	busy   bool
	loaded bool
	find   finder
	agents agentPicker

	comment    textinput.Model
	commenting bool

	pulling     bool // board picker open for "pull into board"
	pullBoards  []board.BoardSummary
	pullCursor  int
}

type sourceItemsMsg struct {
	source string
	items  []source.Item
}
type sourceCommentedMsg struct {
	source, id string
}
type sourcePullBoardsMsg struct {
	source string
	boards []board.BoardSummary
}
type sourceAgentsMsg struct {
	source, id string
	agents     []herdr.Agent
}

func newSourceView(client *apiclient.Client, src source.Source) *sourceView {
	ci := textinput.New()
	ci.Prompt = "comment: "
	ci.CharLimit = 4000
	return &sourceView{client: client, src: src, comment: ci}
}

func (v *sourceView) Title() string { return v.src.Name() }
func (v *sourceView) Busy() bool    { return v.busy }
func (v *sourceView) Capturing() bool {
	return v.find.active() || v.commenting || v.pulling || v.agents.active
}

func (v *sourceView) Init() tea.Cmd {
	v.busy = true
	return v.loadCmd()
}

func (v *sourceView) loadCmd() tea.Cmd {
	src := v.src
	return func() tea.Msg {
		lister, ok := src.(source.Lister)
		if !ok {
			return fail(fmt.Errorf("%s does not support browsing", src.Name()))
		}
		items, err := lister.List(context.Background())
		if err != nil {
			return fail(err)
		}
		return sourceItemsMsg{source: src.Name(), items: items}
	}
}

func (v *sourceView) current() *source.Item {
	if v.cursor < 0 || v.cursor >= len(v.items) {
		return nil
	}
	return &v.items[v.cursor]
}

func (v *sourceView) searchTexts() []string {
	texts := make([]string, len(v.items))
	for i, it := range v.items {
		texts[i] = it.Title + " " + it.Meta
	}
	return texts
}

func (v *sourceView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case sourceItemsMsg:
		if msg.source != v.src.Name() {
			return v, nil
		}
		v.busy = false
		v.loaded = true
		v.items = msg.items
		if v.cursor >= len(v.items) {
			v.cursor = max(0, len(v.items)-1)
		}
		return v, nil

	case sourceCommentedMsg:
		if msg.source != v.src.Name() {
			return v, nil
		}
		v.busy = true
		return v, tea.Batch(v.loadCmd(), flash("comment posted to "+msg.id+" ✓"))

	case sourcePullBoardsMsg:
		if msg.source != v.src.Name() {
			return v, nil
		}
		v.busy = false
		if len(msg.boards) == 0 {
			return v, flash("no boards to pull into")
		}
		v.pulling = true
		v.pullBoards = msg.boards
		v.pullCursor = 0
		return v, nil

	case sourceAgentsMsg:
		if msg.source != v.src.Name() {
			return v, nil
		}
		v.busy = false
		if it := v.current(); it != nil && it.ID == msg.id {
			v.agents.open(it.Title, msg.agents)
		}
		return v, nil

	case agentPromptSentMsg:
		if it := v.current(); it == nil || msg.id != it.ID {
			return v, nil
		}
		v.busy = false
		return v, flash("sent to agent ✓")

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.find.active() {
			committed, cmd := v.find.handleKey(msg)
			if committed {
				if hit := findMatch(v.searchTexts(), v.cursor, 1, v.find.query); hit >= 0 {
					v.cursor = hit
				} else {
					return v, flash("no match: " + v.find.query)
				}
			}
			return v, cmd
		}
		if v.commenting {
			return v.handleCommentKey(msg)
		}
		if v.pulling {
			return v.handlePullKey(msg)
		}
		if v.agents.active {
			agent := v.agents.handleKey(msg)
			if agent == nil {
				return v, nil
			}
			it := v.current()
			if it == nil {
				return v, nil
			}
			subject, body, err := v.src.AgentContext(*it)
			if err != nil {
				return v, flash(err.Error())
			}
			v.busy = true
			return v, sendToAgentCmd(*agent, it.ID, subject, body)
		}
		switch msg.String() {
		case "j", "down":
			if v.cursor < len(v.items)-1 {
				v.cursor++
			}
		case "k", "up":
			if v.cursor > 0 {
				v.cursor--
			}
		case "/":
			return v, v.find.start()
		case "n", "N":
			dir := 1
			if msg.String() == "N" {
				dir = -1
			}
			if hit := findMatch(v.searchTexts(), v.cursor, dir, v.find.query); hit >= 0 {
				v.cursor = hit
			}
		case "r":
			v.busy = true
			return v, v.loadCmd()
		case "o", "enter":
			if it := v.current(); it != nil && it.URL != "" {
				return v, openURLCmd(it.URL)
			}
		case "c":
			if _, ok := v.src.(source.Commenter); !ok {
				return v, flash(v.src.Name() + " does not support comments")
			}
			if v.current() == nil {
				return v, nil
			}
			v.commenting = true
			v.comment.SetValue("")
			return v, v.comment.Focus()
		case "p":
			if v.current() == nil || v.busy {
				return v, nil
			}
			v.busy = true
			client, name := v.client, v.src.Name()
			return v, func() tea.Msg {
				boards, err := client.ListBoards(context.Background())
				if err != nil {
					return fail(err)
				}
				visible := boards[:0]
				for _, b := range boards {
					if !b.Archived {
						visible = append(visible, b)
					}
				}
				return sourcePullBoardsMsg{source: name, boards: visible}
			}
		case "a":
			it := v.current()
			if it == nil || v.busy {
				return v, nil
			}
			v.busy = true
			name, id := v.src.Name(), it.ID
			return v, listAgentsCmd(func(agents []herdr.Agent) tea.Msg {
				return sourceAgentsMsg{source: name, id: id, agents: agents}
			})
		}
	}
	return v, nil
}

func (v *sourceView) handleCommentKey(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		v.commenting = false
		v.comment.Blur()
		return v, nil
	case tea.KeyEnter:
		body := strings.TrimSpace(v.comment.Value())
		v.commenting = false
		v.comment.Blur()
		it := v.current()
		if body == "" || it == nil {
			return v, nil
		}
		commenter := v.src.(source.Commenter)
		v.busy = true
		name, item := v.src.Name(), *it
		return v, func() tea.Msg {
			if err := commenter.Comment(context.Background(), item, body); err != nil {
				return fail(err)
			}
			return sourceCommentedMsg{source: name, id: item.ID}
		}
	}
	var cmd tea.Cmd
	v.comment, cmd = v.comment.Update(msg)
	return v, cmd
}

func (v *sourceView) handlePullKey(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		v.pulling = false
		return v, nil
	case "j", "down":
		if v.pullCursor < len(v.pullBoards)-1 {
			v.pullCursor++
		}
	case "k", "up":
		if v.pullCursor > 0 {
			v.pullCursor--
		}
	case "enter":
		v.pulling = false
		it := v.current()
		if it == nil {
			return v, nil
		}
		boardName := v.pullBoards[v.pullCursor].Name
		v.busy = true
		client, item := v.client, *it
		return v, func() tea.Msg {
			card, err := client.CreateCard(context.Background(), boardName, item.Title, board.Triage)
			if err != nil {
				return fail(err)
			}
			desc := pullDescription(item)
			if card, err = client.PatchCard(context.Background(), boardName, card.ID, apiclient.CardPatch{Description: &desc}); err != nil {
				return fail(err)
			}
			return cardCreatedMsg{board: boardName, card: card}
		}
	}
	return v, nil
}

// pullDescription carries the external reference into the card so a human
// (or a future write-back feature) can trace it: "Source: jira:DEMO-101".
func pullDescription(it source.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source: %s:%s\n", it.Source, it.ID)
	if it.URL != "" {
		b.WriteString(it.URL + "\n")
	}
	if issueBody := itemDescription(it); issueBody != "" {
		b.WriteString("\n" + issueBody)
	}
	return b.String()
}

// itemDescription extracts a body when the payload carries one.
func itemDescription(it source.Item) string {
	switch p := it.Payload.(type) {
	case jira.Issue:
		return p.Description
	default:
		return ""
	}
}

func (v *sourceView) View(width, height int) string {
	if v.agents.active {
		return v.agents.view()
	}
	if v.pulling {
		var b strings.Builder
		b.WriteString(" " + columnTitleStyle.Render("Pull into board") + "  " + dimStyle.Render(v.current().Title) + "\n\n")
		for i, bs := range v.pullBoards {
			name := bs.DisplayName
			if name == "" {
				name = bs.Name
			}
			if i == v.pullCursor {
				b.WriteString(cursorStyle.Render(" ▸ "+name) + "\n")
			} else {
				b.WriteString("   " + name + "\n")
			}
		}
		b.WriteString("\n" + dimStyle.Render(" enter pull (card in Triage) · esc cancel"))
		return b.String()
	}
	var b strings.Builder
	if !v.loaded {
		return "\n  loading " + v.src.Name() + "…"
	}
	if len(v.items) == 0 {
		b.WriteString("\n  no items\n")
	}
	b.WriteString("\n")
	for i, it := range v.items {
		prefix := "   "
		title := highlight(it.Title, v.find.query)
		if i == v.cursor {
			prefix = cursorStyle.Render(" ▸ ")
			title = cursorStyle.Render(it.Title)
		}
		var meta []string
		if it.Due != "" {
			meta = append(meta, dueStyle.Render(it.Due))
		}
		meta = append(meta, dimStyle.Render(it.Meta))
		b.WriteString(prefix + title + "  " + strings.Join(meta, " ") + "\n")
	}
	hint := dimStyle.Render(" o/enter open · c comment · p pull to board · a send to agent · / search · r refresh")
	if bar := v.find.bar(); bar != "" {
		hint = " " + bar
	}
	if v.commenting {
		hint = " " + v.comment.View()
	}
	b.WriteString("\n" + hint)
	return b.String()
}
