package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/messages"
)

const milestoneUsage = `amy milestone — mark a stretch of work on this workspace's project

  amy milestone                      open milestones here
  amy milestone start NAME...        start one ("production deploy")
  amy milestone end [NAME|ID]        end it (the only open one by default)
  amy milestone list [-all]          this project's milestones (-all: every project)
  amy milestone collect [NAME|ID]    everything in its window: sessions, runs,
                                     artifacts, agent notes, wall messages
                                     (-json for analysis, -o FILE to save)

While a milestone is open, everything the project produces belongs to it.
-project NAME works outside a herdr workspace or for another project.`

func runMilestone(args []string) {
	if err := milestoneCommand(args, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "milestone:", err)
		os.Exit(1)
	}
}

func milestoneCommand(args []string, out io.Writer) error {
	op := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		op, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("milestone", flag.ContinueOnError)
	project := fs.String("project", "", "project (default: this herdr workspace's)")
	all := fs.Bool("all", false, "list: every project")
	asJSON := fs.Bool("json", false, "collect: JSON for analysis")
	file := fs.String("o", "", "collect: write to this file")
	endpoint := fs.String("endpoint", "", "amythest server base URL")
	config := fs.String("config", "", "path to cli.yaml")
	fs.Usage = func() { fmt.Fprintln(fs.Output(), milestoneUsage) }
	// Flags may follow words: "start production deploy -project x".
	var words []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		words = append(words, fs.Arg(0))
		args = fs.Args()[1:]
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
		return err
	}
	client := apiclient.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	here := func() (string, error) {
		if *project != "" {
			return *project, nil
		}
		if p := currentProject(); p != "" {
			return p, nil
		}
		return "", fmt.Errorf("not in a herdr workspace; name the project with -project")
	}
	name := strings.Join(words, " ")
	switch op {
	case "", "status":
		p, err := here()
		if err != nil {
			return err
		}
		open, err := client.Milestones(ctx, p, true)
		if err != nil {
			return err
		}
		if len(open) == 0 {
			fmt.Fprintf(out, "No open milestone on %s. Start one: amy milestone start NAME\n", p)
			return nil
		}
		for _, m := range open {
			fmt.Fprintf(out, "◆ %s · %s · open %s\n", m.Name, m.Project, since(m.Started))
		}
	case "start":
		p, err := here()
		if err != nil {
			return err
		}
		if name == "" {
			return fmt.Errorf("start needs a name: amy milestone start production deploy")
		}
		m, err := client.StartMilestone(ctx, p, name)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "◆ %s is open on %s (since %s). End it: amy milestone end\n", m.Name, m.Project, clockOf(m.Started))
	case "end":
		m, err := pickMilestone(ctx, client, here, name, true)
		if err != nil {
			return err
		}
		if m, err = client.EndMilestone(ctx, m.ID); err != nil {
			return err
		}
		fmt.Fprintf(out, "◇ %s on %s ended after %s. Collect it: amy milestone collect %q\n", m.Name, m.Project, span(m), m.Name)
	case "list":
		p := ""
		if !*all {
			if p, err = here(); err != nil {
				return err
			}
		}
		list, err := client.Milestones(ctx, p, false)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Fprintln(out, "No milestones yet.")
		}
		for _, m := range list {
			mark := "◇"
			if m.Ended == "" {
				mark = "◆"
			}
			fmt.Fprintf(out, "%s %-28s %-16s %s  %s  %s\n", mark, m.Name, m.Project, clockOf(m.Started), span(m), m.ID)
		}
	case "collect":
		m, err := pickMilestone(ctx, client, here, name, false)
		if err != nil {
			return err
		}
		c, err := collect(ctx, client, m.ID)
		if err != nil {
			return err
		}
		w := out
		if *file != "" {
			f, err := os.OpenFile(*file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			defer f.Close()
			w = f
		}
		if *asJSON {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			err = enc.Encode(c)
		} else {
			err = c.report(w)
		}
		if err == nil && *file != "" {
			fmt.Fprintf(out, "Wrote %s\n", *file)
		}
		return err
	default:
		return fmt.Errorf("unknown milestone command %q\n\n%s", op, milestoneUsage)
	}
	return nil
}

// pickMilestone finds a milestone by ID, or by name on this project (the
// newest with that name), or with no name the only open one here.
func pickMilestone(ctx context.Context, client *apiclient.Client, here func() (string, error), name string, open bool) (apiclient.Milestone, error) {
	if name != "" && !strings.Contains(name, " ") && len(name) == 36 && strings.Count(name, "-") == 4 {
		list, err := client.Milestones(ctx, "", false)
		if err != nil {
			return apiclient.Milestone{}, err
		}
		for _, m := range list {
			if m.ID == name {
				return m, nil
			}
		}
	}
	p, err := here()
	if err != nil {
		return apiclient.Milestone{}, err
	}
	list, err := client.Milestones(ctx, p, open)
	if err != nil {
		return apiclient.Milestone{}, err
	}
	var match []apiclient.Milestone
	for _, m := range list {
		if name == "" || strings.EqualFold(m.Name, name) {
			match = append(match, m)
		}
	}
	switch {
	case len(match) == 0 && name == "" && open:
		return apiclient.Milestone{}, fmt.Errorf("no open milestone on %s", p)
	case len(match) == 0:
		return apiclient.Milestone{}, fmt.Errorf("no milestone %q on %s", name, p)
	case name == "" && len(match) > 1:
		names := make([]string, len(match))
		for i, m := range match {
			names[i] = m.Name
		}
		return apiclient.Milestone{}, fmt.Errorf("several are open on %s; name one: %s", p, strings.Join(names, ", "))
	}
	return match[0], nil // newest first
}

// Collection is everything a milestone's window holds, for review or analysis.
type Collection struct {
	Milestone apiclient.Milestone      `json:"milestone"`
	Sessions  []apiclient.WorkSession  `json:"sessions"`
	Runs      []apiclient.WorkRun      `json:"runs"`
	Artifacts []apiclient.WorkArtifact `json:"artifacts"`
	Notes     []apiclient.WorkDocument `json:"notes"`
	Messages  []messages.Tagged        `json:"messages"`
}

func collect(ctx context.Context, client *apiclient.Client, id string) (Collection, error) {
	m, snap, err := client.CollectMilestone(ctx, id)
	if err != nil {
		return Collection{}, err
	}
	start, _ := time.Parse(time.RFC3339Nano, m.Started)
	end := time.Now()
	if m.Ended != "" {
		end, _ = time.Parse(time.RFC3339Nano, m.Ended)
	}
	c := Collection{Milestone: m, Artifacts: snap.Artifacts, Notes: snap.Documents,
		Sessions: []apiclient.WorkSession{}, Runs: []apiclient.WorkRun{}, Messages: []messages.Tagged{}}
	if c.Artifacts == nil {
		c.Artifacts = []apiclient.WorkArtifact{}
	}
	if c.Notes == nil {
		c.Notes = []apiclient.WorkDocument{}
	}
	// Runs that overlap the window, and the sessions they belong to.
	inSession := map[string]bool{}
	for _, r := range snap.Runs {
		started, _ := time.Parse(time.RFC3339Nano, r.Started)
		finished, ferr := time.Parse(time.RFC3339Nano, r.Finished)
		if started.Before(end) && (r.Finished == "" || ferr != nil || !finished.Before(start)) {
			c.Runs = append(c.Runs, r)
			inSession[r.Session] = true
		}
	}
	for _, s := range snap.Sessions {
		if inSession[s.ID] {
			c.Sessions = append(c.Sessions, s)
		}
	}
	// Messages put on the project's wall during the window.
	if messagingConfigured() {
		if mc, err := loadMessageClient("", "", "owner", ""); err == nil {
			if tagged, err := mc.Tagged(ctx, m.Project, 500); err == nil {
				for _, t := range tagged {
					if !t.Tag.At.Before(start) && !t.Tag.At.After(end) {
						c.Messages = append(c.Messages, t)
					}
				}
			}
		}
	}
	sort.Slice(c.Messages, func(i, j int) bool { return c.Messages[i].Tag.At.Before(c.Messages[j].Tag.At) })
	return c, nil
}

func (c Collection) report(w io.Writer) error {
	m := c.Milestone
	state := "ended " + clockOf(m.Ended)
	if m.Ended == "" {
		state = "still open"
	}
	fmt.Fprintf(w, "# %s · %s\n\n%s – %s (%s, %s)\n", m.Name, m.Project, clockOf(m.Started), clockOrNow(m.Ended), span(m), state)
	fmt.Fprintf(w, "\n## Sessions (%d)\n", len(c.Sessions))
	for _, s := range c.Sessions {
		fmt.Fprintf(w, "- %s · %s\n", s.Agent, s.Title)
	}
	fmt.Fprintf(w, "\n## Runs (%d)\n", len(c.Runs))
	for _, r := range c.Runs {
		fmt.Fprintf(w, "- %s – %s · session %s\n", clockOf(r.Started), clockOrNow(r.Finished), shortID(r.Session))
	}
	fmt.Fprintf(w, "\n## Artifacts (%d)\n", len(c.Artifacts))
	for _, a := range c.Artifacts {
		title := a.Title
		if title == "" {
			title = a.Ref
		}
		fmt.Fprintf(w, "- %s %s · %s\n", a.Kind, oneLine(title, 100), clockOf(a.Created))
	}
	fmt.Fprintf(w, "\n## Agent notes (%d)\n", len(c.Notes))
	for _, d := range c.Notes {
		fmt.Fprintf(w, "- %s: %s · %s\n", d.Kind, oneLine(d.Title, 100), clockOf(d.Created))
	}
	fmt.Fprintf(w, "\n## Messages on the wall (%d)\n", len(c.Messages))
	for _, t := range c.Messages {
		fmt.Fprintf(w, "- %s %s · %s: %s\n", t.Message.At.Local().Format("Jan 2 15:04"), messages.Plain(t.Conversation.Name),
			messages.Plain(t.Message.Sender), oneLine(messages.Plain(t.Message.Body), 120))
	}
	_, err := fmt.Fprintln(w)
	return err
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func clockOf(stamp string) string {
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return stamp
	}
	return t.Local().Format("Jan 2 15:04")
}

func clockOrNow(stamp string) string {
	if stamp == "" {
		return "now"
	}
	return clockOf(stamp)
}

func since(stamp string) string {
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return ""
	}
	return "for " + time.Since(t).Round(time.Minute).String()
}

func span(m apiclient.Milestone) string {
	start, err := time.Parse(time.RFC3339Nano, m.Started)
	if err != nil {
		return ""
	}
	end := time.Now()
	if m.Ended != "" {
		if e, err := time.Parse(time.RFC3339Nano, m.Ended); err == nil {
			end = e
		}
	}
	return end.Sub(start).Round(time.Minute).String()
}
