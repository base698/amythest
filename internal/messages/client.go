package messages

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	URL, Token string
	HTTP       *http.Client
}

func NewClient(endpoint, token string) *Client {
	return &Client{strings.TrimRight(endpoint, "/"), strings.TrimSpace(token), &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("messaging redirects are not allowed") }}}
}
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var b io.Reader
	if in != nil {
		raw, e := json.Marshal(in)
		if e != nil {
			return e
		}
		b = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.URL+path, b)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return fmt.Errorf("messaging gateway unavailable at %s", c.URL)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return &Fault{resp.StatusCode, strings.TrimSpace(string(b))}
	}
	if out == nil {
		_, e = io.Copy(io.Discard, resp.Body)
		return e
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}
func (c *Client) Items(ctx context.Context, all bool) ([]Item, error) {
	var out []Item
	p := "/items"
	if all {
		p += "?all=1"
	}
	e := c.Do(ctx, "GET", p, nil, &out)
	return out, e
}
func (c *Client) Thread(ctx context.Context, id string) (Thread, error) {
	var out Thread
	e := c.Do(ctx, "GET", "/thread?conversation="+url.QueryEscape(id), nil, &out)
	return out, e
}
func (c *Client) Draft(ctx context.Context, in DraftInput) (Draft, error) {
	var out Draft
	e := c.Do(ctx, "POST", "/drafts", in, &out)
	return out, e
}
func (c *Client) Send(ctx context.Context, id string) (Draft, error) {
	var out Draft
	e := c.Do(ctx, "POST", "/send", map[string]string{"draft": id}, &out)
	return out, e
}
func (c *Client) Attach(ctx context.Context, name string, b []byte) (Attachment, error) {
	var out Attachment
	e := c.Do(ctx, "POST", "/attachments", struct {
		Name string `json:"name"`
		Data []byte `json:"data"`
	}{name, b}, &out)
	return out, e
}
func (c *Client) Attachments(ctx context.Context) ([]Attachment, error) {
	var out []Attachment
	e := c.Do(ctx, "GET", "/attachments", nil, &out)
	return out, e
}
func (c *Client) Focus(ctx context.Context, id, nick string) error {
	return c.Do(ctx, "POST", "/focus", map[string]string{"message": id, "client": nick}, nil)
}
func (c *Client) Attention(ctx context.Context, id, action string) error {
	return c.Do(ctx, "POST", "/attention", map[string]string{"message": id, "action": action}, nil)
}
