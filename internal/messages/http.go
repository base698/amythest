package messages

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Gateway struct {
	Store                  *Store
	OwnerToken, AgentToken string
	ConnectorToken         string // Backwards-compatible Telegram credential.
	ConnectorTokens        map[string]string
	IRC                    *IRC
}

func Token(dir, name string) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".token")
	if b, e := os.ReadFile(path); e == nil {
		v := strings.TrimSpace(string(b))
		if len(v) != 64 {
			return "", fmt.Errorf("invalid %s token file", name)
		}
		return v, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	v := hex.EncodeToString(b[:])
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(e, os.ErrExist) {
		return Token(dir, name)
	}
	if e != nil {
		return "", e
	}
	_, e = f.WriteString(v + "\n")
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return v, e
}
func sameToken(got, want string) bool {
	return len(want) >= 32 && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/health" && r.Method == "GET" {
			write(w, map[string]string{"status": "ok"})
			return
		}
		// This is a native-client API; browsers must not use ambient credentials.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser requests are not supported", 403)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		actor := ""
		provider := "telegram"
		if sameToken(token, g.OwnerToken) {
			actor = "owner"
		} else if sameToken(token, g.AgentToken) {
			actor = "agent"
		} else if sameToken(token, g.ConnectorToken) {
			actor = "connector"
		}
		if actor == "" {
			for name, want := range g.ConnectorTokens {
				if sameToken(token, want) {
					actor = "connector"
					provider = name
					break
				}
			}
		}
		if actor == "" {
			http.Error(w, "messaging credential required", 401)
			return
		}
		if actor == "connector" {
			allowed := r.Method == "POST" && (r.URL.Path == "/connector/heartbeat" || r.URL.Path == "/connector/conversations" || r.URL.Path == "/connector/ingest" || r.URL.Path == "/connector/claim" || r.URL.Path == "/connector/complete" || r.URL.Path == "/connector/authorize") || r.Method == "GET" && r.URL.Path == "/connector/attachment"
			if !allowed {
				http.Error(w, "connector operation required", 403)
				return
			}
		} else if actor != "owner" {
			allowed := r.Method == "GET" && (r.URL.Path == "/items" || r.URL.Path == "/thread" || r.URL.Path == "/drafts") || r.Method == "POST" && (r.URL.Path == "/drafts" || r.URL.Path == "/send")
			if !allowed {
				http.Error(w, "owner credential required", 403)
				return
			}
		}
		result, err := g.route(w, r, actor, provider)
		if err != nil {
			var f *Fault
			if errors.As(err, &f) {
				http.Error(w, f.Text, f.Status)
			} else {
				http.Error(w, "messaging storage unavailable", 503)
			}
			return
		}
		if result != nil {
			write(w, result)
		}
	})
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	dec.DisallowUnknownFields()
	if e := dec.Decode(v); e != nil {
		return fault(400, "invalid or oversized JSON")
	}
	if e := dec.Decode(new(any)); e != io.EOF {
		return fault(400, "expected one JSON value")
	}
	return nil
}
func (g *Gateway) route(w http.ResponseWriter, r *http.Request, actor, provider string) (any, error) {
	s := g.Store
	switch r.Method + " " + r.URL.Path {
	case "GET /contacts":
		return s.ContactNames()
	case "PUT /contacts":
		var in struct {
			Conversation string `json:"conversation"`
			Name         string `json:"name"`
		}
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		return map[string]bool{"ok": true}, s.NameContact(in.Conversation, in.Name)
	case "POST /connector/authorize":
		var in struct {
			Account string `json:"account"`
			Draft   string `json:"draft"`
			Attempt string `json:"attempt"`
		}
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		return map[string]bool{"ok": true}, s.AuthorizeDelivery(in.Account, in.Draft, in.Attempt, provider)
	case "GET /connectors":
		return s.Connectors()
	case "POST /connector/conversations":
		var in struct {
			Account       string         `json:"account"`
			Conversations []Conversation `json:"conversations"`
		}
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		return map[string]bool{"ok": true}, s.RegisterConversations(provider, in.Account, in.Conversations)
	case "POST /connector/heartbeat":
		var c Connector
		if e := decode(r, &c); e != nil {
			return nil, e
		}
		if c.Provider != "" && c.Provider != provider {
			return nil, fault(403, "credential belongs to another provider")
		}
		c.Provider = provider
		return map[string]bool{"ok": true}, s.Heartbeat(c)
	case "POST /connector/claim":
		var in struct {
			Account string `json:"account"`
			Worker  string `json:"worker"`
		}
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		d, e := s.Claim(in.Account, in.Worker, provider)
		return map[string]any{"delivery": d}, e
	case "POST /connector/complete":
		var in Receipt
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		if in.Provider != "" && in.Provider != provider {
			return nil, fault(403, "credential belongs to another provider")
		}
		in.Provider = provider
		d, e := s.Complete(in)
		if e == nil && d.State == "sent" && g.IRC != nil {
			g.IRC.Broadcast("reply-" + d.ID)
		}
		return d, e
	case "GET /connector/attachment":
		q := r.URL.Query()
		a, b, e := s.DeliveryAttachment(q.Get("account"), q.Get("draft"), q.Get("attempt"), q.Get("id"), provider)
		if e != nil {
			return nil, e
		}
		w.Header().Set("Content-Type", a.MIME)
		_, e = w.Write(b)
		return nil, e
	case "GET /items":
		return s.Items(actor, r.URL.Query().Get("all") == "1", r.URL.Query().Get("trash") == "1")
	case "GET /thread":
		return s.Thread(actor, r.URL.Query().Get("conversation"))
	case "GET /drafts":
		return s.Drafts(actor)
	case "GET /attachments":
		return s.Attachments()
	case "GET /attachment":
		a, b, e := s.Attachment(r.URL.Query().Get("id"))
		if e != nil {
			return nil, e
		}
		w.Header().Set("Content-Type", a.MIME)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
		_, e = w.Write(b)
		return nil, e
	case "POST /attachments":
		var in struct {
			Name string `json:"name"`
			Data []byte `json:"data"`
		}
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		return s.AddAttachment(in.Name, in.Data)
	case "POST /drafts":
		var in DraftInput
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		return s.Draft(actor, in)
	case "POST /send":
		var in struct {
			Draft string `json:"draft"`
		}
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		d, e := s.Send(actor, in.Draft)
		if e == nil && g.IRC != nil {
			g.IRC.Broadcast("reply-" + d.ID)
		}
		return d, e
	case "POST /ingest", "POST /connector/ingest":
		var in Ingress
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		if r.URL.Path == "/connector/ingest" {
			if in.Conversation.Route.Provider != provider {
				return nil, fault(403, "credential belongs to another provider")
			}
			if e := s.ValidateIngress(in); e != nil {
				return nil, e
			}
		} else if in.Conversation.Route.Provider != "" {
			return nil, fault(400, "provider routes must come from a connector")
		}
		m, e := s.Ingest(in)
		if e == nil && g.IRC != nil {
			g.IRC.Broadcast(m.ID)
		}
		return m, e
	case "POST /trash":
		var in struct {
			Conversation string `json:"conversation"`
			Trashed      *bool  `json:"trashed"`
		}
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		if in.Trashed == nil {
			return nil, fault(400, "trashed must be true or false")
		}
		return map[string]bool{"ok": true}, s.SetTrashed(in.Conversation, *in.Trashed)
	case "POST /attention":
		var in struct {
			Message string `json:"message"`
			Action  string `json:"action"`
		}
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		return map[string]bool{"ok": true}, s.Attention(in.Message, in.Action)
	case "POST /grants":
		var in Grant
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		return map[string]bool{"ok": true}, s.Grant(in)
	case "DELETE /grants":
		return map[string]bool{"ok": true}, s.Revoke(r.URL.Query().Get("conversation"))
	case "POST /focus":
		var in struct {
			Message string `json:"message"`
			Client  string `json:"client"`
		}
		if e := decode(r, &in); e != nil {
			return nil, e
		}
		if g.IRC == nil {
			return nil, fault(409, "IRC is not enabled; conversation remains available here")
		}
		return g.IRC.Focus(in.Message, in.Client)
	default:
		return nil, fault(404, "unknown messaging operation")
	}
}
func Shutdown(ctx context.Context, s *http.Server) {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = s.Shutdown(c)
}
