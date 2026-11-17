package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/find"
	"github.com/base698/amythest/internal/wall"
)

// `amy find` is one search across the vault (notes, cards, tasks), what
// agents said and produced, and the text on screen in open terminals. Enter
// on a result goes to the pane where it happened when that pane is still
// open on this machine; otherwise it opens the result in the browser.
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
	// Close the search's pane before going anywhere, so the focus that
	// follows is not handed back to a neighbour of the closing pane.
	if pane != "" {
		wall.ClosePane(pane, nil)
	}
	if choice == nil {
		return
	}
	if choice.Pane != "" {
		if err := find.Focus(choice.Pane, nil); err != nil {
			fmt.Fprintln(os.Stderr, "find: could not go to", choice.Pane+":", err)
			os.Exit(1)
		}
		return
	}
	if err := openURL(choice.URL); err != nil {
		fmt.Println(choice.URL)
	}
}
