package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const sessionCookie = "amythest_kanban_session"

// session mirrors kanban.py's cache file field-for-field so either client can
// reuse a session the other minted. exp is a float because Python writes
// time.time(); base pins the session to one server.
type session struct {
	Cookie string  `json:"cookie"`
	CSRF   string  `json:"csrf"`
	Exp    float64 `json:"exp"`
	Base   string  `json:"base"`
}

func (c *Client) loadSession() *session {
	raw, err := os.ReadFile(c.cfg.SessionFile)
	if err != nil {
		return nil
	}
	var s session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	if s.Cookie == "" || s.CSRF == "" {
		return nil
	}
	if s.Exp < float64(c.now().Unix()+60) {
		return nil
	}
	if s.Base != c.cfg.KanbanBase() {
		return nil
	}
	return &s
}

func (c *Client) storeSession(cookie, csrf string) (*session, error) {
	s := session{
		Cookie: cookie,
		CSRF:   csrf,
		Exp:    float64(c.now().Unix()) + 7*3600,
		Base:   c.cfg.KanbanBase(),
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(c.cfg.SessionFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	tmp := c.cfg.SessionFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, c.cfg.SessionFile); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	return &s, nil
}

func (c *Client) credentials() (user, password string, err error) {
	user = os.Getenv("KANBAN_USERNAME")
	password = os.Getenv("KANBAN_PASSWORD")
	if user != "" && password != "" {
		return user, password, nil
	}
	values, ferr := parseEnvFile(c.cfg.EnvFile)
	if ferr != nil {
		return "", "", fmt.Errorf("no credentials: set KANBAN_USERNAME/KANBAN_PASSWORD or make %s readable", c.cfg.EnvFile)
	}
	if user == "" {
		user = values["KANBAN_USERNAME"]
	}
	if password == "" {
		password = values["KANBAN_PASSWORD"]
	}
	if user == "" || password == "" {
		return "", "", fmt.Errorf("KANBAN_USERNAME/KANBAN_PASSWORD not found in %s", c.cfg.EnvFile)
	}
	return user, password, nil
}

func parseEnvFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `'"`)
	}
	return values, nil
}

// login authenticates and caches the new session. Callers hold c.mu.
func (c *Client) login(ctx context.Context) (*session, error) {
	user, password, err := c.credentials()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"username": user, "password": password})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.KanbanBase()+"/api/login", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", c.cfg.Endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}
	var payload struct {
		User string `json:"user"`
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode login response: %w", err)
	}
	cookie := ""
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			cookie = ck.Name + "=" + ck.Value
		}
	}
	if cookie == "" {
		return nil, fmt.Errorf("login succeeded but no session cookie was returned")
	}
	c.user = payload.User
	return c.storeSession(cookie, payload.CSRF)
}
