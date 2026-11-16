package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultSessionTTL = 90 * 24 * time.Hour

type Credential struct {
	ClientID string `json:"client_id"`
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Created  int64  `json:"created"`
	Expires  int64  `json:"expires"`
	LastUsed int64  `json:"last_used"`
}
type Registration struct {
	DeviceCode string `json:"device_code,omitempty"`
	UserCode   string `json:"user_code"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	State      string `json:"state"`
	Expires    int64  `json:"expires"`
	Interval   int    `json:"interval"`
}

func randomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }

// OpenManager opens authoritative authentication data, never a rebuildable index.
func OpenManager(path, username, password string, secret []byte, ttl time.Duration) (*Manager, error) {
	m, err := NewManager(username, password, secret, ttl)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(e error) (*Manager, error) { db.Close(); return nil, e }
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;`); err != nil {
		return fail(err)
	}
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fail(err)
	}
	if version > 2 {
		return fail(errors.New("auth database is newer than this binary"))
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS credentials(id TEXT PRIMARY KEY,secret_hash TEXT UNIQUE NOT NULL,label TEXT NOT NULL,kind TEXT NOT NULL,created INTEGER NOT NULL,expires INTEGER NOT NULL,last_used INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS registrations(device_hash TEXT PRIMARY KEY,user_code TEXT UNIQUE NOT NULL,label TEXT NOT NULL,kind TEXT NOT NULL,state TEXT NOT NULL,expires INTEGER NOT NULL,last_poll INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS audit(id INTEGER PRIMARY KEY,action TEXT NOT NULL,target TEXT NOT NULL,at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS credential_clients(token_id TEXT PRIMARY KEY,client_id TEXT NOT NULL);
 PRAGMA user_version=2;`); err != nil {
		return fail(err)
	}
	fingerprint := digest(username + "\x00" + password + "\x00" + string(secret))
	tx, err := db.Begin()
	if err != nil {
		return fail(err)
	}
	var old string
	err = tx.QueryRow(`SELECT value FROM metadata WHERE key='bootstrap'`).Scan(&old)
	if err != nil && err != sql.ErrNoRows {
		tx.Rollback()
		return fail(err)
	}
	err = nil
	if old != fingerprint {
		if _, err = tx.Exec(`DELETE FROM credentials WHERE kind='session'`); err == nil {
			_, err = tx.Exec(`INSERT OR REPLACE INTO metadata(key,value) VALUES('bootstrap',?)`, fingerprint)
		}
	}
	if err != nil {
		tx.Rollback()
		return fail(err)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		return fail(err)
	}
	m.db = db
	return m, nil
}
func (m *Manager) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}
func (m *Manager) TTL() time.Duration { return m.ttl }
func (m *Manager) csrf(token string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte("csrf:" + token))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (m *Manager) newPersistentSession(now time.Time) (string, Session, error) {
	token, c, err := m.CreateCredential("Browser session", "session", now, m.ttl)
	return token, Session{User: m.username, Exp: c.Expires, CSRF: m.csrf(token)}, err
}
func (m *Manager) verifyPersistent(token string, now time.Time) (Session, error) {
	c, err := m.Credential(token, now)
	if err != nil || c.Kind != "session" {
		return Session{}, ErrInvalidSession
	}
	return Session{User: m.username, Exp: c.Expires, CSRF: m.csrf(token)}, nil
}

// Renew extends idle expiry at most daily without changing the secret or CSRF token.
func (m *Manager) Renew(token string, now time.Time) (bool, error) {
	if m.db == nil {
		return false, nil
	}
	r, err := m.db.Exec(`UPDATE credentials SET expires=? WHERE secret_hash=? AND kind='session' AND expires>? AND expires<?`, now.Add(m.ttl).Unix(), digest(token), now.Unix(), now.Add(m.ttl-24*time.Hour).Unix())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}
func (m *Manager) RevokeSession(token string) error {
	if m.db == nil {
		return nil
	}
	_, err := m.db.Exec(`DELETE FROM credentials WHERE secret_hash=? AND kind='session'`, digest(token))
	return err
}
func (m *Manager) CreateCredential(label, kind string, now time.Time, ttl time.Duration) (string, Credential, error) {
	if m.db == nil {
		return "", Credential{}, errors.New("durable authentication unavailable")
	}
	if kind != "session" && kind != "reader" && kind != "collector" {
		return "", Credential{}, errors.New("unsupported credential preset")
	}
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 100 || ttl <= 0 || ttl > 365*24*time.Hour {
		return "", Credential{}, errors.New("invalid label or lifetime")
	}
	token := randomSecret()
	c := Credential{ID: randomSecret(), Label: label, Kind: kind, Created: now.Unix(), Expires: now.Add(ttl).Unix()}
	c.ClientID = c.ID
	tx, err := m.db.Begin()
	if err != nil {
		return "", c, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO credentials(id,secret_hash,label,kind,created,expires) VALUES(?,?,?,?,?,?)`, c.ID, digest(token), label, kind, c.Created, c.Expires)
	if err == nil {
		_, err = tx.Exec(`INSERT INTO audit(action,target,at) VALUES('create',?,?)`, c.ID, now.Unix())
	}
	if err == nil {
		err = tx.Commit()
	}
	return token, c, err
}
func (m *Manager) Credential(token string, now time.Time) (Credential, error) {
	var c Credential
	if m.db == nil {
		return c, ErrInvalidSession
	}
	err := m.db.QueryRow(`SELECT c.id,c.label,c.kind,c.created,c.expires,c.last_used,COALESCE(cc.client_id,c.id) FROM credentials c LEFT JOIN credential_clients cc ON cc.token_id=c.id WHERE secret_hash=? AND expires>?`, digest(token), now.Unix()).Scan(&c.ID, &c.Label, &c.Kind, &c.Created, &c.Expires, &c.LastUsed, &c.ClientID)
	if err != nil {
		return c, ErrInvalidSession
	}
	if now.Unix()-c.LastUsed > 3600 {
		_, err = m.db.Exec(`UPDATE credentials SET last_used=? WHERE id=?`, now.Unix(), c.ID)
	}
	return c, err
}
func (m *Manager) Credentials() ([]Credential, error) {
	rows, err := m.db.Query(`SELECT c.id,c.label,c.kind,c.created,c.expires,c.last_used,COALESCE(cc.client_id,c.id) FROM credentials c LEFT JOIN credential_clients cc ON cc.token_id=c.id ORDER BY created DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Credential{}
	for rows.Next() {
		var c Credential
		if err = rows.Scan(&c.ID, &c.Label, &c.Kind, &c.Created, &c.Expires, &c.LastUsed, &c.ClientID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (m *Manager) Revoke(id string, now time.Time) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM credentials WHERE id=?`, id)
	if err == nil {
		_, err = tx.Exec(`INSERT INTO audit(action,target,at) VALUES('revoke',?,?)`, id, now.Unix())
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}
func (m *Manager) Register(label, kind string, now time.Time) (Registration, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return Registration{}, err
	}
	r := Registration{DeviceCode: randomSecret(), UserCode: strings.ToUpper(hex.EncodeToString(b)), Label: strings.TrimSpace(label), Kind: kind, State: "pending", Expires: now.Add(10 * time.Minute).Unix(), Interval: 5}
	if r.Label == "" || len(r.Label) > 100 || (kind != "reader" && kind != "collector") {
		return r, errors.New("choose reader or collector and a name")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM registrations WHERE expires<?`, now.Unix())
	if err != nil {
		return r, err
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM registrations`).Scan(&count); err != nil {
		return r, err
	}
	if count >= 100 {
		return r, errors.New("too many pending registrations")
	}
	_, err = tx.Exec(`INSERT INTO registrations(device_hash,user_code,label,kind,state,expires) VALUES(?,?,?,?,?,?)`, digest(r.DeviceCode), r.UserCode, r.Label, r.Kind, r.State, r.Expires)
	if err == nil {
		err = tx.Commit()
	}
	return r, err
}
func (m *Manager) Registration(code string, now time.Time) (Registration, error) {
	var r Registration
	err := m.db.QueryRow(`SELECT user_code,label,kind,state,expires FROM registrations WHERE user_code=? AND expires>?`, strings.ToUpper(code), now.Unix()).Scan(&r.UserCode, &r.Label, &r.Kind, &r.State, &r.Expires)
	return r, err
}
func (m *Manager) Decide(code string, approve bool, now time.Time) error {
	state := "denied"
	if approve {
		state = "approved"
	}
	r, err := m.db.Exec(`UPDATE registrations SET state=? WHERE user_code=? AND state='pending' AND expires>?`, state, strings.ToUpper(code), now.Unix())
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return errors.New("request is expired or already decided")
	}
	return nil
}
func (m *Manager) Exchange(code string, now time.Time) (string, Credential, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return "", Credential{}, err
	}
	defer tx.Rollback()
	var state, label, kind string
	var expires, last int64
	err = tx.QueryRow(`SELECT state,label,kind,expires,last_poll FROM registrations WHERE device_hash=?`, digest(code)).Scan(&state, &label, &kind, &expires, &last)
	if err != nil || expires <= now.Unix() {
		return "", Credential{}, errors.New("expired")
	}
	if now.Unix()-last < 5 {
		return "", Credential{}, errors.New("slow_down")
	}
	if state != "approved" {
		_, err = tx.Exec(`UPDATE registrations SET last_poll=? WHERE device_hash=?`, now.Unix(), digest(code))
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return "", Credential{}, err
		}
		return "", Credential{}, errors.New(state)
	}
	token := randomSecret()
	c := Credential{ID: randomSecret(), Label: label, Kind: kind, Created: now.Unix(), Expires: now.Add(90 * 24 * time.Hour).Unix()}
	c.ClientID = c.ID
	_, err = tx.Exec(`INSERT INTO credentials(id,secret_hash,label,kind,created,expires) VALUES(?,?,?,?,?,?)`, c.ID, digest(token), label, kind, c.Created, c.Expires)
	if err == nil {
		_, err = tx.Exec(`UPDATE registrations SET state='consumed',last_poll=? WHERE device_hash=?`, now.Unix(), digest(code))
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO audit(action,target,at) VALUES('register',?,?)`, c.ID, now.Unix())
	}
	if err == nil {
		err = tx.Commit()
	}
	return token, c, err
}

// ReplaceCredential overlaps secrets during a client update while retaining the
// immutable client ID. Revoke the previous credential after the client switches.
func (m *Manager) ReplaceCredential(previous string, now time.Time) (string, Credential, error) {
	var c Credential
	tx, e := m.db.Begin()
	if e != nil {
		return "", c, e
	}
	defer tx.Rollback()
	e = tx.QueryRow(`SELECT c.label,c.kind,COALESCE(cc.client_id,c.id) FROM credentials c LEFT JOIN credential_clients cc ON cc.token_id=c.id WHERE c.id=? AND c.kind IN('reader','collector')`, previous).Scan(&c.Label, &c.Kind, &c.ClientID)
	if e != nil {
		return "", c, errors.New("credential unavailable for replacement")
	}
	c.ID = randomSecret()
	c.Created = now.Unix()
	c.Expires = now.Add(90 * 24 * time.Hour).Unix()
	token := randomSecret()
	_, e = tx.Exec(`INSERT INTO credentials(id,secret_hash,label,kind,created,expires) VALUES(?,?,?,?,?,?)`, c.ID, digest(token), c.Label, c.Kind, c.Created, c.Expires)
	if e == nil {
		_, e = tx.Exec(`INSERT INTO credential_clients VALUES(?,?)`, c.ID, c.ClientID)
	}
	if e == nil {
		_, e = tx.Exec(`INSERT INTO audit(action,target,at) VALUES('replace',?,?)`, c.ID, now.Unix())
	}
	if e == nil {
		e = tx.Commit()
	}
	return token, c, e
}
