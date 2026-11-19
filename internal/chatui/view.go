package chatui

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/base698/amythest/internal/messages"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	barStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#f1f3f5", Dark: "#f1f3f5"}).Background(lipgloss.AdaptiveColor{Light: "#3b4a6b", Dark: "#2b3550"})
	dim        = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "242", Dark: "245"})
	refStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "244", Dark: "243"})
	noticeSty  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "30", Dark: "73"})
	markStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "179"})
	actTextSty = lipgloss.NewStyle()
	actMsgSty  = lipgloss.NewStyle().Bold(true)
	actHiSty   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "127", Dark: "213"})
	nickColors = []lipgloss.AdaptiveColor{
		{Light: "25", Dark: "75"}, {Light: "28", Dark: "114"}, {Light: "130", Dark: "215"},
		{Light: "90", Dark: "176"}, {Light: "30", Dark: "80"}, {Light: "124", Dark: "210"},
	}
)

// line is one rendered row; msg names the message it belongs to.
type line struct {
	text string
	msg  string
}

func nick(name string, outgoing bool) string {
	name = messages.Plain(name)
	if outgoing {
		return lipgloss.NewStyle().Bold(true).Render(name)
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	return lipgloss.NewStyle().Foreground(nickColors[int(h.Sum32())%len(nickColors)]).Render(name)
}

// indented wraps text after a fixed-width head, irssi style: continuation
// lines line up under the first character of the text.
func indented(head string, text string, width int, msg string) []line {
	pad := lipgloss.Width(head)
	room := max(10, width-pad)
	parts := strings.Split(ansi.Hardwrap(messages.Plain(text), room, true), "\n")
	out := make([]line, 0, len(parts))
	for i, p := range parts {
		if i == 0 {
			out = append(out, line{head + p, msg})
		} else {
			out = append(out, line{strings.Repeat(" ", pad) + p, msg})
		}
	}
	return out
}

func clock(t time.Time) string { return t.Local().Format("15:04") }

func (m *Model) lines(w *window, width int) []line {
	width = max(20, width)
	var out []line
	switch w.kind {
	case kindStatus:
		for _, n := range w.notices {
			out = append(out, indented(clock(n.at)+" ", n.text, width, "")...)
		}
		if len(m.focus) > 0 {
			out = append(out, line{"", ""}, line{dim.Render("Waiting on you:"), ""})
			for _, it := range m.focus {
				where := "  "
				if i, _ := m.window(it.Conversation.ID); i >= 0 {
					where = fmt.Sprintf("%2d", i+1)
				}
				head := fmt.Sprintf(" %s  %s  ", where, messages.Plain(it.Conversation.Name))
				out = append(out, indented(head, it.Message.Category+" · "+messages.Plain(it.Message.Sender)+": "+strings.Join(strings.Fields(it.Message.Body), " "), width, "")...)
			}
		}
		return out
	case kindSearch:
		for _, n := range w.notices {
			out = append(out, indented(clock(n.at)+" ", n.text, width, "")...)
		}
		if !w.loaded {
			return append(out, line{dim.Render("searching…"), ""})
		}
		for i, h := range w.hits {
			head := fmt.Sprintf("%3d. ", i+1)
			where := messages.Plain(h.Conversation.Name) + " · " + messages.Plain(h.Message.Sender) + " · " + h.Message.At.Local().Format("Jan 2 15:04")
			out = append(out, indented(head, where, width, "")...)
			out = append(out, indented(strings.Repeat(" ", len(head)), strings.Join(strings.Fields(h.Snippet), " "), width, "")...)
		}
		if len(w.hits) > 0 {
			out = append(out, line{"", ""}, line{dim.Render("Type a number (or /go N) to open that message."), ""})
		}
		return out
	}
	if w.parent != nil {
		out = append(out, line{dim.Render("-- Slack thread. Text here replies in the thread. --"), ""})
	}
	if w.hasBefore {
		out = append(out, line{dim.Render("-- PgUp for older messages --"), ""})
	}
	if !w.loaded && len(w.msgs) == 0 {
		return append(out, line{dim.Render("loading…"), ""})
	}
	// Messages and this window's notices, in time order.
	type entry struct {
		at  time.Time
		msg *messages.Message
		n   string
	}
	entries := make([]entry, 0, len(w.msgs)+len(w.notices))
	for i := range w.msgs {
		entries = append(entries, entry{at: w.msgs[i].At, msg: &w.msgs[i]})
	}
	for _, n := range w.notices {
		entries = append(entries, entry{at: n.at, n: n.text})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	day := ""
	for _, e := range entries {
		if d := e.at.Local().Format("Mon Jan 2 2006"); d != day {
			if day != "" || len(w.msgs) > 1 {
				out = append(out, line{dim.Render("-- " + d + " --"), ""})
			}
			day = d
		}
		if e.msg == nil {
			out = append(out, indented(clock(e.at)+" ", noticeSty.Render(e.n), width, "")...)
			continue
		}
		msg := e.msg
		ref := w.refFor(msg.ID)
		mark := " "
		if msg.ID == w.highlight {
			mark = markStyle.Render("▶")
		}
		head := clock(msg.At) + mark + refStyle.Render("["+ref+"]") + " <" + nick(msg.Sender, msg.Outgoing) + "> "
		body := msg.Body
		if msg.ReplyTo != "" {
			if r, ok := w.refs[msg.ReplyTo]; ok {
				body = "↳ " + r + ": " + body
			}
		}
		out = append(out, indented(head, body, width, msg.ID)...)
		pad := strings.Repeat(" ", lipgloss.Width(head))
		for _, a := range msg.Attachments {
			out = append(out, line{pad + dim.Render(fmt.Sprintf("[file] %s (%d bytes)", messages.Plain(a.Name), a.Size)), msg.ID})
		}
		if r, ok := w.replies[msg.ID]; ok {
			label := "1 reply"
			if r.Count != 1 {
				label = fmt.Sprintf("%d replies", r.Count)
			}
			out = append(out, line{pad + dim.Render("["+label+" · /thread "+ref+"]"), msg.ID})
		}
	}
	return out
}

func (m *Model) View() string {
	if m.w < 30 || m.h < 6 {
		return "Messages needs at least 30 × 6."
	}
	w := m.cur()
	title := " " + w.name()
	if w.kind == kindConv {
		title += " — " + messages.Destination(w.conv)
		if w.conv.Demo {
			title += " (demo: replies are simulated)"
		}
	}
	top := barStyle.Render(ansi.Truncate(title, m.w, "…") + strings.Repeat(" ", max(0, m.w-lipgloss.Width(ansi.Truncate(title, m.w, "…")))))

	h := m.bodyHeight()
	lines := m.lines(w, m.w)
	end := max(0, len(lines)-w.scroll)
	start := max(0, end-h)
	var b strings.Builder
	b.WriteString(top + "\n")
	for i := 0; i < h; i++ {
		// Short histories sit at the bottom, as a chat log reads.
		s := ""
		if k := end - h + i; k >= start && k < end {
			s = ansi.Truncate(lines[k].text, m.w, "…")
		}
		b.WriteString(s + "\n")
	}
	b.WriteString(barStyle.Render(m.statusBar()) + "\n")
	b.WriteString(m.prompt() + m.input.View())
	return b.String()
}

func (m *Model) statusBar() string {
	w := m.cur()
	left := fmt.Sprintf(" [%s] [%d:%s]", time.Now().Format("15:04"), m.active+1, w.name())
	var act []string
	for i, x := range m.wins {
		if i == m.active || x.level == actNone {
			continue
		}
		n := fmt.Sprint(i + 1)
		switch x.level {
		case actHilight:
			act = append(act, actHiSty.Render(n+"!"))
		case actMsg:
			act = append(act, actMsgSty.Render(n))
		default:
			act = append(act, actTextSty.Render(n))
		}
	}
	if len(act) > 0 {
		left += " [Act: " + strings.Join(act, ",") + "]"
	}
	if w.scroll > 0 {
		left += " -more-"
	}
	if w.loading {
		left += " loading…"
	}
	pad := max(0, m.w-lipgloss.Width(left))
	return ansi.Truncate(left, m.w, "…") + strings.Repeat(" ", pad)
}
