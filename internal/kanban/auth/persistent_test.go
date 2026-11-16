package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDurableSessionsRenewRevokeAndCredentialChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	secret := []byte("01234567890123456789012345678901")
	open := func(password string) *Manager {
		m, e := OpenManager(path, "owner", password, secret, DefaultSessionTTL)
		if e != nil {
			t.Fatal(e)
		}
		return m
	}
	now := time.Now()
	m := open("correct horse")
	token, s, e := m.NewSession(now)
	if e != nil {
		t.Fatal(e)
	}
	if s.Exp != now.Add(DefaultSessionTTL).Unix() {
		t.Fatal("not a 90-day session")
	}
	m.Close()
	m = open("correct horse")
	if _, e = m.Verify(token, now.Add(89*24*time.Hour)); e != nil {
		t.Fatal("restart lost session", e)
	}
	if renewed, e := m.Renew(token, now.Add(89*24*time.Hour)); e != nil || !renewed {
		t.Fatal("renew", renewed, e)
	}
	if s2, e := m.Verify(token, now.Add(100*24*time.Hour)); e != nil || s2.CSRF != s.CSRF {
		t.Fatal("renewal lost csrf or session", e)
	}
	if e = m.RevokeSession(token); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Verify(token, now.Add(100*24*time.Hour)); e == nil {
		t.Fatal("revoked session accepted")
	}
	token, _, _ = m.NewSession(now)
	m.Close()
	m = open("different password")
	defer m.Close()
	if _, e = m.Verify(token, now); e == nil {
		t.Fatal("credential change kept session")
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte(token)) {
		t.Fatal("plaintext session persisted")
	}
}
func TestPairingSingleUseExpiryAndRevocation(t *testing.T) {
	m, e := OpenManager(filepath.Join(t.TempDir(), "auth.db"), "owner", "correct horse", []byte("01234567890123456789012345678901"), DefaultSessionTTL)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	now := time.Now()
	r, e := m.Register("research", "reader", now)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = m.Exchange(r.DeviceCode, now); e == nil || e.Error() != "pending" {
		t.Fatal(e)
	}
	if e = m.Decide(r.UserCode, true, now); e != nil {
		t.Fatal(e)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, e := m.Exchange(r.DeviceCode, now.Add(6*time.Second)); e == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("exchange count", successes.Load())
	}
	expired, _ := m.Register("expired", "collector", now)
	if e = m.Decide(expired.UserCode, true, now.Add(11*time.Minute)); e == nil {
		t.Fatal("approved expired registration")
	}
	denied, _ := m.Register("denied", "reader", now)
	m.Decide(denied.UserCode, false, now)
	if _, _, e = m.Exchange(denied.DeviceCode, now); e == nil || e.Error() != "denied" {
		t.Fatal(e)
	}
	token, c, e := m.CreateCredential("reader", "reader", now, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Credential(token, now.Add(2*time.Hour)); e == nil {
		t.Fatal("expired token accepted")
	}
	m.Revoke(c.ID, now)
	if _, e = m.Credential(token, now); e == nil {
		t.Fatal("revoked token accepted")
	}
	if _, _, e = m.CreateCredential("admin", "owner", now, time.Hour); e == nil {
		t.Fatal("owner token accepted")
	}
}

func TestReplacementPreservesClientIdentity(t *testing.T) {
	m, e := OpenManager(filepath.Join(t.TempDir(), "auth.db"), "owner", "long secret password", []byte("01234567890123456789012345678901"), DefaultSessionTTL)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	now := time.Now()
	old, c, e := m.CreateCredential("laptop", "collector", now, DefaultSessionTTL)
	if e != nil {
		t.Fatal(e)
	}
	token, next, e := m.ReplaceCredential(c.ID, now)
	if e != nil || next.ClientID != c.ID || next.ID == c.ID {
		t.Fatal(next, e)
	}
	m.Revoke(c.ID, now)
	if _, e = m.Credential(old, now); e == nil {
		t.Fatal("old token valid")
	}
	got, e := m.Credential(token, now)
	if e != nil || got.ClientID != c.ID {
		t.Fatal(got, e)
	}
}
