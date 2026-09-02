// Command amy is an interactive terminal client for a running amythest
// server: browse open vault tasks and kanban boards, toggle tasks and card
// checklist items, and move or complete cards.
//
// Configure the endpoint with -endpoint, AMYTHEST_ENDPOINT, or
// ~/.config/amythest/cli.yaml; credentials come from KANBAN_USERNAME /
// KANBAN_PASSWORD or ~/.config/amythest/env, like the other clients.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/source"
	"github.com/base698/amythest/internal/source/amythest"
	"github.com/base698/amythest/internal/source/azboards"
	"github.com/base698/amythest/internal/source/jira"
	"github.com/base698/amythest/internal/tui"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "source" {
		runSource(args[1:])
		return
	}
	check := false
	filtered := args[:0]
	for _, a := range args {
		if a == "-check" || a == "--check" {
			check = true
			continue
		}
		filtered = append(filtered, a)
	}

	cfg, err := apiclient.LoadConfig(filtered)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	client := apiclient.New(cfg)

	if check {
		runCheck(client)
		return
	}

	sources := []source.Source{amythest.New(client)}
	if jcfg, ok, jerr := jira.LoadConfig(cfg.File); jerr != nil {
		fmt.Fprintln(os.Stderr, "warning: sources config:", jerr)
	} else if ok {
		sources = append(sources, jira.New(jcfg, cfg.EnvFile))
	}
	if acfg, ok, aerr := azboards.LoadConfig(cfg.File); aerr != nil {
		fmt.Fprintln(os.Stderr, "warning: sources config:", aerr)
	} else if ok {
		sources = append(sources, azboards.New(acfg))
	}

	program := tea.NewProgram(tui.NewApp(client, source.NewRegistry(sources...)), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runCheck is a non-TTY smoke: login, list boards and tasks, exit 0. Used by
// scripts/compat.sh and for debugging endpoint config.
func runCheck(client *apiclient.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	boards, err := client.ListBoards(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kanban:", err)
		os.Exit(1)
	}
	groups, err := client.ListTasks(ctx, "not done;limit 1")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tasks:", err)
		os.Exit(1)
	}
	open := 0
	for _, g := range groups {
		open += len(g.Tasks)
	}
	fmt.Printf("ok: %s user=%s boards=%d open-tasks>=%d\n", client.Endpoint(), client.User(), len(boards), open)
}
