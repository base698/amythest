package server

import (
	"net/http"
	"time"
)

// requireKanbanSession authorizes a request that mutates the vault. Vault
// writes fail closed: without configured kanban credentials there are no
// sessions, so editing is disabled entirely rather than open to anyone who
// can reach the listener.
func (s *Server) requireKanbanSession(w http.ResponseWriter, r *http.Request, action string) bool {
	if s.kanbanAuth == nil {
		http.Error(w, "editing is disabled: kanban credentials are not configured", http.StatusForbidden)
		return false
	}
	cookie, err := r.Cookie(kanbanSessionCookie)
	if err != nil {
		http.Error(w, "sign in to the kanban to "+action, http.StatusUnauthorized)
		return false
	}
	if _, err := s.kanbanAuth.Verify(cookie.Value, time.Now()); err != nil {
		http.Error(w, "session expired: sign in to the kanban again", http.StatusUnauthorized)
		return false
	}
	return true
}
