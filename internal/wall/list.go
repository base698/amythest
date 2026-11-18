package wall

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// DetectGraphics chooses how to draw images from what the environment says
// about the terminal. "kitty" needs Kitty graphics with Unicode placeholders;
// "blocks" (coloured half blocks) works in any colour terminal. answers
// reports whether the terminal replies to a Kitty graphics query; it is only
// asked where a yes also means placeholders work.
func DetectGraphics(getenv func(string) string, answers func() bool) string {
	switch v := getenv("AMY_WALL_IMAGES"); v {
	case "kitty", "blocks", "off":
		return v
	}
	switch {
	case getenv("TMUX") != "":
		return "blocks" // tmux does not pass image uploads through
	case getenv("HERDR_ENV") == "1":
		// Terminal herdr draws placeholder images itself, but only when the
		// terminal it runs in can show them, which it reports by answering.
		// Checked before the Herdr App: a herdr started inside an App
		// terminal carries that terminal's variables too.
		if answers != nil && answers() {
			return "kitty"
		}
		return "blocks"
	case getenv("HC_TERMINAL_ID") != "":
		return "kitty" // Herdr App terminals
	case getenv("KITTY_WINDOW_ID") != "", getenv("TERM") == "xterm-kitty", getenv("TERM") == "xterm-ghostty", getenv("TERM_PROGRAM") == "ghostty":
		return "kitty"
	}
	return "blocks"
}

// List prints a project's feed as plain text, newest first: one item per
// line with its kind, age, title, source tab and web address.
func List(ctx context.Context, w io.Writer, api API, project string, locate func() Place) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if project == "" {
		o, err := api.WorkOverview(ctx)
		if err != nil {
			return err
		}
		var p Place
		if locate != nil {
			p = locate()
		}
		if project = projectFor(p, o); project == "" {
			return fmt.Errorf("not in a herdr workspace; name one with -project (%s)", strings.Join(o.Projects, ", "))
		}
	}
	s, err := api.WorkProject(ctx, project)
	if err != nil {
		return err
	}
	items := buildFeed(s)
	canvases, err := api.CanvasList(ctx, project)
	if err != nil {
		return fmt.Errorf("load canvases: %w", err)
	}
	items = withCanvases(items, canvases)
	fmt.Fprintf(w, "%s: %d items\n", project, len(items))
	now := time.Now()
	for _, it := range items {
		title := it.Title
		if it.Kind == "commit" {
			title = shortRef(it.Art.Ref) + " " + title
		}
		line := fmt.Sprintf("%-6s %-6s %s", it.Kind, ago(it.When, now), title)
		if it.Tab != "" {
			line += "  [" + it.Tab + "]"
		}
		if u := it.webURL(api.Endpoint()); u != "" {
			line += "  " + u
		}
		fmt.Fprintln(w, line)
	}
	return nil
}
