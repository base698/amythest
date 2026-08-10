package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/herdr"
	"github.com/base698/amythest/internal/kanban/board"
)

// agentPicker is the shared "send to herdr agent" selector used by the note
// and card views: j/k to choose one of the running agents, enter sends,
// esc cancels.
type agentPicker struct {
	active  bool
	subject string // what will be sent, shown in the header
	agents  []herdr.Agent
	cursor  int
}

func (p *agentPicker) open(subject string, agents []herdr.Agent) {
	p.active = true
	p.subject = subject
	p.agents = agents
	p.cursor = 0
}

// handleKey drives the picker and returns the chosen agent on enter.
func (p *agentPicker) handleKey(msg tea.KeyMsg) *herdr.Agent {
	switch msg.String() {
	case "esc", "q", "a":
		p.active = false
	case "j", "down":
		if p.cursor < len(p.agents)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "enter":
		p.active = false
		agent := p.agents[p.cursor]
		return &agent
	}
	return nil
}

func (p *agentPicker) view() string {
	var b strings.Builder
	b.WriteString(" " + columnTitleStyle.Render("Send to agent") + "  " + dimStyle.Render(p.subject) + "\n\n")
	for i, agent := range p.agents {
		prefix := "   "
		title := agent.Title
		if title == "" {
			title = agent.Cwd
		}
		line := fmt.Sprintf("%s [%s] %s  %s", agent.Agent, agent.Status, title, dimStyle.Render(agent.Cwd))
		if i == p.cursor {
			prefix = cursorStyle.Render(" ▸ ")
			line = cursorStyle.Render(fmt.Sprintf("%s [%s] %s", agent.Agent, agent.Status, title)) + "  " + dimStyle.Render(agent.Cwd)
		}
		b.WriteString(prefix + line + "\n")
	}
	b.WriteString("\n" + dimStyle.Render(" enter send · j/k choose · esc cancel"))
	return b.String()
}

// agentPromptSentMsg confirms a context prompt was delivered.
type agentPromptSentMsg struct{ subject string }

func listAgentsCmd(wrap func(agents []herdr.Agent) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		agents, err := herdr.List(context.Background())
		if err != nil {
			return fail(err)
		}
		if len(agents) == 0 {
			return errMsg{fmt.Errorf("no running herdr agents")}
		}
		return wrap(agents)
	}
}

func sendToAgentCmd(agent herdr.Agent, subject, text string) tea.Cmd {
	return func() tea.Msg {
		if err := herdr.Prompt(context.Background(), agent.PaneID, text); err != nil {
			return fail(err)
		}
		return agentPromptSentMsg{subject: subject}
	}
}

// cardContextPrompt frames a kanban card — metadata, description, and
// comments — for the receiving agent.
func cardContextPrompt(card board.Card, boardName, endpoint string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Context from my amythest kanban card %q (board %s, %s/kanban/):\n", card.Title, boardName, endpoint)
	fmt.Fprintf(&b, "status: %s", card.Status)
	if card.Priority != "" {
		fmt.Fprintf(&b, " · priority: %s", card.Priority)
	}
	if card.DueDate != "" {
		fmt.Fprintf(&b, " · due: %s", card.DueDate)
	}
	if len(card.Labels) > 0 {
		fmt.Fprintf(&b, " · labels: %s", strings.Join(card.Labels, ", "))
	}
	if card.Blocked {
		b.WriteString(" · BLOCKED")
	}
	b.WriteString("\n\n")
	description := card.Description
	if len(description) > maxAgentContextBytes {
		description = description[:maxAgentContextBytes] + "\n\n[description truncated]"
	}
	b.WriteString(description)
	if len(card.Comments) > 0 {
		b.WriteString("\n\nComments:\n")
		for _, comment := range card.Comments {
			author := comment.Author
			if author == "" {
				author = "unknown"
			}
			fmt.Fprintf(&b, "- %s (%s): %s\n", author, comment.CreatedAt.Local().Format("2006-01-02"), comment.Body)
		}
	}
	return b.String()
}
