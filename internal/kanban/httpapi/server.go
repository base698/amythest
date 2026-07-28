package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/base698/amythest/internal/kanban/auth"
	"github.com/base698/amythest/internal/kanban/board"
)

const (
	SessionCookie            = "amythest_kanban_session"
	maxLoginFailureEntries   = 1024
	maxAttachmentRequestSize = board.MaxAttachmentSize + 64*1024
)

type Config struct {
	Store *board.Store
	Auth   *auth.Manager
	Assets fs.FS
	Now          func() time.Time
	CookieSecure bool
}

type server struct {
	config   Config
	loginMu  sync.Mutex
	failures map[string]loginFailures
}

type loginFailures struct {
	Count int
	Reset time.Time
}

func New(config Config) http.Handler {
	if config.Now == nil {
		config.Now = time.Now
	}
	s := &server{config: config, failures: map[string]loginFailures{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/api/login", s.login)
	mux.HandleFunc("/api/logout", s.logout)
	mux.HandleFunc("/api/session", s.session)
	mux.HandleFunc("/api/boards", s.boards)
	mux.HandleFunc("/api/boards/", s.boardRoutes)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "not found") })
	mux.HandleFunc("/", s.spa)
	return securityHeaders(mux)
}

func HostRouter(app, preview http.Handler, previewHosts []string, forcePreview bool) http.Handler {
	allowed := map[string]bool{}
	for _, host := range previewHosts {
		allowed[strings.ToLower(strings.TrimSpace(host))] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if split, _, err := net.SplitHostPort(host); err == nil {
			host = split
		} else if strings.Count(host, ":") == 1 {
			host = strings.Split(host, ":")[0]
		}
		if forcePreview || allowed[host] {
			preview.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	})
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	if s.loginLimited(ip) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input, 4096); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.config.Auth.Authenticate(input.Username, input.Password) {
		s.recordLoginFailure(ip)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.clearLoginFailures(ip)
	token, session, err := s.config.Auth.NewSession(s.config.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: token, Path: "/", MaxAge: 8 * 60 * 60, HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]string{"user": session.User, "csrf": session.CSRF})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	session, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !checkCSRF(r, session) {
		writeError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	session, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"user": session.User, "csrf": session.CSRF})
}

func (s *server) boards(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/boards" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
		return
	}
	session, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !checkCSRF(r, session) {
			writeError(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
		var input struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(w, r, &input, 1024); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		created, err := s.config.Store.CreateBoard(input.Name)
		if errors.Is(err, board.ErrBoardExists) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	boards, err := s.config.Store.ListBoards()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load boards")
		return
	}
	writeJSON(w, http.StatusOK, boards)
}

func (s *server) boardRoutes(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/boards/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	boardName := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		value, err := s.config.Store.Load(boardName)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
		return
	}
	if r.Method != http.MethodGet && !checkCSRF(r, session) {
		writeError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	if len(parts) == 2 && parts[1] == "archive" && r.Method == http.MethodGet {
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 100 {
				writeError(w, http.StatusBadRequest, "limit must be 1-100")
				return
			}
			limit = value
		}
		cards, err := s.config.Store.ListArchived(boardName, r.URL.Query().Get("q"), limit)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cards)
		return
	}
	if len(parts) == 4 && parts[1] == "archive" && parts[3] == "restore" && r.Method == http.MethodPost {
		input := struct {
			Status board.Status `json:"status"`
		}{Status: board.Triage}
		if err := decodeJSON(w, r, &input, 1024); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		card, err := s.config.Store.RestoreCard(boardName, parts[2], input.Status)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, card)
		return
	}
	if len(parts) == 2 && parts[1] == "settings" && r.Method == http.MethodPut {
		var input struct {
			DispatchEnabled bool `json:"dispatchEnabled"`
		}
		if err := decodeJSON(w, r, &input, 1024); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		value, err := s.config.Store.UpdateBoardSettings(boardName, input.DispatchEnabled)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
		return
	}
	if len(parts) == 2 && parts[1] == "cards" && r.Method == http.MethodPost {
		var input board.CardInput
		if err := decodeJSON(w, r, &input, 12*1024); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		card, err := s.config.Store.CreateCard(boardName, input)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, card)
		return
	}
	if len(parts) == 4 && parts[1] == "cards" && parts[3] == "attachments" {
		cardID := parts[2]
		if r.Method == http.MethodGet {
			attachments, err := s.config.Store.ListAttachments(boardName, cardID)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, attachments)
			return
		}
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentRequestSize)
			if err := r.ParseMultipartForm(board.MaxAttachmentSize); err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					writeError(w, http.StatusRequestEntityTooLarge, "attachment request is too large")
				} else {
					writeError(w, http.StatusBadRequest, "invalid multipart upload")
				}
				return
			}
			if r.MultipartForm != nil {
				defer r.MultipartForm.RemoveAll()
			}
			files := r.MultipartForm.File["file"]
			if len(files) != 1 || len(r.MultipartForm.File) != 1 {
				writeError(w, http.StatusBadRequest, "exactly one file is required")
				return
			}
			header := files[0]
			if header.Size > board.MaxAttachmentSize {
				writeError(w, http.StatusRequestEntityTooLarge, "attachment is too large")
				return
			}
			file, err := header.Open()
			if err != nil {
				writeError(w, http.StatusBadRequest, "could not read attachment")
				return
			}
			defer file.Close()
			probe := make([]byte, 512)
			read, err := io.ReadFull(file, probe)
			if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "could not read attachment")
				return
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				writeError(w, http.StatusBadRequest, "could not read attachment")
				return
			}
			created, err := s.config.Store.AddAttachment(boardName, cardID, header.Filename, http.DetectContentType(probe[:read]), file, header.Size)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, created)
			return
		}
	}
	if len(parts) == 5 && parts[1] == "cards" && parts[3] == "attachments" {
		cardID, attachmentID := parts[2], parts[4]
		if r.Method == http.MethodGet {
			file, attachment, err := s.config.Store.OpenAttachment(boardName, cardID, attachmentID)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			defer file.Close()
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Filename}))
			w.Header().Set("Content-Type", attachment.ContentType)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Length", strconv.FormatInt(attachment.Size, 10))
			http.ServeContent(w, r, attachment.Filename, attachment.CreatedAt, file)
			return
		}
		if r.Method == http.MethodDelete {
			if err := s.config.Store.DeleteAttachment(boardName, cardID, attachmentID); err != nil {
				writeStoreError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	if len(parts) == 4 && parts[1] == "cards" && parts[3] == "move" && r.Method == http.MethodPost {
		var input struct {
			Status   board.Status `json:"status"`
			BeforeID string       `json:"beforeId"`
		}
		if err := decodeJSON(w, r, &input, 1024); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		card, err := s.config.Store.MoveCard(boardName, parts[2], input.Status, input.BeforeID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, card)
		return
	}
	if len(parts) == 4 && parts[1] == "cards" && parts[3] == "board" && r.Method == http.MethodPost {
		var input struct {
			DestinationBoard string `json:"destinationBoard"`
			Confirm          bool   `json:"confirm"`
		}
		if err := decodeJSON(w, r, &input, 1024); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !input.Confirm {
			writeError(w, http.StatusBadRequest, "explicit confirmation is required")
			return
		}
		card, err := s.config.Store.MoveCardToBoard(boardName, input.DestinationBoard, parts[2], session.User)
		if errors.Is(err, board.ErrCardExists) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, card)
		return
	}
	if len(parts) == 3 && parts[1] == "cards" && r.Method == http.MethodDelete {
		if _, err := s.config.Store.DeleteCard(boardName, parts[2]); err != nil {
			writeStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 3 && parts[1] == "cards" && r.Method == http.MethodPut {
		var patch board.CardPatch
		if err := decodeJSON(w, r, &patch, 12*1024); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		card, err := s.config.Store.UpdateCard(boardName, parts[2], patch)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, card)
		return
	}
	if len(parts) == 4 && parts[1] == "cards" && parts[3] == "comments" && r.Method == http.MethodPost {
		var input struct {
			Body string `json:"body"`
		}
		if err := decodeJSON(w, r, &input, 4096); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		card, err := s.config.Store.AddComment(boardName, parts[2], session.User, input.Body)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, card)
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *server) spa(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(s.config.Assets, name)
	if err != nil {
		data, err = fs.ReadFile(s.config.Assets, "index.html")
		name = "index.html"
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(data)
}

func (s *server) requireSession(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return auth.Session{}, false
	}
	session, err := s.config.Auth.Verify(cookie.Value, s.config.Now())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return auth.Session{}, false
	}
	return session, true
}

func (s *server) loginLimited(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	s.pruneLoginFailuresLocked(s.config.Now())
	entry := s.failures[ip]
	return entry.Count >= 5 && s.config.Now().Before(entry.Reset)
}
func (s *server) recordLoginFailure(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := s.config.Now()
	s.pruneLoginFailuresLocked(now)
	if _, exists := s.failures[ip]; !exists && len(s.failures) >= maxLoginFailureEntries {
		var oldestIP string
		var oldestReset time.Time
		for candidate, entry := range s.failures {
			if oldestIP == "" || entry.Reset.Before(oldestReset) {
				oldestIP, oldestReset = candidate, entry.Reset
			}
		}
		delete(s.failures, oldestIP)
	}
	entry := s.failures[ip]
	if !now.Before(entry.Reset) {
		entry = loginFailures{Reset: now.Add(5 * time.Minute)}
	}
	entry.Count++
	s.failures[ip] = entry
}
func (s *server) clearLoginFailures(ip string) {
	s.loginMu.Lock()
	delete(s.failures, ip)
	s.loginMu.Unlock()
}

func (s *server) pruneLoginFailuresLocked(now time.Time) {
	for ip, entry := range s.failures {
		if !now.Before(entry.Reset) {
			delete(s.failures, ip)
		}
	}
}

func checkCSRF(r *http.Request, session auth.Session) bool {
	return session.CSRF != "" && r.Header.Get("X-CSRF-Token") == session.CSRF
}
func decodeJSON(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid JSON payload")
	}
	var extra any
	if decoder.Decode(&extra) == nil {
		return errors.New("payload must contain one JSON object")
	}
	return nil
}
func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}
