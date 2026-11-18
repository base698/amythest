package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/wall"
)

func diffGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	} else {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func diffRepo(t *testing.T, parent, name, remote string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	diffGit(t, dir, "init", "-q")
	if remote != "" {
		diffGit(t, dir, "remote", "add", "origin", remote)
	}
	// Git's show-toplevel resolves platform aliases such as /var -> /private/var.
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDiffFindsMatchingLocalCheckout(t *testing.T) {
	base := t.TempDir()
	repo := diffRepo(t, base, "checkout with spaces", "git@github.com:example/demo.git")
	other := diffRepo(t, base, "another-repo", "https://github.com/example/other.git")
	home := t.TempDir()
	for _, tc := range []struct {
		name, identity, cwd, want string
		extra                     []string
	}{
		{"current", "github.com/example/demo", repo, repo, nil},
		{"parent", "https://github.com/example/demo.git", base, repo, nil},
		{"sibling", "ssh://git@github.com/example/demo", other, repo, nil},
		{"absolute", repo, home, repo, nil},
		{"Herdr directory", "github.com/example/demo", home, repo, []string{repo}},
		{"Herdr workspace folder", "github.com/example/demo", home, repo, []string{base}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findDiffCheckout(context.Background(), wall.FileTarget{Repo: tc.identity}, tc.cwd, tc.extra)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := findDiffCheckout(context.Background(), wall.FileTarget{Repo: "github.com/elsewhere/demo"}, base, nil); err == nil {
		t.Fatal("matched a repository with only the same basename")
	}
}

func TestDiffPrefersActiveWorktree(t *testing.T) {
	base := t.TempDir()
	repo := diffRepo(t, base, "main", "https://github.com/example/demo.git")
	diffGit(t, repo, "-c", "user.name=Example", "-c", "user.email=example@example.test", "commit", "--allow-empty", "-m", "initial")
	worktree := filepath.Join(base, "active")
	diffGit(t, repo, "worktree", "add", "--detach", worktree, "HEAD")
	worktree, _ = filepath.EvalSymlinks(worktree)
	got, err := findDiffCheckout(context.Background(), wall.FileTarget{Repo: "github.com/example/demo"}, worktree, []string{repo})
	if err != nil || got != worktree {
		t.Fatalf("active worktree lost: %q, %v", got, err)
	}
}

func TestDiffLookupSkipsCheckoutThatLacksTheFile(t *testing.T) {
	base := t.TempDir()
	stale := diffRepo(t, base, "stale", "https://github.com/example/demo.git")
	ready := diffRepo(t, base, "ready", "git@github.com:example/demo.git")
	file := "new-file.go"
	if err := os.WriteFile(filepath.Join(ready, file), []byte("package example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := wall.FileTarget{Repo: "github.com/example/demo", Path: file}
	got, err := findDiffCheckout(context.Background(), target, stale, nil)
	if err != nil || got != ready {
		t.Fatalf("stopped at a checkout without the file: %q, %v", got, err)
	}
	// When both contain it, keep editing the active checkout.
	if err := os.WriteFile(filepath.Join(stale, file), []byte("package example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = findDiffCheckout(context.Background(), target, stale, nil)
	if err != nil || got != stale {
		t.Fatalf("active checkout lost: %q, %v", got, err)
	}
	target.Path = "absent.go"
	if _, err := findDiffCheckout(context.Background(), target, stale, nil); err == nil || !strings.Contains(err.Error(), "sync or switch branch") {
		t.Fatalf("missing file needs an actionable error: %v", err)
	}
}

func TestDiffFindsFileInRegisteredWorktreeOutsideWorkspace(t *testing.T) {
	repo := diffRepo(t, t.TempDir(), "main", "https://github.com/example/demo.git")
	diffGit(t, repo, "-c", "user.name=Example", "-c", "user.email=example@example.test", "commit", "--allow-empty", "-m", "initial")
	worktree := filepath.Join(t.TempDir(), "another folder")
	diffGit(t, repo, "worktree", "add", "--detach", worktree, "HEAD")
	worktree, _ = filepath.EvalSymlinks(worktree)
	if err := os.WriteFile(filepath.Join(worktree, "new-file.go"), []byte("package example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{"github.com/example/demo", repo} {
		got, err := findDiffCheckout(context.Background(), wall.FileTarget{Repo: identity, Path: "new-file.go"}, repo, nil)
		if err != nil || got != worktree {
			t.Fatalf("registered worktree was missed: %q, %v", got, err)
		}
	}
}

func TestDiffVimOpensWorkingFileAtLineWithLiteralArguments(t *testing.T) {
	root := t.TempDir()
	name := "+odd file # with spaces.go"
	file := filepath.Join(root, name)
	if err := os.WriteFile(file, []byte("working copy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd, err := diffVimCommand(root, wall.FileTarget{Path: name, Line: 23})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"vim", "+23", "--", file}; !reflect.DeepEqual(cmd.Args, want) || cmd.Dir != root {
		t.Fatalf("Vim command: %v dir %q", cmd.Args, cmd.Dir)
	}
	if cmd.Stdout != os.Stdout {
		t.Fatal("Vim must inherit the terminal file, not Bubble Tea's wrapped writer (which becomes a pipe)")
	}
	for _, name := range []string{"../outside.go", file, "missing.go", "."} {
		if _, err := diffVimCommand(root, wall.FileTarget{Path: name}); err == nil {
			t.Fatalf("opened missing, absolute or non-file path %q", name)
		}
	}
}

func TestDiffTerminalDirsIgnoreRemoteMachines(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "hc"), []byte("#!/bin/sh\nprintf '%s' '{\"terminals\":[{\"address\":\"t1\",\"cwd\":\"/local/repo\"},{\"address\":\"host/t2\",\"cwd\":\"/remote/repo\"}]}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	got := localTerminalDirs(context.Background())
	if !reflect.DeepEqual(got, []string{"/local/repo"}) {
		t.Fatalf("directories: %v", got)
	}
}
