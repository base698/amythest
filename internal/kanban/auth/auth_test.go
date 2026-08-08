package auth

import (
	"testing"
	"time"
)

func TestManagerAuthenticatesAndRejectsTamperedOrExpiredSessions(t *testing.T) {
	m, err := NewManager("operator", "correct horse", []byte("01234567890123456789012345678901"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Authenticate("operator", "correct horse") {
		t.Fatal("valid credentials rejected")
	}
	if m.Authenticate("operator", "wrong") {
		t.Fatal("invalid password accepted")
	}

	now := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	token, session, err := m.NewSession(now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Verify(token, now.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.User != "operator" || got.CSRF != session.CSRF {
		t.Fatalf("session = %#v", got)
	}

	if _, err := m.Verify(token+"x", now); err == nil {
		t.Fatal("tampered token accepted")
	}
	if _, err := m.Verify(token, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestManagerRequiresStrongConfiguration(t *testing.T) {
	if _, err := NewManager("", "password", []byte("01234567890123456789012345678901"), time.Hour); err == nil {
		t.Fatal("empty username accepted")
	}
	if _, err := NewManager("operator", "short", []byte("01234567890123456789012345678901"), time.Hour); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := NewManager("operator", "long enough password", []byte("short"), time.Hour); err == nil {
		t.Fatal("short secret accepted")
	}
}
