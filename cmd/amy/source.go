package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const jiraTemplate = `
# Added by 'amy source init jira'. Secrets stay out of yaml: set JIRA_EMAIL
# and JIRA_API_TOKEN in the environment or ~/.config/amythest/env.
sources:
  jira:
    url: https://yourcompany.atlassian.net
    jql: assignee = currentUser() AND resolution = EMPTY ORDER BY due ASC
    stub: true   # canned demo data; remove once url + credentials are real
`

// runSource handles `amy source init jira`: appends a commented sources
// template to cli.yaml (creating it when absent). If the file already has a
// sources: section it prints the template instead — user yaml is never
// rewritten structurally.
func runSource(args []string) {
	// Strip -config flags; configPathFromArgs reads them from os.Args.
	var words []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-config" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-config=") {
			continue
		}
		words = append(words, args[i])
	}
	if len(words) != 2 || words[0] != "init" || words[1] != "jira" {
		fmt.Fprintln(os.Stderr, "usage: amy source init jira [-config path]")
		os.Exit(2)
	}
	path := configPathFromArgs()
	raw, err := os.ReadFile(path)
	switch {
	case err == nil && strings.Contains(string(raw), "sources:"):
		fmt.Printf("%s already has a sources: section — merge this yourself:\n%s", path, jiraTemplate)
		return
	case err != nil && !os.IsNotExist(err):
		fmt.Fprintln(os.Stderr, "read config:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if _, err := f.WriteString(jiraTemplate); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("jira source template appended to %s\n", path)
	fmt.Println("It starts in stub mode (demo data). For a real Jira: set url, remove 'stub: true',")
	fmt.Println("and export JIRA_EMAIL / JIRA_API_TOKEN (env or ~/.config/amythest/env).")
}

// configPathFromArgs honors -config, else the default cli.yaml location.
func configPathFromArgs() string {
	for i, a := range os.Args {
		if a == "-config" && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "-config="); ok {
			return v
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "amythest", "cli.yaml")
}
