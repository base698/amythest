package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrInvalidSession = errors.New("invalid session")

type Session struct {
	User string `json:"user"`
	Exp  int64  `json:"exp"`
	CSRF string `json:"csrf"`
}

type Manager struct {
	username     string
	passwordHash [32]byte
	secret       []byte
	ttl          time.Duration
}

func NewManager(username, password string, secret []byte, ttl time.Duration) (*Manager, error) {
	if strings.TrimSpace(username) == "" {
		return nil, errors.New("username is required")
	}
	if len(password) < 10 {
		return nil, errors.New("password must be at least 10 characters")
	}
	if len(secret) < 32 {
		return nil, errors.New("session secret must be at least 32 bytes")
	}
	if ttl <= 0 {
		return nil, errors.New("session TTL must be positive")
	}
	return &Manager{username: username, passwordHash: sha256.Sum256([]byte(password)), secret: append([]byte(nil), secret...), ttl: ttl}, nil
}

func (m *Manager) Authenticate(username, password string) bool {
	candidate := sha256.Sum256([]byte(password))
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(m.username))
	passwordOK := subtle.ConstantTimeCompare(candidate[:], m.passwordHash[:])
	return userOK == 1 && passwordOK == 1
}

func (m *Manager) NewSession(now time.Time) (string, Session, error) {
	csrfBytes := make([]byte, 24)
	if _, err := rand.Read(csrfBytes); err != nil {
		return "", Session{}, err
	}
	session := Session{User: m.username, Exp: now.Add(m.ttl).Unix(), CSRF: base64.RawURLEncoding.EncodeToString(csrfBytes)}
	payload, err := json.Marshal(session)
	if err != nil {
		return "", Session{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature, session, nil
}

func (m *Manager) Verify(token string, now time.Time) (Session, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Session{}, ErrInvalidSession
	}
	supplied, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(supplied, mac.Sum(nil)) {
		return Session{}, ErrInvalidSession
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	var session Session
	if json.Unmarshal(payload, &session) != nil || session.User != m.username || session.CSRF == "" || now.Unix() >= session.Exp {
		return Session{}, ErrInvalidSession
	}
	return session, nil
}
