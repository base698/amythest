package wall

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
)

// The summary heads the wall: what the workspace's agents are doing now and
// how much they produced over the last days. It mirrors the web Project view.

var (
	working = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "26", Dark: "75"})
	blocked = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})
	done    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "114"})
)

const (
	summaryDays = 4
	summaryTabs = 6
)

// counts are the state line's numbers.
func counts(s *apiclient.WorkSnapshot) (nWorking, nBlocked, nReview int) {
	for _, x := range s.Slots {
		switch x.Status {
		case "working":
			nWorking++
		case "blocked":
			nBlocked++
		}
	}
	for _, r := range s.Runs {
		if r.Finished != "" && !r.Historical && r.OutputVersion > r.Reviewed {
			nReview++
		}
	}
	return
}

// openTabs are the workspace's open agent panes, ordered by tab name with
// numbered tabs in numeric order.
func openTabs(s *apiclient.WorkSnapshot) []apiclient.WorkSlot {
	var tabs []apiclient.WorkSlot
	for _, x := range s.Slots {
		if x.Status != "gone" {
			if x.Tab == "" {
				x.Tab = x.Pane
			}
			tabs = append(tabs, x)
		}
	}
	sort.SliceStable(tabs, func(i, j int) bool {
		a, aerr := strconv.Atoi(tabs[i].Tab)
		b, berr := strconv.Atoi(tabs[j].Tab)
		switch {
		case aerr == nil && berr == nil:
			return a < b
		case aerr == nil:
			return true
		case berr == nil:
			return false
		}
		return tabs[i].Tab < tabs[j].Tab
	})
	return tabs
}

type dayCount struct {
	label          string
	commits, media int
}

// days counts commits and media over the last days ending today, oldest
// first, by the viewer's local days.
func days(items []Item, now time.Time, n int) []dayCount {
	out := make([]dayCount, n)
	index := map[string]int{}
	for i := 0; i < n; i++ {
		d := time.Date(now.Year(), now.Month(), now.Day()-(n-1-i), 0, 0, 0, 0, now.Location())
		out[i].label = d.Format("Mon 2")
		index[d.Format("2006-01-02")] = i
	}
	for _, it := range items {
		if it.When.IsZero() {
			continue
		}
		i, ok := index[it.When.In(now.Location()).Format("2006-01-02")]
		if !ok {
			continue
		}
		switch it.Kind {
		case "commit":
			out[i].commits++
		case "image", "video":
			out[i].media++
		}
	}
	return out
}

// summaryLines renders the summary for a pane width cells wide. cards maps
// card ids to titles for panes assigned to a card.
func summaryLines(s *apiclient.WorkSnapshot, items []Item, cards map[string]string, now time.Time, width int) []string {
	if s == nil || width < 20 {
		return nil
	}
	var lines []string
	w, b, r := counts(s)
	lines = append(lines, " "+working.Render("●")+fmt.Sprintf(" %d working  ", w)+blocked.Render("●")+fmt.Sprintf(" %d blocked  ", b)+done.Render("●")+fmt.Sprintf(" %d to review", r))

	agent := map[string]string{}
	for _, x := range s.Sessions {
		agent[x.ID] = x.Agent
	}
	tabs := openTabs(s)
	for i, x := range tabs {
		if i == summaryTabs {
			lines = append(lines, "  "+dim.Render(fmt.Sprintf("and %d more tabs", len(tabs)-summaryTabs)))
			break
		}
		name := agent[x.Session]
		if name == "" {
			name = "agent"
		}
		state := name + " · " + x.Status
		style := dim
		switch x.Status {
		case "working":
			style = working
		case "blocked":
			style = blocked
		case "done":
			style = done
		}
		card := dim.Render("no card")
		rest := width - 2 - 8 - lipgloss.Width(state) - 2
		if x.Binding != nil {
			title := cards[x.Binding.Card]
			if title == "" {
				title = "card " + x.Binding.Card
			}
			card = clip(title, max(6, rest))
		}
		lines = append(lines, "  "+dim.Render(fmt.Sprintf("%-7s", clip(x.Tab, 7)))+" "+style.Render(state)+"  "+card)
	}
	if len(tabs) == 0 {
		lines = append(lines, "  "+dim.Render("no agent tabs open in this workspace"))
	}

	// The last days as columns: name, a bar relative to the busiest, counts.
	ds := days(items, now, summaryDays)
	col := (width - 1) / summaryDays
	busiest := 1
	for _, d := range ds {
		busiest = max(busiest, d.commits+d.media)
	}
	var names, bars, nums strings.Builder
	for _, d := range ds {
		names.WriteString(pad(strong.Render(clip(d.label, col-1)), col))
		barW := max(2, col-2)
		filled := (d.commits + d.media) * barW / busiest
		if d.commits+d.media > 0 && filled == 0 {
			filled = 1
		}
		bars.WriteString(pad(accent.Render(strings.Repeat("━", filled))+dim.Render(strings.Repeat("─", barW-filled)), col))
		text := "–"
		if d.commits+d.media > 0 {
			text = fmt.Sprintf("%dc %dm", d.commits, d.media)
		}
		nums.WriteString(pad(dim.Render(clip(text, col-1)), col))
	}
	lines = append(lines, "", " "+names.String(), " "+bars.String(), " "+nums.String(), "")
	return lines
}

// pad extends styled text with spaces to w cells.
func pad(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
