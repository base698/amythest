package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/messages"
)

const milestoneUsage = `amy milestone — mark a stretch of work on this workspace's project

  amy milestone                      open milestones here
  amy milestone start NAME...        start one ("production deploy")
      -from DATE [-to DATE]          ...or record past work: from DATE, and
                                     with -to already finished
  amy milestone end [NAME|ID]        end it (the only open one by default)
  amy milestone edit NAME|ID         change it: -from DATE, -to DATE (-to open
                                     reopens it), -name NEW
  amy milestone list [-all]          this project's milestones (-all: every project)
  amy milestone link NAME|ID REF...  link things to it whatever their time:
                                     msg:ID (a Messages ID), pr:owner/repo#N,
                                     or a pull request's URL
  amy milestone unlink NAME|ID REF...
  amy milestone links NAME|ID        what is linked
  amy milestone collect [NAME|ID]    everything in its window: sessions, runs,
                                     artifacts, agent notes, wall messages sent
                                     in it, and everything linked
                                     (-json for analysis, -o FILE to save)

While a milestone is open, everything the project produces belongs to it.
DATE is 2026-10-01 (local midnight; as -to, the end of that day),
"2026-10-01 14:30", or RFC 3339. Milestones on a project may overlap.
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
	from := fs.String("from", "", "start, edit: when it began (DATE)")
	to := fs.String("to", "", "start, edit: when it ended (DATE; edit: open reopens it)")
	rename := fs.String("name", "", "edit: new name")
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
	var refs []string
	if op == "link" || op == "unlink" {
		var rest []string
		for _, w := range words {
			if _, _, ok := linkRef(w); ok {
				refs = append(refs, w)
			} else {
				rest = append(rest, w)
			}
		}
		words = rest
	}
	name := strings.Join(words, " ")
	if (*from != "" || *to != "") && op != "start" && op != "edit" {
		return fmt.Errorf("-from and -to apply to start and edit")
	}
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
		if *to != "" && *from == "" {
			return fmt.Errorf("-to needs -from: amy milestone start NAME -from 2026-10-01 -to 2026-10-31")
		}
		started, err := milestoneDate(*from, false)
		if err != nil {
			return err
		}
		ended, err := milestoneDate(*to, true)
		if err != nil {
			return err
		}
		m, err := client.StartMilestone(ctx, p, name, started, ended)
		if err != nil {
			return err
		}
		if m.Ended != "" {
			fmt.Fprintf(out, "◇ %s on %s: %s – %s. Link to it: amy milestone link %q msg:ID pr:owner/repo#N\n", m.Name, m.Project, clockOf(m.Started), clockOf(m.Ended), m.Name)
			return nil
		}
		fmt.Fprintf(out, "◆ %s is open on %s (since %s). End it: amy milestone end\n", m.Name, m.Project, clockOf(m.Started))
	case "edit":
		if name == "" {
			return fmt.Errorf("edit needs a milestone: amy milestone edit NAME -from 2026-10-01")
		}
		var ch apiclient.MilestoneChange
		if *rename != "" {
			ch.Name = rename
		}
		if *from != "" {
			v, err := milestoneDate(*from, false)
			if err != nil {
				return err
			}
			ch.Started = &v
		}
		if *to != "" {
			v := ""
			if *to != "open" {
				if v, err = milestoneDate(*to, true); err != nil {
					return err
				}
			}
			ch.Ended = &v
		}
		if ch == (apiclient.MilestoneChange{}) {
			return fmt.Errorf("edit changes -from, -to or -name")
		}
		m, err := pickMilestone(ctx, client, here, name, false)
		if err != nil {
			return err
		}
		if m, err = client.UpdateMilestone(ctx, m.ID, ch); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s on %s: %s – %s\n", mark(m), m.Name, m.Project, clockOf(m.Started), clockOrNow(m.Ended))
	case "link", "unlink":
		if name == "" || len(refs) == 0 {
			return fmt.Errorf("%s needs a milestone and what to link: amy milestone %s NAME msg:ID pr:owner/repo#N", op, op)
		}
		m, err := pickMilestone(ctx, client, here, name, false)
		if err != nil {
			return err
		}
		for _, r := range refs {
			kind, ref, _ := linkRef(r)
			if op == "link" {
				l, err := client.LinkMilestone(ctx, m.ID, kind, ref)
				if err != nil {
					return fmt.Errorf("%s: %w", r, err)
				}
				fmt.Fprintf(out, "Linked %s %s to %s\n", l.Kind, l.Ref, m.Name)
			} else {
				if err := client.UnlinkMilestone(ctx, m.ID, kind, ref); err != nil {
					return fmt.Errorf("%s: %w", r, err)
				}
				fmt.Fprintf(out, "Unlinked %s %s from %s\n", kind, ref, m.Name)
			}
		}
	case "links":
		m, err := pickMilestone(ctx, client, here, name, false)
		if err != nil {
			return err
		}
		links, err := client.MilestoneLinks(ctx, m.ID)
		if err != nil {
			return err
		}
		if len(links) == 0 {
			fmt.Fprintf(out, "Nothing is linked to %s.\n", m.Name)
		}
		for _, l := range links {
			prefix := "msg:"
			if l.Kind == "pr" {
				prefix = "pr:"
			}
			fmt.Fprintf(out, "%s%s  linked %s\n", prefix, l.Ref, clockOf(l.Added))
		}
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
			fmt.Fprintf(out, "%s %-28s %-16s %s  %s  %s\n", mark(m), m.Name, m.Project, clockOf(m.Started), span(m), m.ID)
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

// Collection is everything a milestone's window holds, for review or
// analysis, with what is linked to it. Via says why a message or pull
// request is here: "window" (sent on the project's wall, or seen by the
// collector, inside the window) and/or "linked".
type Collection struct {
	Milestone apiclient.Milestone      `json:"milestone"`
	Sessions  []apiclient.WorkSession  `json:"sessions"`
	Runs      []apiclient.WorkRun      `json:"runs"`
	Artifacts []apiclient.WorkArtifact `json:"artifacts"`
	Notes     []apiclient.WorkDocument `json:"notes"`
	Messages  []CollectedMessage       `json:"messages"`
	PRs       []CollectedPR            `json:"pull_requests"`
}

// CollectedMessage is a message in a milestone. Tag is set when it is on
// the project's wall.
type CollectedMessage struct {
	Tag          *messages.Tag         `json:"tag,omitempty"`
	Message      messages.Message      `json:"message"`
	Conversation messages.Conversation `json:"conversation"`
	Via          []string              `json:"via"`
	Error        string                `json:"error,omitempty"`
}

// CollectedPR is a pull request in a milestone: title, state and merge time
// come from gh for linked ones, or the collector's artifact.
type CollectedPR struct {
	Ref      string                  `json:"ref"`
	URL      string                  `json:"url"`
	Title    string                  `json:"title,omitempty"`
	State    string                  `json:"state,omitempty"`
	MergedAt string                  `json:"merged_at,omitempty"`
	Artifact *apiclient.WorkArtifact `json:"artifact,omitempty"`
	Via      []string                `json:"via"`
	Error    string                  `json:"error,omitempty"`
}

// sentAt is when a wall message was sent, or tagged if that is unknown.
func sentAt(t messages.Tagged) time.Time {
	if !t.Message.At.IsZero() {
		return t.Message.At
	}
	return t.Tag.At
}

// prLookup reads a pull request's title, state and merge time with gh.
var prLookup = func(ctx context.Context, url string) (title, state, merged string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "gh", "pr", "view", url, "--json", "title,state,mergedAt").Output()
	if err != nil {
		return "", "", "", fmt.Errorf("gh pr view: %w", err)
	}
	var v struct {
		Title    string `json:"title"`
		State    string `json:"state"`
		MergedAt string `json:"mergedAt"`
	}
	err = json.Unmarshal(raw, &v)
	return v.Title, v.State, v.MergedAt, err
}

// messageSource is what collect reads from Messages.
type messageSource interface {
	Tagged(ctx context.Context, project string, limit int) ([]messages.Tagged, error)
	Message(ctx context.Context, id string) (messages.Message, error)
	Conversations(ctx context.Context) ([]messages.Listing, error)
}

var collectMessages = func() messageSource {
	if !messagingConfigured() {
		return nil
	}
	mc, err := loadMessageClient("", "", "owner", "")
	if err != nil {
		return nil
	}
	return mc
}

func collect(ctx context.Context, client *apiclient.Client, id string) (Collection, error) {
	m, snap, links, err := client.CollectMilestone(ctx, id)
	if err != nil {
		return Collection{}, err
	}
	return collection(ctx, m, snap, links, collectMessages()), nil
}

func collection(ctx context.Context, m apiclient.Milestone, snap *apiclient.WorkSnapshot, links []apiclient.MilestoneLink, mc messageSource) Collection {
	start, _ := time.Parse(time.RFC3339Nano, m.Started)
	end := time.Now()
	if m.Ended != "" {
		end, _ = time.Parse(time.RFC3339Nano, m.Ended)
	}
	c := Collection{Milestone: m, Artifacts: snap.Artifacts, Notes: snap.Documents,
		Sessions: []apiclient.WorkSession{}, Runs: []apiclient.WorkRun{}, Messages: []CollectedMessage{}, PRs: []CollectedPR{}}
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
	// Messages on the project's wall that were sent during the window (so
	// ones tagged afterwards still count), and linked ones.
	linked := map[string]bool{}
	for _, l := range links {
		if l.Kind == "message" {
			linked[l.Ref] = true
		}
	}
	seen := map[string]int{}
	if mc != nil {
		if tagged, err := mc.Tagged(ctx, m.Project, 500); err == nil {
			for _, t := range tagged {
				at := sentAt(t)
				if at.Before(start) || at.After(end) {
					if !linked[t.Message.ID] {
						continue
					}
				}
				tag := t.Tag
				cm := CollectedMessage{Tag: &tag, Message: t.Message, Conversation: t.Conversation, Via: []string{}}
				if !at.Before(start) && !at.After(end) {
					cm.Via = append(cm.Via, "window")
				}
				if linked[t.Message.ID] {
					cm.Via = append(cm.Via, "linked")
				}
				seen[t.Message.ID] = len(c.Messages)
				c.Messages = append(c.Messages, cm)
			}
		}
	}
	var names map[string]messages.Conversation
	for _, l := range links {
		if l.Kind != "message" {
			continue
		}
		if _, ok := seen[l.Ref]; ok {
			continue
		}
		cm := CollectedMessage{Message: messages.Message{ID: l.Ref}, Via: []string{"linked"}}
		if mc == nil {
			cm.Error = "Messages is not configured here"
		} else if msg, err := mc.Message(ctx, l.Ref); err != nil {
			cm.Error = err.Error()
		} else {
			cm.Message = msg
			if names == nil {
				names = map[string]messages.Conversation{}
				if list, err := mc.Conversations(ctx); err == nil {
					for _, x := range list {
						names[x.Conversation.ID] = x.Conversation
					}
				}
			}
			cm.Conversation = names[msg.Conversation]
		}
		seen[l.Ref] = len(c.Messages)
		c.Messages = append(c.Messages, cm)
	}
	sort.SliceStable(c.Messages, func(i, j int) bool { return c.Messages[i].Message.At.Before(c.Messages[j].Message.At) })
	// Pull requests: linked ones, and the ones the collector saw in the window.
	byURL := map[string]int{}
	for _, l := range links {
		if l.Kind != "pr" {
			continue
		}
		p := CollectedPR{Ref: l.Ref, URL: prURL(l.Ref), Artifact: l.Artifact, Via: []string{"linked"}}
		if title, state, merged, err := prLookup(ctx, p.URL); err == nil {
			p.Title, p.State, p.MergedAt = title, state, merged
		} else {
			p.Error = err.Error()
			if l.Artifact != nil {
				p.Title = l.Artifact.Title
			}
		}
		byURL[p.URL] = len(c.PRs)
		c.PRs = append(c.PRs, p)
	}
	for _, a := range c.Artifacts {
		if a.Kind != "pr" {
			continue
		}
		u := strings.TrimSuffix(a.Ref, "/")
		if i, ok := byURL[u]; ok {
			if !slices.Contains(c.PRs[i].Via, "window") {
				c.PRs[i].Via = append([]string{"window"}, c.PRs[i].Via...)
			}
			if c.PRs[i].Artifact == nil {
				art := a
				c.PRs[i].Artifact = &art
			}
			continue
		}
		art := a
		byURL[u] = len(c.PRs)
		c.PRs = append(c.PRs, CollectedPR{Ref: prRefOf(u), URL: u, Title: a.Title, Artifact: &art, Via: []string{"window"}})
	}
	return c
}

var (
	prShortRef = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#([1-9][0-9]{0,9})$`)
	prGitHub   = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/pull/([1-9][0-9]{0,9})$`)
	prAnyURL   = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(?::[0-9]+)?/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[1-9][0-9]{0,9}/?(?:[?#].*)?$`)
)

// prURL is a stored pull request ref's web address.
func prURL(ref string) string {
	if m := prShortRef.FindStringSubmatch(ref); m != nil {
		return "https://github.com/" + m[1] + "/" + m[2] + "/pull/" + m[3]
	}
	return ref
}

// prRefOf is a pull request URL's short ref where it has one.
func prRefOf(u string) string {
	if m := prGitHub.FindStringSubmatch(u); m != nil {
		return m[1] + "/" + m[2] + "#" + m[3]
	}
	return u
}

// linkRef reads a link argument: msg:ID, pr:owner/repo#N, pr:URL or a pull
// request URL.
func linkRef(arg string) (kind, ref string, ok bool) {
	switch {
	case strings.HasPrefix(arg, "msg:") && len(arg) > 4:
		return "message", arg[4:], true
	case strings.HasPrefix(arg, "pr:") && len(arg) > 3:
		return "pr", arg[3:], true
	case prAnyURL.MatchString(arg):
		return "pr", arg, true
	}
	return "", "", false
}

// milestoneDate reads a DATE flag as RFC 3339. A bare day is local
// midnight; as an end it means the end of that day.
func milestoneDate(v string, end bool) (string, error) {
	if v == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t.UTC().Format(time.RFC3339Nano), nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", v, time.Local); err == nil {
		return t.UTC().Format(time.RFC3339Nano), nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", v, time.Local); err == nil {
		return t.UTC().Format(time.RFC3339Nano), nil
	}
	t, err := time.ParseInLocation("2006-01-02", v, time.Local)
	if err != nil {
		return "", fmt.Errorf("dates look like 2026-10-01, \"2026-10-01 14:30\" or RFC 3339, not %q", v)
	}
	if end {
		t = t.AddDate(0, 0, 1)
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}

func mark(m apiclient.Milestone) string {
	if m.Ended == "" {
		return "◆"
	}
	return "◇"
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
	fmt.Fprintf(w, "\n## Pull requests (%d)\n", len(c.PRs))
	for _, p := range c.PRs {
		line := "- " + p.Ref
		if p.Title != "" {
			line += " " + oneLine(p.Title, 100)
		}
		if p.State != "" {
			line += " [" + strings.ToLower(p.State) + "]"
		}
		if p.MergedAt != "" {
			line += " merged " + clockOf(p.MergedAt)
		}
		fmt.Fprintf(w, "%s%s\n", line, viaNote(p.Via))
	}
	fmt.Fprintf(w, "\n## Messages (%d)\n", len(c.Messages))
	for _, t := range c.Messages {
		if t.Error != "" {
			fmt.Fprintf(w, "- msg:%s (%s)%s\n", t.Message.ID, t.Error, viaNote(t.Via))
			continue
		}
		fmt.Fprintf(w, "- %s %s · %s: %s%s\n", t.Message.At.Local().Format("Jan 2 15:04"), messages.Plain(t.Conversation.Name),
			messages.Plain(t.Message.Sender), oneLine(messages.Plain(t.Message.Body), 120), viaNote(t.Via))
	}
	_, err := fmt.Fprintln(w)
	return err
}

// viaNote marks linked items in the report; window items need no note.
func viaNote(via []string) string {
	if slices.Contains(via, "linked") {
		return " (linked)"
	}
	return ""
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
