package apiclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
}

type WorkSession struct {
	ID        string `json:"session_id"`
	Collector string `json:"collector"`
	Agent     string `json:"agent"`
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
