package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/find"
	"github.com/base698/amythest/internal/tui"
	"github.com/base698/amythest/internal/wall"
	tea "github.com/charmbracelet/bubbletea"
)

func runWorkspace(args []string) {
	fs := flag.NewFlagSet("workspace", flag.ExitOnError)
	name := fs.String("workspace", "", "workspace name (defaults to the current Herdr space)")
	images := fs.String("images", "auto", "how to draw images: auto, kitty, blocks or off")
	endpoint := fs.String("endpoint", "", "amythest server base URL")
	config := fs.String("config", "", "path to private cli.yaml")
	fs.Parse(args)
	pass := []string{}
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
	if *name == "" {
		*name = wall.Locate(nil, nil).Project
	}
	if *name == "" {
		fmt.Fprintln(os.Stderr, "Choose a workspace with: amy workspace -workspace NAME")
		os.Exit(2)
	}
	client := apiclient.New(cfg)
	scope := cfg.Workspace(*name)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	overview, _ := client.WorkOverview(ctx)
	cancel()
	if overview != nil {
		for _, project := range overview.Projects {
			if strings.EqualFold(project, scope.Project) {
				scope.Project = project
				break
			}
		}
	}
	if err := tui.LoadThemes(cfg.File); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	options := tui.WorkspaceOptions{Scope: scope, AllSources: configuredSources(client, cfg), Wall: wall.Options{Open: openURL, OpenCanvas: openCanvasPane, Share: func(it wall.Item) tea.Cmd { return shareWallItem(client, it) }}}
	withWallMessages(&options.Wall)
	options.Wall.OpenFile = openWallFile
	out := &serialOut{File: os.Stdout}
	if err := configureWallGraphics(*images, &options.Wall, out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	options.Open = func(c *find.Choice) tea.Cmd {
		return func() tea.Msg {
			var err error
			if c.Pane != "" {
				err = find.Focus(c.Pane, nil)
			} else if c.URL != "" {
				err = openURL(c.URL)
			}
			return tui.WorkspaceOpened{Err: err}
		}
	}
	dashboard := tui.NewWorkspace(client, options)
	_, err = tea.NewProgram(dashboard, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithOutput(out)).Run()
	dashboard.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
