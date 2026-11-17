package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/find"
	"github.com/base698/amythest/internal/source"
	"github.com/base698/amythest/internal/source/azboards"
	"github.com/base698/amythest/internal/source/jira"
	"github.com/base698/amythest/internal/wall"
)

// `amy find` is one search across the vault (notes, cards, tasks), what
// agents made, tickets from the configured trackers, and the text on screen
// in open terminals. Enter shows a commit or image right there, goes to the
// pane for text on screen, and opens notes, cards, tasks and tickets in the
// browser; Tab goes to the pane a result came from when it is still open.
//
//	amy find                 open the search
//	amy find gait timing     open it with a query
//	amy find -pane           opened by a key in the Herdr App: the pane
//	                         closes when the search ends
func runFind(args []string) {
	fs := flag.NewFlagSet("find", flag.ExitOnError)
	ownPane := fs.Bool("pane", false, "the search owns its Herdr App pane and closes it when done")
	noScreens := fs.Bool("no-screens", false, "do not search the text of open terminals")
	endpoint := fs.String("endpoint", "", "amythest server base URL")
	config := fs.String("config", "", "path to cli.yaml")
	fs.Parse(args)

	var pass []string
	if *endpoint != "" {
		pass = append(pass, "-endpoint", *endpoint)
	}
	if *config != "" {
		pass = append(pass, "-config", *config)
	}
	cfg, err := apiclient.LoadConfig(pass)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	client := apiclient.New(cfg)

	opts := find.Options{Query: strings.Join(fs.Args(), " "), Local: find.LocalCollector()}
	if !*noScreens && os.Getenv("HC_TERMINAL_ID") != "" {
		self := os.Getenv("HC_TERMINAL_ID")
		opts.Screens = func() []find.Screen {
			var out []find.Screen
			for _, s := range find.ReadScreens(nil, 300) {
				if s.Pane != self { // not the search's own screen
					out = append(out, s)
				}
			}
			return out
		}
	}
	// Tickets come from the same tracker sources the main screen lists.
	var trackers []source.Lister
	if jcfg, ok, err := jira.LoadConfig(cfg.File); err == nil && ok {
		trackers = append(trackers, jira.New(jcfg, cfg.EnvFile))
	}
	if acfg, ok, err := azboards.LoadConfig(cfg.File); err == nil && ok {
		trackers = append(trackers, azboards.New(acfg))
	}
	if len(trackers) > 0 {
		opts.Tickets = func() []find.Ticket {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			var out []find.Ticket
			for _, t := range trackers {
				items, _ := t.List(ctx) // a tracker that is down just has no tickets to show
				for _, it := range items {
					out = append(out, find.Ticket{Source: it.Source, ID: it.ID, Title: it.Title, Meta: it.Meta, URL: it.URL})
				}
			}
			return out
		}
	}
	pane := ""
	if *ownPane {
		pane, _ = wall.AppPane(os.Getenv("HC_TERMINAL_ID"), nil)
	}
	final, err := tea.NewProgram(find.New(client, opts), tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	choice := find.Chosen(final)
	// Act on the choice first and close the search's own pane last: closing
	// the pane ends this program, so anything after it may never run.
	failed := false
	switch {
	case choice == nil:
	case choice.View != nil:
		// Show the commit or image right here: the wall takes over this
		// terminal, opened on that item, and closes the pane when quit.
		v := choice.View
		err := showWall(client, "auto", wall.Options{Project: v.Project, Show: &wall.Want{Kind: v.Kind, Title: v.Title, Ref: v.Ref}}, pane)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case choice.Pane != "":
		if err := find.Focus(choice.Pane, nil); err != nil {
			fmt.Fprintln(os.Stderr, "find: could not go to", choice.Pane+":", err)
			failed = true
		}
	default:
		if err := openURL(choice.URL); err != nil {
			fmt.Println(choice.URL)
			failed = true
		}
	}
	if pane != "" && !failed {
		wall.ClosePane(pane, nil)
	}
	if failed {
		os.Exit(1)
	}
}
