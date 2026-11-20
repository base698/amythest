package apiclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ---------------------------------------------------------------------- work
//
// The Work module records what agents produced: runs, commits, images and
// other artifacts, grouped by project. These are the read-only views the
// terminal client shows; the types mirror the server's JSON and are kept here
// so the client does not link the server's database code.

type WorkArtifact struct {
	ID          string `json:"id"`
	Key         string `json:"identity_key"`
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	Title       string `json:"title"`
	Repo        string `json:"repo"`
	MediaHash   string `json:"media_hash"`
	RemotePath  string `json:"remote_path"`
	LinkID      string `json:"link_id"`
	RunID       string `json:"run_id"`
	Card        string `json:"card_id"`
	Attribution string `json:"attribution"`
	Featured    bool   `json:"featured"`
	Available   bool   `json:"available"`
	Created     string `json:"created_at"`
}

type WorkRun struct {
	ID        string `json:"id"`
	Collector string `json:"collector"`
	Session   string `json:"session_id"`
	Card      string `json:"card_id"`
	Project   string `json:"project"`
	Started   string `json:"started_at"`
	Finished  string `json:"finished_at"`
	Outcome   string `json:"outcome"`
	// A finished run needs review while its output is newer than what was reviewed.
	OutputVersion int64 `json:"output_version"`
	Reviewed      int64 `json:"reviewed_through_version"`
	Historical    bool  `json:"historical"`
}

type WorkSession struct {
	ID        string `json:"session_id"`
	Collector string `json:"collector"`
	Agent     string `json:"agent"`
	Provider  string `json:"provider_ref"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Project   string `json:"project"`
}

type WorkSlot struct {
	Collector string `json:"collector"`
	Pane      string `json:"pane"`
	Session   string `json:"session_id"`
	Status    string `json:"status"`
	Project   string `json:"project"`
	Tab       string `json:"tab"`
	// Binding is the card this pane's runs are filed under, when assigned.
	Binding *struct {
		Card         string `json:"card_id"`
		Acknowledged bool   `json:"acknowledged"`
	} `json:"binding,omitempty"`
}

type WorkMachine struct {
	ID     string `json:"collector"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

type WorkSnapshot struct {
	Projects  []string       `json:"projects"`
	Sessions  []WorkSession  `json:"sessions"`
	Runs      []WorkRun      `json:"runs"`
	Artifacts []WorkArtifact `json:"artifacts"`
	Documents []WorkDocument `json:"documents,omitempty"`
	Slots     []WorkSlot     `json:"slots"`
	Machines  []WorkMachine  `json:"machines"`
}

// WorkOverview returns projects, sessions and live slots without artifacts.
func (c *Client) WorkOverview(ctx context.Context) (*WorkSnapshot, error) {
	var out WorkSnapshot
	if err := c.do(ctx, http.MethodGet, "/api/work/snapshot?summary=1", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WorkProject returns one project's recent runs and artifacts, newest first.
func (c *Client) WorkProject(ctx context.Context, project string) (*WorkSnapshot, error) {
	var out WorkSnapshot
	if err := c.do(ctx, http.MethodGet, "/api/work/snapshot?project="+url.QueryEscape(project), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WorkMedia downloads an uploaded artifact (an image, a video, a commit's
// diff) by content hash, refusing anything over limit bytes.
func (c *Client) WorkMedia(ctx context.Context, hash string, limit int64) ([]byte, error) {
	if len(hash) != 64 {
		return nil, fmt.Errorf("invalid media hash")
	}
	sess, err := c.ensureSession(ctx, false)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Endpoint+"/api/work/media/"+hash, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Cookie", sess.Cookie)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, wrapUnavailableTransport(err)
		}
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && attempt == 0 {
			resp.Body.Close()
			if sess, err = c.ensureSession(ctx, true); err != nil {
				return nil, err
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, decodeError(resp)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > limit {
			return nil, fmt.Errorf("media larger than %d MB", limit>>20)
		}
		return b, nil
	}
	return nil, fmt.Errorf("authentication failed")
}

// WorkRun returns the snapshot for one run, however old.
func (c *Client) WorkRun(ctx context.Context, run string) (*WorkSnapshot, error) {
	var out WorkSnapshot
	if err := c.do(ctx, http.MethodGet, "/api/work/snapshot?run_id="+url.QueryEscape(run), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WorkHit is one match in what agents said and produced.
type WorkHit struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Body    string `json:"body"` // a snippet around the match
	Run     string `json:"run_id"`
	Created string `json:"created_at"`
	Card    string `json:"card_id"`
	Project string `json:"project"`
}

// WorkSearch searches agent chats, commits, artifacts and observations.
func (c *Client) WorkSearch(ctx context.Context, query string, limit int) ([]WorkHit, error) {
	var out []WorkHit
	path := fmt.Sprintf("/api/work/search?q=%s&limit=%d", url.QueryEscape(query), limit)
	if c.scope != nil {
		path += "&project=" + url.QueryEscape(c.scope.Project)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if c.scope != nil {
		filtered := make([]WorkHit, 0)
		for _, hit := range out {
			if strings.EqualFold(hit.Project, c.scope.Project) {
				filtered = append(filtered, hit)
			}
		}
		out = filtered
	}
	return out, nil
}

// VaultHit is one match among notes, cards and tasks.
type VaultHit struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Excerpt  string `json:"excerpt"` // plain text; \x02 and \x03 bracket each match
	Kind     string `json:"kind"`    // note, card or task
	Date     string `json:"date"`
	URL      string `json:"url"` // relative to the endpoint
	Archived bool   `json:"archived"`
}

// VaultSearch searches notes, cards and tasks.
func (c *Client) VaultSearch(ctx context.Context, query string) ([]VaultHit, error) {
	var out []VaultHit
	path, err := c.searchPath(ctx, query)
	if err != nil {
		return nil, err
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return c.scopeVault(ctx, out)
}

// CardTitles finds the titles of cards by id across the boards. Cards that
// are not on an open board are simply absent from the result.
func (c *Client) CardTitles(ctx context.Context, ids []string) (map[string]string, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := map[string]string{}
	if len(want) == 0 {
		return out, nil
	}
	boards, err := c.ListBoards(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range boards {
		if b.Archived {
			continue
		}
		full, err := c.GetBoard(ctx, b.Name)
		if err != nil {
			continue // one unreadable board must not hide the others' cards
		}
		for _, card := range full.Cards {
			if want[card.ID] {
				out[card.ID] = card.Title
			}
		}
		if len(out) == len(want) {
			break
		}
	}
	return out, nil
}

// WorkDocument is an agent's chat summary or observation from a run.
type WorkDocument struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	RunID   string `json:"run_id"`
	Created string `json:"created_at"`
}

// Milestone is a named stretch of work on a project ("production deploy"),
// open until it is ended.
type Milestone struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Name    string `json:"name"`
	Started string `json:"started_at"`
	Ended   string `json:"ended_at,omitempty"`
}

// Milestones lists milestones newest first: one project's ("" for all),
// only open ones when open is set.
func (c *Client) Milestones(ctx context.Context, project string, open bool) ([]Milestone, error) {
	q := url.Values{"project": {project}}
	if open {
		q.Set("open", "1")
	}
	var out []Milestone
	return out, c.do(ctx, http.MethodGet, "/api/work/milestones?"+q.Encode(), nil, &out)
}

// StartMilestone opens a milestone on a project (or returns the open one).
func (c *Client) StartMilestone(ctx context.Context, project, name string) (Milestone, error) {
	var out Milestone
	return out, c.do(ctx, http.MethodPost, "/api/work/milestones", map[string]string{"project": project, "name": name}, &out)
}

// EndMilestone closes a milestone.
func (c *Client) EndMilestone(ctx context.Context, id string) (Milestone, error) {
	var out Milestone
	return out, c.do(ctx, http.MethodPost, "/api/work/milestones/end", map[string]string{"id": id}, &out)
}

// CollectMilestone is the project's work within a milestone's window.
func (c *Client) CollectMilestone(ctx context.Context, id string) (Milestone, *WorkSnapshot, error) {
	var out struct {
		Milestone Milestone    `json:"milestone"`
		Snapshot  WorkSnapshot `json:"snapshot"`
	}
	err := c.do(ctx, http.MethodGet, "/api/work/milestones/collect?id="+url.QueryEscape(id), nil, &out)
	return out.Milestone, &out.Snapshot, err
}
