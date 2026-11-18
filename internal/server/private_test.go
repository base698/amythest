package server

import (
	"encoding/json"
	"github.com/base698/amythest/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func privateTestServer(t *testing.T) *Server {
	t.Setenv("AMYTHEST_USERNAME", "owner")
	t.Setenv("AMYTHEST_PASSWORD", "correct horse")
	t.Setenv("AMYTHEST_SESSION_SECRET", "01234567890123456789012345678901")
	t.Setenv("KANBAN_USERNAME", "")
	t.Setenv("KANBAN_PASSWORD", "")
	t.Setenv("KANBAN_SESSION_SECRET", "")
	return hardenedServer(t, func(c *config.Config) { c.AuthMode = "private"; c.AuthPublicURL = "https://notes.example.com" })
}
func TestPrivateAuthSessionRoutesAndCSRF(t *testing.T) {
	s := privateTestServer(t)
	request := func(method, path, body string, cookie *http.Cookie, csrf, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/search?q=Note", "/api/note?slug=Note", "/kanban/api/boards", "/api/auth/credentials"} {
		if w := request("GET", path, "", nil, "", ""); w.Code != 401 {
			t.Fatalf("%s %d", path, w.Code)
		}
	}
	for _, path := range []string{"/Note", "/assets/private.txt", "/presentations/test"} {
		if w := request("GET", path, "", nil, "", ""); w.Code != 303 {
			t.Fatalf("%s %d", path, w.Code)
		}
	}
	if w := request("GET", "/metrics", "", nil, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "amythest_") {
		t.Fatalf("/metrics %d %s", w.Code, w.Body.String())
	}
	w := request("POST", "/kanban/api/login", `{"username":"owner","password":"correct horse"}`, nil, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != 90*86400 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("incorrect cookie", cookies)
	}
	cookie := cookies[0]
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	csrf := session["csrf"]
	for _, path := range []string{"/api/search?q=Note", "/Note", "/kanban/api/boards", "/settings/access"} {
		if w = request("GET", path, "", cookie, "", ""); w.Code != 200 {
			t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
		}
	}
	if w = request("GET", "/api/search?q=Note", "", cookie, "", "wrong"); w.Code != 401 {
		t.Fatal("invalid bearer fell back to cookie")
	}
	if w = request("POST", "/api/auth/credentials", `{"action":"create","kind":"reader","label":"test"}`, cookie, "", ""); w.Code != 403 {
		t.Fatal("missing csrf accepted")
	}
	w = request("POST", "/api/auth/credentials", `{"action":"create","kind":"reader","label":"test"}`, cookie, csrf, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	r := httptest.NewRequest("POST", "/api/auth/logout", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin logout accepted")
	}
	if w = request("POST", "/api/auth/logout", "", cookie, csrf, ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w = request("GET", "/api/auth/session", "", cookie, "", ""); w.Code != 401 {
		t.Fatal("logout did not revoke")
	}
}
func TestPrivateReaderMCPAndCollectorIsolation(t *testing.T) {
	s := privateTestServer(t)
	reader, cred, e := s.kanbanAuth.CreateCredential("reader", "reader", time.Now(), time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	collector, _, _ := s.kanbanAuth.CreateCredential("collector", "collector", time.Now(), time.Hour)
	call := func(token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", "2025-03-26")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w := call(reader, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "search_notes") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, name := range []string{"create_note", "toggle_task", "kanban_archive", "query_sql", "present_content"} {
		if strings.Contains(w.Body.String(), `"name":"`+name+`"`) {
			t.Fatal("reader discovered forbidden tool", name)
		}
	}
	w = call(reader, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_note","arguments":{"title":"attack","body":"bad"}}}`)
	if !strings.Contains(w.Body.String(), "error") {
		t.Fatal("reader invoked write", w.Body.String())
	}
	w = call(reader, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_notes","arguments":{"query":"Note"}}}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(collector, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); w.Code != 403 {
		t.Fatal("collector gained MCP", w.Code)
	}
	s.kanbanAuth.Revoke(cred.ID, time.Now())
	if w = call(reader, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); w.Code != 401 {
		t.Fatal("revoked reader accepted", w.Code)
	}
}

func TestPrivatePairingApprovalAndHostPrefix(t *testing.T) {
	s := privateTestServer(t)
	request := func(path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w := request("/api/auth/registrations", `{"label":"Laptop","kind":"collector"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var pair map[string]any
	json.Unmarshal(w.Body.Bytes(), &pair)
	if !strings.HasPrefix(pair["verification_uri"].(string), "https://notes.example.com/settings/access?code=") {
		t.Fatal(pair)
	}
	w = request("/api/auth/registrations/decision", `{"action":"decide","approve":true,"code":"`+pair["user_code"].(string)+`"}`, nil, "")
	if w.Code != 401 {
		t.Fatal("anonymous approval", w.Code)
	}
	token, session, e := s.kanbanAuth.NewSession(time.Now())
	if e != nil {
		t.Fatal(e)
	}
	cookie := &http.Cookie{Name: kanbanSessionCookie, Value: token}
	body := `{"action":"decide","approve":true,"code":"` + pair["user_code"].(string) + `"}`
	w = request("/api/auth/registrations/decision", body, cookie, session.CSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("/api/auth/registrations/token", `{"device_code":"`+pair["device_code"].(string)+`"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Token string `json:"token"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	c, e := s.kanbanAuth.Credential(result.Token, time.Now())
	if e != nil || c.Kind != "collector" {
		t.Fatal(c, e)
	}
	s.cfg.BaseURL = "/notes"
	if s.safeNext("https://evil.example") != "/notes/" || s.safeNext("//evil.example") != "/notes/" || s.safeNext("/notes/kanban/") != "/notes/kanban/" {
		t.Fatal("unsafe redirect")
	}
}
