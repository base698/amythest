package server

import (
	"crypto/subtle"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/base698/amythest/internal/kanban/auth"
)

type authWindow struct {
	start time.Time
	count int
}
type authLimiter struct {
	mu      sync.Mutex
	windows map[string]authWindow
}

func (l *authLimiter) allow(r *http.Request) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	if l.windows == nil {
		l.windows = map[string]authWindow{}
	}
	for k, v := range l.windows {
		if now.Sub(v.start) > time.Minute {
			delete(l.windows, k)
		}
	}
	v, exists := l.windows[host]
	if !exists {
		if len(l.windows) >= 1024 {
			return false
		}
		v.start = now
	}
	v.count++
	l.windows[host] = v
	return v.count <= 120
}

func (s *Server) privateGuard(next http.Handler) http.Handler {
	loginLimit := &authLimiter{}
	pairLimit := &authLimiter{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if s.isProbePath(r.URL.Path) {
			if r.URL.Path == s.cfg.LivenessPath {
				s.handleLiveness(w, r)
			} else {
				s.handleHealthLegacy(w, r)
			}
			return
		}
		if r.URL.Path == "/metrics" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.sameOrigin(r) {
			http.Error(w, "request origin is not allowed", 403)
			return
		}
		if r.URL.Path == "/login" || r.URL.Path == "/api/auth/login" || r.URL.Path == "/kanban/api/login" {
			if r.Method == http.MethodPost && !loginLimit.allow(r) {
				http.Error(w, "too many attempts; try again in a minute", 429)
				return
			}
			s.privateLogin(w, r)
			return
		}
		if r.URL.Path == "/api/auth/registrations" || r.URL.Path == "/api/auth/registrations/token" {
			if !pairLimit.allow(r) {
				http.Error(w, "too many requests", 429)
				return
			}
			s.privatePair(w, r)
			return
		}
		if header := r.Header.Get("Authorization"); header != "" {
			token, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || token == "" {
				http.Error(w, "invalid bearer token", 401)
				return
			}
			c, err := s.kanbanAuth.Credential(token, time.Now())
			if err != nil && s.cfg.LegacyMCPToken && os.Getenv("AMYTHEST_MCP_TOKEN") != "" && subtle.ConstantTimeCompare([]byte(token), []byte(os.Getenv("AMYTHEST_MCP_TOKEN"))) == 1 {
				c = auth.Credential{Kind: "reader"}
				err = nil
			}
			if err != nil || c.Kind == "session" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="amythest"`)
				http.Error(w, "invalid or expired bearer token", 401)
				return
			}
			if c.Kind == "collector" && s.work != nil && collectorRoute(r) {
				next.ServeHTTP(w, collectorRequest(r, c.ClientID))
				return
			}
			// Each preset has an explicit route allowlist.
			if c.Kind != "reader" {
				http.Error(w, "credential does not grant this operation", 403)
				return
			}
			if r.URL.Path == "/mcp" || r.URL.Path == "/mcp/" {
				next.ServeHTTP(w, r)
				return
			}
			// Explicit allowlist: new HTTP endpoints never inherit reader privileges.
			reads := map[string]bool{"/api/search": true, "/api/note": true, "/api/notes": true, "/api/tasks": true, "/api/contentIndex": true}
			if (r.Method == "GET" || r.Method == "HEAD") && (reads[r.URL.Path] || s.work != nil && workReadRoute(r)) {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "reader token does not grant this operation; use MCP for board reads", 403)
			return
		}
		cookie, err := r.Cookie(kanbanSessionCookie)
		var session auth.Session
		if err == nil {
			session, err = s.kanbanAuth.Verify(cookie.Value, time.Now())
		}
		if err != nil {
			if r.Method == "GET" && !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/kanban/api/") && !strings.HasPrefix(r.URL.Path, "/mcp") {
				dest := s.base() + strings.TrimPrefix(r.URL.RequestURI(), "/")
				http.Redirect(w, r, s.base()+"login?next="+url.QueryEscape(dest), http.StatusSeeOther)
			} else {
				http.Error(w, "sign in required", 401)
			}
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			csrf := r.Header.Get("X-CSRF-Token")
			if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
				r.Body = http.MaxBytesReader(w, r.Body, 16384)
				if r.ParseForm() != nil {
					http.Error(w, "invalid form", 400)
					return
				}
				csrf = r.PostForm.Get("csrf")
			}
			if csrf == "" || subtle.ConstantTimeCompare([]byte(csrf), []byte(session.CSRF)) != 1 {
				http.Error(w, "invalid CSRF token", 403)
				return
			}
		}
		if renewed, e := s.kanbanAuth.Renew(cookie.Value, time.Now()); e != nil {
			http.Error(w, "session renewal unavailable", 503)
			return
		} else if renewed {
			s.setPrivateCookie(w, cookie.Value, int(s.kanbanAuth.TTL().Seconds()))
		}
		switch r.URL.Path {
		case "/api/auth/logout", "/kanban/api/logout":
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			if err := s.kanbanAuth.RevokeSession(cookie.Value); err != nil {
				http.Error(w, "could not sign out", 500)
				return
			}
			s.setPrivateCookie(w, "", -1)
			if strings.Contains(r.Header.Get("Content-Type"), "form") {
				http.Redirect(w, r, s.base()+"login", 303)
			} else {
				w.WriteHeader(204)
			}
			return
		case "/api/auth/session", "/kanban/api/session":
			s.writeJSON(w, map[string]any{"user": session.User, "csrf": session.CSRF, "expires": session.Exp})
			return
		case "/settings/access", "/api/auth/credentials", "/api/auth/registrations/decision":
			s.privateSettings(w, r, session)
			return
		}
		// MCP is for agent credentials; browser cookies cannot bypass reader policy.
		if strings.HasPrefix(r.URL.Path, "/mcp") {
			http.Error(w, "use an agent bearer token for MCP", 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if s.cfg.AuthPublicURL != "" {
		p, _ := url.Parse(s.cfg.AuthPublicURL)
		return u.Scheme == p.Scheme && u.Host == p.Host
	}
	scheme := "https"
	if !s.cookieSecure() {
		scheme = "http"
	}
	return u.Host == r.Host && u.Scheme == scheme
}
func (s *Server) cookieSecure() bool { return !s.cfg.AuthAllowHTTP }
func (s *Server) setPrivateCookie(w http.ResponseWriter, value string, maxAge int) {
	c := &http.Cookie{Name: kanbanSessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: s.cookieSecure(), SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
	if maxAge > 0 {
		c.Expires = time.Now().Add(time.Duration(maxAge) * time.Second)
	} else {
		c.Expires = time.Unix(1, 0)
	}
	http.SetCookie(w, c)
}
func (s *Server) privateLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/login" {
		s.authPage(w, authPageData{Base: s.base(), Next: s.safeNext(r.URL.Query().Get("next")), Login: true})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	next := s.base()
	form := strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
	if form {
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		if r.ParseForm() != nil {
			http.Error(w, "invalid form", 400)
			return
		}
		in.Username = r.PostForm.Get("username")
		in.Password = r.PostForm.Get("password")
		next = s.safeNext(r.PostForm.Get("next"))
	} else if jsonDecodeLimit(r, &in, 16384) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if !s.kanbanAuth.Authenticate(in.Username, in.Password) {
		if form {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		w.WriteHeader(401)
		if form {
			s.authPage(w, authPageData{Base: s.base(), Next: next, Login: true, Error: "Username or password was not recognized."})
		} else {
			fmt.Fprintln(w, "invalid credentials")
		}
		return
	}
	token, session, err := s.kanbanAuth.NewSession(time.Now())
	if err != nil {
		http.Error(w, "could not create session", 500)
		return
	}
	s.setPrivateCookie(w, token, int(s.kanbanAuth.TTL().Seconds()))
	if form {
		http.Redirect(w, r, next, 303)
	} else {
		s.writeJSON(w, map[string]string{"user": session.User, "csrf": session.CSRF})
	}
}
func (s *Server) safeNext(next string) string {
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") || !strings.HasPrefix(u.Path, s.base()) {
		return s.base()
	}
	return next
}
func (s *Server) privatePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	if r.URL.Path == "/api/auth/registrations/token" {
		var in struct {
			DeviceCode string `json:"device_code"`
		}
		if jsonDecodeLimit(r, &in, 4096) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		token, c, err := s.kanbanAuth.Exchange(in.DeviceCode, time.Now())
		if err != nil {
			w.WriteHeader(400)
			s.writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		s.writeJSON(w, map[string]any{"token": token, "credential": c})
		return
	}
	if s.cfg.AuthPublicURL == "" {
		http.Error(w, "registration requires auth_public_url", 503)
		return
	}
	var in struct {
		Label string `json:"label"`
		Kind  string `json:"kind"`
	}
	if jsonDecodeLimit(r, &in, 4096) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	reg, err := s.kanbanAuth.Register(in.Label, in.Kind, time.Now())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.writeJSON(w, map[string]any{"device_code": reg.DeviceCode, "user_code": reg.UserCode, "verification_uri": strings.TrimRight(s.cfg.AuthPublicURL, "/") + "/settings/access?code=" + reg.UserCode, "expires_in": 600, "interval": 5})
}
func (s *Server) privateSettings(w http.ResponseWriter, r *http.Request, session auth.Session) {
	d := authPageData{Base: s.base(), CSRF: session.CSRF}
	if r.Method == "POST" {
		var in struct {
			Action  string `json:"action"`
			Label   string `json:"label"`
			Kind    string `json:"kind"`
			ID      string `json:"id"`
			Code    string `json:"code"`
			Approve bool   `json:"approve"`
		}
		form := strings.Contains(r.Header.Get("Content-Type"), "form")
		if form {
			in.Action = r.PostForm.Get("action")
			in.Label = r.PostForm.Get("label")
			in.Kind = r.PostForm.Get("kind")
			in.ID = r.PostForm.Get("id")
			in.Code = r.PostForm.Get("code")
			in.Approve = r.PostForm.Get("approve") == "yes"
		} else if jsonDecodeLimit(r, &in, 8192) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		var err error
		switch in.Action {
		case "create":
			if in.Kind != "reader" && in.Kind != "collector" {
				http.Error(w, "choose reader or collector", 400)
				return
			}
			var c auth.Credential
			d.Token, c, err = s.kanbanAuth.CreateCredential(in.Label, in.Kind, time.Now(), 90*24*time.Hour)
			if err == nil && !form {
				s.writeJSON(w, map[string]any{"token": d.Token, "credential": c})
				return
			}
		case "replace":
			var c auth.Credential
			d.Token, c, err = s.kanbanAuth.ReplaceCredential(in.ID, time.Now())
			if err == nil && !form {
				s.writeJSON(w, map[string]any{"token": d.Token, "credential": c})
				return
			}
		case "revoke":
			err = s.kanbanAuth.Revoke(in.ID, time.Now())
		case "decide":
			err = s.kanbanAuth.Decide(in.Code, in.Approve, time.Now())
		default:
			http.Error(w, "unknown action", 400)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if !form {
			s.writeJSON(w, map[string]bool{"ok": true})
			return
		}
	} else if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}
	creds, err := s.kanbanAuth.Credentials()
	if err != nil {
		http.Error(w, "credentials unavailable", 503)
		return
	}
	d.Credentials = creds
	if r.URL.Path == "/api/auth/credentials" {
		s.writeJSON(w, creds)
		return
	}
	if code := r.URL.Query().Get("code"); code != "" {
		reg, err := s.kanbanAuth.Registration(code, time.Now())
		if err != nil {
			d.Error = "Registration is expired or unknown."
		} else {
			d.Registration = &reg
		}
	}
	s.authPage(w, d)
}

type authPageData struct {
	Base, Next, CSRF, Token, Error string
	Login                          bool
	Credentials                    []auth.Credential
	Registration                   *auth.Registration
}

var authTemplate = template.Must(template.New("auth").Funcs(template.FuncMap{"date": func(t int64) string {
	if t == 0 {
		return "Not yet used"
	}
	return time.Unix(t, 0).UTC().Format("2006-01-02")
}}).Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{if .Login}}Sign in{{else}}Access settings{{end}} · Amythest</title><style>body{font:16px/1.55 system-ui,sans-serif;background:#f7f5f1;color:#292532;margin:0;padding:6vh 6vw}main{max-width:760px;margin:auto}h1{font-size:2rem;letter-spacing:-.04em}a{color:#644d88}section,form.panel{background:white;padding:24px;border:1px solid #ddd6e4;border-radius:12px;margin:20px 0}label{display:block;margin:12px 0}input,select,button{font:inherit;padding:9px 12px;border:1px solid #b9afc4;border-radius:6px}input{max-width:90%}button{cursor:pointer;background:#5d457d;color:white}button.secondary{background:white;color:#5d457d}p.muted,small{color:#625c69}.error{color:#9f243a}code{overflow-wrap:anywhere}li{margin:16px 0;padding:12px 0;border-bottom:1px solid #eee}li form{display:inline}header{display:flex;justify-content:space-between;align-items:center}</style><main>
{{if .Login}}<h1>Welcome back.</h1><p class="muted">Sign in once for your notes, boards and connected agents. Your browser stays signed in while you keep using it.</p>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}<form class="panel" method="post" action="{{.Base}}login"><input type="hidden" name="next" value="{{.Next}}"><label>Username<br><input name="username" autocomplete="username" required autofocus></label><label>Password<br><input name="password" type="password" autocomplete="current-password" required></label><button>Sign in</button></form>
{{else}}<header><a href="{{.Base}}">← Notes</a><form method="post" action="{{.Base}}api/auth/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="secondary">Sign out</button></form></header><h1>Access settings</h1><p class="muted">Manage browsers, readers and collectors. Signing out or revoking access takes effect immediately. Browser sessions renew as you use the app and expire after 90 days away.</p>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
{{with .Registration}}<section><h2>Connect {{.Label}}</h2><p>Code: <strong>{{.UserCode}}</strong> · {{.Kind}} · {{.State}}</p><p>Compare this code with the requesting device. Readers can read notes and boards through MCP. Collectors are reserved for their own Work events and cannot read your notes.</p><form method="post" action="{{$.Base}}settings/access"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="action" value="decide"><input type="hidden" name="code" value="{{.UserCode}}"><button name="approve" value="yes">Approve connection</button> <button class="secondary" name="approve" value="no">Deny</button></form></section>{{end}}
{{if .Token}}<section><h2>Copy your token now</h2><p>This secret is shown only once. Save it in your client's secret settings.</p><code>{{.Token}}</code><p>Send it in the <code>Authorization: Bearer …</code> header. MCP endpoint: <code>{{.Base}}mcp</code>.</p></section>{{end}}
<form class="panel" method="post" action="{{.Base}}settings/access"><h2>Create a credential</h2><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="action" value="create"><label>Name<br><input name="label" maxlength="100" placeholder="Research agent" required></label><label>Access<br><select name="kind"><option value="reader">Agent reader — read-only</option><option value="collector">Collector — Work only</option></select></label><p class="muted">Tokens expire in 90 days. Collectors can send activity when Work is enabled. Replacement tokens keep the same collector identity; revoke the old token after switching.</p><button>Create token</button></form>
<section><h2>Connected credentials</h2><ul>{{range .Credentials}}<li><strong>{{.Label}}</strong> · {{.Kind}}<br><small>Expires {{date .Expires}} · Last used {{date .LastUsed}}</small><form method="post" action="{{$.Base}}settings/access"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="action" value="revoke"><input type="hidden" name="id" value="{{.ID}}"> <button class="secondary">Revoke</button></form>{{if ne .Kind "session"}}<form method="post" action="{{$.Base}}settings/access"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="action" value="replace"><input type="hidden" name="id" value="{{.ID}}"> <button class="secondary">Replace token</button></form>{{end}}</li>{{else}}<li>No credentials.</li>{{end}}</ul></section>{{end}}</main></html>`))

func (s *Server) authPage(w http.ResponseWriter, d authPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = authTemplate.Execute(w, d)
}
