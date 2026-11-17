package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/wall"
)

// `amy wall` shows what agents produced for one project as a newest-first
// feed: commits, and images drawn inline. It is meant to sit in a pane beside
// the work. Inside herdr it shows the current workspace's project; elsewhere,
// or with -project, any project.
//
//	amy wall                    the current herdr workspace
//	amy wall -project robot     a named project
//	amy wall -list              print the feed as text and exit
//	amy wall -pane              opened by a key in the Herdr App: a second
//	                            press closes it, and quitting closes the pane
func runWall(args []string) {
	fs := flag.NewFlagSet("wall", flag.ExitOnError)
	project := fs.String("project", "", "project to show (default: the current herdr workspace)")
	images := fs.String("images", "auto", "how to draw images: auto, kitty, blocks or off")
	list := fs.Bool("list", false, "print the feed as text and exit")
	ownPane := fs.Bool("pane", false, "the wall owns its Herdr App pane: toggle with a wall already in this tab, close the pane on quit")
	refresh := fs.Duration("refresh", 15*time.Second, "how often to look for new items")
	endpoint := fs.String("endpoint", "", "amythest server base URL")
	config := fs.String("config", "", "path to cli.yaml")
	fs.Parse(args)
	pane := ""
	if *ownPane && !*list {
		var closed bool
		var err error
		pane, closed, err = wall.ToggleAppPane(os.Getenv("HC_TERMINAL_ID"), "wall", nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "wall:", err)
			os.Exit(1)
		}
		if closed {
			return
		}
	}

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
	locate := func() wall.Place { return wall.Locate(nil, nil) }

	if *list {
		if err := wall.List(context.Background(), os.Stdout, client, *project, locate); err != nil {
			fmt.Fprintln(os.Stderr, "wall:", err)
			os.Exit(1)
		}
		return
	}

	if err := showWall(client, *images, wall.Options{Project: *project, Refresh: *refresh, Locate: locate}, pane); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// showWall runs the wall on this terminal until the person quits it, then
// closes pane, the Herdr App pane it owns, if any.
func showWall(client *apiclient.Client, images string, opts wall.Options, pane string) error {
	graphics := images
	if graphics == "" || graphics == "auto" {
		graphics = wall.DetectGraphics(os.Getenv, terminalAnswersGraphicsQuery)
	}
	if graphics != "kitty" && graphics != "blocks" && graphics != "off" {
		return fmt.Errorf("wall: -images must be auto, kitty, blocks or off")
	}
	if opts.Share == nil {
		opts.Share = func(it wall.Item) tea.Cmd { return shareWallItem(client, it) }
	}
	out := &serialOut{File: os.Stdout}
	opts.Graphics, opts.Out, opts.Open = graphics, out, openURL
	// The terminal reports its size in cells and in pixels; together they give
	// the shape of a cell, which decides how many rows an image needs.
	if ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ); err == nil && ws.Col > 0 && ws.Row > 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
		opts.CellW = int(ws.Xpixel) / int(ws.Col)
		opts.CellAspect = (float64(ws.Xpixel) / float64(ws.Col)) / (float64(ws.Ypixel) / float64(ws.Row))
	}
	program := tea.NewProgram(wall.New(client, opts), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithOutput(out))
	if _, err := program.Run(); err != nil {
		return err
	}
	if pane != "" {
		wall.ClosePane(pane, nil)
	}
	return nil
}

// serialOut is the terminal as one writer. The screen and image uploads both
// go through it, so an upload can never land in the middle of a redraw.
type serialOut struct {
	*os.File
	mu sync.Mutex
}

func (s *serialOut) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.File.Write(p)
}

// terminalAnswersGraphicsQuery asks the terminal whether it draws Kitty
// graphics: a 1×1 test image that is checked but not shown, followed by a
// request every terminal answers. A terminal with graphics replies to the
// first before the second; one without ignores it.
func terminalAnswersGraphicsQuery() bool {
	fd := os.Stdin.Fd()
	if !term.IsTerminal(fd) || !term.IsTerminal(os.Stdout.Fd()) {
		return false
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return false
	}
	defer term.Restore(fd, old)
	if _, err := os.Stdout.WriteString("\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c"); err != nil {
		return false
	}
	var reply []byte
	buf := make([]byte, 256)
	for deadline := time.Now().Add(700 * time.Millisecond); time.Now().Before(deadline); {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if n, err := unix.Poll(fds, 50); err != nil || n == 0 {
			continue
		}
		n, err := unix.Read(int(fd), buf)
		if err != nil || n == 0 {
			break
		}
		reply = append(reply, buf[:n]...)
		// The second answer has arrived: nothing more is coming.
		if i := strings.Index(string(reply), "\x1b[?"); i >= 0 && strings.HasSuffix(string(reply), "c") {
			break
		}
	}
	return strings.Contains(string(reply), "\x1b_Gi=31;OK")
}

func openURL(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("not a web address")
	}
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	cmd := exec.Command(name, url)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Give the opener a moment to hand the address over: the caller may be
	// about to close the pane this program runs in, which would end it too.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		return nil
	}
}
