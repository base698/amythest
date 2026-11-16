package hub

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Credential struct {
	Server     string `json:"server"`
	Token      string `json:"token"`
	Credential struct {
		ID       string `json:"id"`
		ClientID string `json:"client_id"`
		Kind     string `json:"kind"`
		Label    string `json:"label"`
	} `json:"credential"`
}
type Client struct {
	Credential Credential
	HTTP       *http.Client
}

func Load(path string) (*Client, error) {
	info, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("credential file must be private (chmod 600)")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var c Credential
	if e = json.Unmarshal(b, &c); e != nil {
		return nil, e
	}
	u, e := url.Parse(c.Server)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid credential server")
	}
	local := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		local = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("collector server requires HTTPS")
	}
	if c.Token == "" || c.Credential.Kind != "collector" {
		return nil, errors.New("collector credential required")
	}
	if c.Credential.ClientID == "" {
		c.Credential.ClientID = c.Credential.ID
	}
	if c.Credential.ClientID == "" {
		return nil, errors.New("credential client ID required")
	}
	c.Server = strings.TrimRight(c.Server, "/")
	return &Client{Credential: c, HTTP: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, c.Credential.Server+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+c.Credential.Token)
	req.Header.Set("Content-Type", "application/json")
	res, e := c.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("hub %s: HTTP %d; queue retained (check Work access or stream state)", path, res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
	}
	return nil
}
func (c *Client) Sync(q *Queue) error {
	s := q.Snapshot()
	if s.Client != "" && (s.Client != c.Credential.Credential.ClientID || s.Server != c.Credential.Server) {
		return errors.New("credential belongs to a different collector/server; queue must not be reassigned")
	}
	if s.Client == "" {
		if e := q.Update(func(s *State) error {
			s.Client = c.Credential.Credential.ClientID
			s.Server = c.Credential.Server
			return nil
		}); e != nil {
			return e
		}
	}
	var ack struct {
		Generation string `json:"generation"`
		Acked      int64  `json:"acked"`
	}
	if e := c.Do("POST", "/api/work/streams", map[string]string{"generation": s.Generation}, &ack); e != nil {
		return e
	}
	if e := q.Ack(ack.Generation, ack.Acked); e != nil {
		return e
	}
	s = q.Snapshot()
	if e := c.Do("POST", "/api/work/heartbeat", map[string]any{"name": c.Credential.Credential.Label, "syncing": true}, nil); e != nil {
		return e
	}
	for len(s.Events) > 0 {
		batch := s.Events[:min(100, len(s.Events))]
		for {
			b, _ := json.Marshal(batch)
			if len(b) < (1900 << 10) {
				break
			}
			if len(batch) == 1 {
				return errors.New("queued event exceeds hub limit")
			}
			batch = batch[:len(batch)/2]
		}
		if e := c.Do("POST", "/api/work/events", map[string]any{"generation": s.Generation, "events": batch}, &ack); e != nil {
			return e
		}
		if ack.Acked < batch[len(batch)-1].Seq {
			return errors.New("hub did not acknowledge the complete batch")
		}
		if e := q.Ack(ack.Generation, ack.Acked); e != nil {
			return e
		}
		s = q.Snapshot()
	}
	var bindings []Binding
	if e := c.Do("GET", "/api/work/bindings", nil, &bindings); e != nil {
		return e
	}
	if e := q.Bind(bindings); e != nil {
		return e
	}
	for _, b := range bindings {
		if e := c.Do("POST", "/api/work/bindings/ack", map[string]any{"id": b.ID, "version": b.Version}, nil); e != nil {
			return e
		}
	}
	// Inactivity is intentionally omitted until the adapter has an actual output
	// clock. Time since a status transition is not evidence of a stalled agent.
	return c.Do("POST", "/api/work/heartbeat", map[string]any{"name": c.Credential.Credential.Label, "syncing": len(q.Snapshot().Events) > 0 || time.Since(q.Snapshot().InventoryAt) > time.Minute}, nil)
}
