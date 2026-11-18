package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/base698/amythest/internal/wall"
	tea "github.com/charmbracelet/bubbletea"
)

// Resolve locally before handing the terminal to Vim. ExecProcess suspends
// Amy's input/rendering and restores the same diff when the editor exits.
func openWallFile(target wall.FileTarget) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cwd, err := os.Getwd()
		if err != nil {
			return wall.FileOpened{Err: err}
		}
		root, err := findDiffCheckout(ctx, target.Repo, cwd, nil)
		if err != nil && ctx.Err() == nil && os.Getenv("HC_TERMINAL_ID") != "" {
			// A dashboard may start in a workspace container directory.
			// Local Herdr terminals provide additional checkout locations.
			root, err = findDiffCheckout(ctx, target.Repo, cwd, localTerminalDirs(ctx))
		}
		if err != nil {
			return wall.FileOpened{Err: err}
		}
		cmd, err := diffVimCommand(root, target)
		if err != nil {
			return wall.FileOpened{Err: err}
		}
		return tea.ExecProcess(cmd, func(err error) tea.Msg { return wall.FileOpened{Err: err} })()
	}
}

func diffVimCommand(root string, target wall.FileTarget) (*exec.Cmd, error) {
	if !filepath.IsLocal(target.Path) {
		return nil, fmt.Errorf("invalid repository-relative filename")
	}
	file := filepath.Join(root, target.Path)
	info, err := os.Stat(file)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("file is not in the local checkout: %s", target.Path)
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", target.Path)
	}
	cmd := exec.Command("vim", fmt.Sprintf("+%d", max(1, target.Line)), "--", file)
	cmd.Dir = root
	return cmd, nil
}

func localTerminalDirs(ctx context.Context) []string {
	out, err := exec.CommandContext(ctx, "hc", "ls", "--json").Output()
	if err != nil {
		return nil
	}
	var list struct {
		Terminals []struct{ Address, Cwd string } `json:"terminals"`
	}
	if json.Unmarshal(out, &list) != nil {
		return nil
	}
	var dirs []string
	for _, terminal := range list.Terminals {
		if !strings.Contains(terminal.Address, "/") && filepath.IsAbs(terminal.Cwd) {
			dirs = append(dirs, terminal.Cwd)
		}
	}
	return dirs
}

// Prefer the active checkout; otherwise inspect nearby repositories and the
// local Herdr terminal directories. Match Git identity, never just a folder
// or filename. This also accepts .git files used by linked worktrees.
func findDiffCheckout(ctx context.Context, repo, cwd string, extra []string) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("no repository recorded for this diff")
	}
	git := func(dir string, args ...string) string {
		out, _ := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
		return strings.TrimSpace(string(out))
	}
	seen := map[string]bool{}
	probe := func(dir string) string {
		if dir == "" || seen[dir] || ctx.Err() != nil {
			return ""
		}
		seen[dir] = true
		root := git(dir, "rev-parse", "--show-toplevel")
		if root == "" {
			return ""
		}
		if filepath.IsAbs(repo) {
			canonical, err := filepath.EvalSymlinks(repo)
			if err == nil && canonical == root {
				return root
			}
		}
		for _, remote := range strings.Split(git(root, "config", "--get-regexp", `^remote\..*\.url$`), "\n") {
			_, address, ok := strings.Cut(remote, " ")
			if ok && diffRepoIdentity(address) == diffRepoIdentity(repo) {
				return root
			}
		}
		return ""
	}
	for _, dir := range append([]string{cwd, repo}, extra...) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if root := probe(dir); root != "" {
			return root, nil
		}
	}
	// Only immediate children, not a recursive scan of a home directory.
	parents := append([]string{cwd}, extra...)
	if root := git(cwd, "rev-parse", "--show-toplevel"); root != "" {
		parents = append(parents, filepath.Dir(root))
	}
	for _, parent := range parents {
		entries, _ := os.ReadDir(parent)
		for _, entry := range entries {
			dir := filepath.Join(parent, entry.Name())
			if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
				continue
			}
			if root := probe(dir); root != "" {
				return root, nil
			}
		}
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("local repository lookup timed out")
	}
	return "", fmt.Errorf("no local checkout found; open Amy from that repository or its parent folder")
}

func diffRepoIdentity(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		raw = strings.ToLower(u.Hostname()) + "/" + strings.TrimPrefix(u.Path, "/")
	} else if _, rest, ok := strings.Cut(raw, "@"); ok {
		raw = strings.Replace(rest, ":", "/", 1)
	}
	return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(raw), "/"), ".git")
}
