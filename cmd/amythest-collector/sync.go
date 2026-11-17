package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/base698/amythest/internal/collector/collect"
	"github.com/base698/amythest/internal/collector/herdr"
	"github.com/base698/amythest/internal/collector/hub"
	"github.com/base698/amythest/internal/collector/store"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// sync is an opt-in shadow sender. The original watch command and JSONL remain
// usable throughout reconciliation; this process never removes their records.
func syncWork(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	credential := fs.String("credential", filepath.Join(store.Dir(), "credential.json"), "collector credential from amythest-collector register")
	capture := fs.Bool("capture-only", false, "capture lifecycle events without a server credential")
	skipHermes := fs.Bool("skip-hermes-panes", false, "Hermes panes are remote views collected on their host")
	once := fs.Bool("once", false, "deliver queued events once without monitoring panes")
	if e := fs.Parse(args); e != nil {
		return e
	}
	var client *hub.Client
	var e error
	if !*capture {
		client, e = hub.Load(*credential)
		if e != nil {
			return e
		}
	}
	q, e := hub.Open(store.Dir())
	if e != nil {
		return e
	}
	defer q.Close()
	if *once {
		if e = q.PollCodex(filepath.Join(store.Dir(), "codex.json")); e != nil {
			return e
		}
		if e = q.DrainLifecycle(filepath.Join(store.Dir(), "hooks")); e != nil {
			return e
		}
		if *capture {
			return nil
		}
		return client.Sync(q)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	failed := make(chan error, 3)
	captureDone := make(chan struct{})
	hcDone := make(chan struct{})
	jobsDone := make(chan struct{})
	// Without a stable provider session reference, a process restart cannot
	// prove that a reused pane still holds the same agent incarnation.
	if e = q.Update(func(s *hub.State) error {
		for key, p := range s.Sessions {
			if p.Pane.Provider == "" {
				p.Pane.Provider = "unknown-before-restart"
				s.Sessions[key] = p
			}
		}
		return nil
	}); e != nil {
		return e
	}
	topo := newTopology(q, "tui", "hc")
	go func() {
		defer close(captureDone)
		if e := captureTUI(ctx, topo, *skipHermes); e != nil {
			failed <- e
		}
	}()
	go func() {
		defer close(hcDone)
		if e := captureHC(ctx, topo, *skipHermes); e != nil {
			failed <- e
		}
	}()
	go func() {
		defer close(jobsDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			if e := q.PollCodex(filepath.Join(store.Dir(), "codex.json")); e != nil {
				failed <- e
				return
			}
			if e := q.DrainLifecycle(filepath.Join(store.Dir(), "hooks")); e != nil {
				failed <- e
				return
			}
			state := q.Snapshot()
			if len(state.Jobs) == 0 {
				continue
			}
			job := state.Jobs[0]
			p := job.Session.Pane
			arts := collect.Collect(collect.Run{Owner: collect.Owner{Machine: "local", Project: p.Project, Pane: p.ID, Session: p.Provider, Agent: p.Agent, Attribute: "suggested"}, Dir: p.Dir, Since: job.Session.Started})
			output := []hub.Artifact{}
			seen := map[string]bool{}
			for _, a := range arts {
				if seen[a.Key] {
					continue
				}
				seen[a.Key] = true
				key := a.Key
				// Local path observations must never collide with another collector. IDs
				// remain stable because the completed batch is persisted before delivery.
				switch a.Kind {
				case "commit":
					repo := collect.CanonicalRepo(a.Repo)
					if repo == "" {
						repo = collectorNamespace(q) + ":" + a.Repo
					}
					key = "commit:" + repo + ":" + a.Ref
				case "pr":
				default:
					key = "file:" + collectorNamespace(q) + ":" + hub.ID()
				}
				artifact := hub.Artifact{Key: key, Kind: a.Kind, Ref: a.Ref, Title: a.Title, Repo: a.Repo, Attribution: "suggested"}
				if filepath.IsAbs(a.Ref) {
					artifact.RemotePath = a.Ref
				}
				output = append(output, artifact)
				if e := store.Append("artifacts", a); e != nil {
					failed <- e
					return
				}
			}
			if e := q.Complete(job, output); e != nil {
				failed <- e
				return
			}
		}
	}()
	defer func() { cancel(); <-captureDone; <-hcDone; <-jobsDone }()
	backoff := 2 * time.Second
	for {
		if client == nil {
			backoff = 2 * time.Second
		} else if e := client.Sync(q); e != nil {
			fmt.Fprintln(os.Stderr, e)
			backoff = min(backoff*2, time.Minute)
		} else {
			backoff = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return nil
		case e := <-failed:
			return e
		case <-time.After(backoff):
		}
	}
}
func captureTUI(ctx context.Context, topo *topology, skipHermes bool) error {
	m := herdr.Machine{Name: "local"}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		t, e := m.Snapshot()
		if e != nil {
			if e = topo.unreachable("tui", notRunning(e)); e != nil {
				return e
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
				continue
			}
		}
		ids := []string{}
		for _, p := range t.Panes {
			ids = append(ids, p.PaneID)
		}
		events, stop, e := m.Subscribe(ids)
		if e != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
				continue
			}
		}
		// Herdr starts subscriptions at live events, not retained history.
		// Subscribe first, then inventory; queued transitions bridge startup.
		t, e = m.Snapshot()
		if e != nil {
			stop()
			continue
		}

		panes := []hub.Pane{}
		for _, p := range t.Panes {
			if p.Agent == "" || skipHermes && p.Agent == "hermes" {
				continue
			}
			status := p.Status
			switch status {
			case "working", "blocked", "idle", "done":
			default:
				status = "idle"
			}
			panes = append(panes, hub.Pane{ID: p.PaneID, Provider: p.SessionID(), Agent: p.Agent, Project: t.Workspaces[p.WorkspaceID], Tab: t.Tabs[p.TabID], Dir: p.Dir(), Status: status})
		}
		// Reported as a copy: status changes below edit panes in place, and the
		// merged list is read by the other runtime's capture.
		if e = topo.report("tui", append([]hub.Pane(nil), panes...)); e != nil {
			stop()
			return e
		}

		timer := time.NewTimer(30 * time.Second)
	watch:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				stop()
				return nil
			case <-timer.C:
				break watch
			case ev, ok := <-events:
				if !ok {
					break watch
				}
				if ev.Event != "pane_agent_status_changed" && ev.Event != "pane.agent_status_changed" {
					break watch
				}
				var change struct {
					Pane   string `json:"pane_id"`
					Status string `json:"agent_status"`
					Agent  string `json:"agent"`
				}
				if json.Unmarshal(ev.Data, &change) != nil {
					break watch
				}
				switch change.Status {
				case "working", "idle", "done", "blocked":
				default:
					break watch
				}
				found := false
				for i := range panes {
					if panes[i].ID == change.Pane {
						if change.Agent != "" && change.Agent != panes[i].Agent {
							break watch
						}
						panes[i].Status = change.Status
						found = true
						break
					}
				}
				if !found {
					break watch
				}
				if e = topo.report("tui", append([]hub.Pane(nil), panes...)); e != nil {
					timer.Stop()
					stop()
					return e
				}
			}
		}
		timer.Stop()
		stop()

	}
}

func collectorNamespace(q *hub.Queue) string {
	s := q.Snapshot()
	if s.Client != "" {
		return s.Client
	}
	return s.Generation
}
