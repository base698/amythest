package server

import (
	"net/http"
	"time"

	"github.com/base698/amythest/internal/kanban/auth"
)

// requireKanbanSession authorizes a request that mutates the vault. Vault
// writes fail closed: without configured kanban credentials there are no
// sessions, so editing is disabled entirely rather than open to anyone who
// can reach the listener.
func (s *Server) requireKanbanSession(w http.ResponseWriter, r *http.Request, action string) bool {
	_, ok := s.verifiedKanbanSession(w, r, action)
	return ok
}

func (s *Server) verifiedKanbanSession(w http.ResponseWriter, r *http.Request, action string) (auth.Session, bool) {
	if s.kanbanAuth == nil {
		http.Error(w, "editing is disabled: kanban credentials are not configured", http.StatusForbidden)
		return auth.Session{}, false
	}
	cookie, err := r.Cookie(kanbanSessionCookie)
	if err != nil {
		http.Error(w, "sign in to the kanban to "+action, http.StatusUnauthorized)
		return auth.Session{}, false
	}
	session, err := s.kanbanAuth.Verify(cookie.Value, time.Now())
	if err != nil {
		http.Error(w, "session expired: sign in to the kanban again", http.StatusUnauthorized)
		return auth.Session{}, false
	}
	return session, true
}

func (s *Server) requireKanbanSessionCSRF(w http.ResponseWriter, r *http.Request, action string) bool {
	session, ok := s.verifiedKanbanSession(w, r, action)
	if !ok {
		return false
	}
	if session.CSRF == "" || r.Header.Get("X-CSRF-Token") != session.CSRF {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return false
	}
	return true
}
