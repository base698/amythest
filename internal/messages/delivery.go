package messages

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

type Connector struct {
	Transport string    `json:"transport,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Account   string    `json:"account"`
	Worker    string    `json:"worker"`
	Chats     []string  `json:"chats"`
	AllChats  bool      `json:"all_chats"`
	Send      bool      `json:"send"`
	Seen      time.Time `json:"seen"`
}
type Delivery struct {
	Draft  Draft   `json:"draft"`
	Parent Message `json:"parent"`
}
type Receipt struct {
	Provider string `json:"provider,omitempty"`
	Account  string `json:"account"`
	Draft    string `json:"draft"`
	Attempt  string `json:"attempt"`
	State    string `json:"state"`
	NativeID string `json:"native_id"`
	Error    string `json:"error"`
}

func decimal(s string, positive bool) bool {
	n, e := strconv.ParseInt(s, 10, 64)
	return e == nil && n != 0 && (!positive || n > 0) && strconv.FormatInt(n, 10) == s
}
func (c Connector) allows(chat string) bool {
	if c.AllChats {
		return true
	}
	for _, id := range c.Chats {
		if id == chat {
			return true
		}
	}
	return false
}
func providerName(values []string) string {
	if len(values) > 0 && values[0] != "" {
		return values[0]
	}
	return "telegram"
}
func connectorKey(provider, account string) string {
	if provider == "telegram" || provider == "" {
		return account
	}
	return provider + "/" + account
}
func canonicalUUID(id string) bool { v, e := uuid.Parse(id); return e == nil && v.String() == id }
func signalChat(id string) bool {
	if strings.HasPrefix(id, "user/") {
		return canonicalUUID(strings.TrimPrefix(id, "user/"))
	}
	if strings.HasPrefix(id, "group/") {
		v, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(id, "group/"))
		return e == nil && len(v) == 32
	}
	return false
}
func SignalConversationID(r Route) string {
	h := sha256.Sum256([]byte(r.Account + "\x00" + r.Chat))
	return "signal/" + hex.EncodeToString(h[:])
}
func imessageChat(id string) bool {
	parts := strings.SplitN(id, ";", 3)
	return len(parts) == 3 && parts[1] != "" && parts[2] != "" && len(id) <= 300 && (parts[0] == "iMessage" || parts[0] == "any" || parts[0] == "SMS" || parts[0] == "RCS") && !strings.ContainsAny(id, "\r\n\x00")
}
func IMessageConversationID(r Route) string {
	h := sha256.Sum256([]byte(r.Account + "\x00" + r.Chat))
	return "imessage/" + hex.EncodeToString(h[:])
}
func signalParent(id string) bool {
	parts := strings.Split(id, "/")
	return len(parts) == 2 && decimal(parts[0], true) && canonicalUUID(parts[1])
}
func (s *Store) Heartbeat(c Connector) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.Provider = providerName([]string{c.Provider})
	if (browserProvider(c.Provider) || c.Transport == "chrome" || c.Transport == "agent" && c.Provider != "slack") && c.Send {
		return fault(400, "Agent imports are read-only")
	}
	validAccount := c.Provider == "telegram" && decimal(c.Account, true) || (c.Provider == "signal" || c.Provider == "imessage") && canonicalUUID(c.Account)
	validAccount = validAccount || (mailProvider(c.Provider) || browserProvider(c.Provider)) && mailAccount(c.Account) || c.Provider == "slack" && slackAccountRE.MatchString(c.Account)
	if !validAccount || !validID(c.Worker) || len(c.Chats) > 2000 {
		return fault(400, "invalid connector registration")
	}
	for _, id := range c.Chats {
		if browserProvider(c.Provider) && !mailID("outlook", id) || c.Provider == "slack" && !slackChatRE.MatchString(id) || c.Provider == "telegram" && !decimal(id, false) || c.Provider == "signal" && !signalChat(id) || c.Provider == "imessage" && !imessageChat(id) || mailProvider(c.Provider) && !mailID(c.Provider, id) {
			return fault(400, "invalid native chat ID")
		}
	}
	c.Seen = s.now().UTC()
	_, e := s.db.Exec("INSERT INTO connectors VALUES(?,?) ON CONFLICT(account) DO UPDATE SET payload=excluded.payload", connectorKey(c.Provider, c.Account), encode(c))
	return e
}
func (s *Store) Connectors() ([]Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query("SELECT payload FROM connectors")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Connector{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var c Connector
		if e = json.Unmarshal([]byte(raw), &c); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func validateNativeConversation(c Conversation) error {
	r := c.Route
	valid := r.Provider == "telegram" && c.Service == "Telegram" && decimal(r.Account, true) && decimal(r.Chat, false) && (r.Thread == "" || decimal(r.Thread, true)) && c.ID == TelegramConversationID(r)
	if r.Provider == "signal" {
		valid = c.Service == "Signal" && canonicalUUID(r.Account) && signalChat(r.Chat) && r.Thread == "" && c.ID == SignalConversationID(r)
	}
	if r.Provider == "imessage" {
		valid = (c.Service == "iMessage" || c.Service == "Messages") && canonicalUUID(r.Account) && imessageChat(r.Chat) && r.Thread == "" && c.ID == IMessageConversationID(r)
	}
	if r.Provider == "slack" {
		valid = c.Service == "Slack" && slackAccountRE.MatchString(r.Account) && slackChatRE.MatchString(r.Chat) && (r.Thread == "" || slackTimestampRE.MatchString(r.Thread)) && c.ID == SlackConversationID(r)
	}
	if browserProvider(r.Provider) {
		valid = c.Service == browserServices[r.Provider] && mailAccount(r.Account) && mailID("outlook", r.Chat) && r.Thread == "" && c.ID == MailConversationID(r)
	}
	if mailProvider(r.Provider) {
		service := map[string]string{"gmail": "Gmail", "outlook": "Outlook"}[r.Provider]
		valid = c.Service == service && mailAccount(r.Account) && mailID(r.Provider, r.Chat) && r.Thread == "" && c.ID == MailConversationID(r)
	}
	if !valid || c.Demo || c.Account != r.Account || c.Recipient != r.Chat || c.Thread != r.Thread {
		return fault(400, "invalid native source identifiers")
	}
	return nil
}
func (s *Store) ValidateIngress(in Ingress) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := validateNativeConversation(in.Conversation); e != nil {
		return e
	}
	r := in.Conversation.Route
	valid := r.Provider == "telegram" && decimal(in.Message.NativeID, true) || r.Provider == "signal" && signalParent(in.Message.NativeID) || r.Provider == "imessage" && validID(in.Message.NativeID)
	valid = valid || r.Provider == "slack" && slackTimestampRE.MatchString(in.Message.NativeID)
	valid = valid || browserProvider(r.Provider) && mailID("outlook", in.Message.NativeID)
	valid = valid || mailProvider(r.Provider) && mailID(r.Provider, in.Message.NativeID)
	if !valid {
		return fault(400, "invalid native message ID")
	}
	connector, e := getJSON[Connector](s.db, "SELECT payload FROM connectors WHERE account=?", connectorKey(r.Provider, r.Account))
	if e != nil || !connector.allows(r.Chat) {
		return fault(403, "chat is outside the connector's configured scope")
	}
	return nil
}
func TelegramConversationID(r Route) string {
	return "telegram/" + r.Account + "/" + r.Chat + "/" + r.Thread
}
func validateLive(d Draft) error {
	if d.Target.Route.Provider == "slack" {
		if e := validateNativeConversation(d.Target); e != nil {
			return e
		}
		if len(d.Attachments) > 1 {
			return fault(400, "Slack sends one file per message")
		}
		if len([]rune(d.Body)) > 5000 {
			return fault(400, "Slack text sends are limited to 5000 characters")
		}
		return nil
	}
	if mailProvider(d.Target.Route.Provider) {
		if e := validateNativeConversation(d.Target); e != nil {
			return e
		}
		if d.Target.Route.Provider == "outlook" {
			total := 0
			for _, a := range d.Attachments {
				total += a.Size
			}
			// MIME expands attachment bytes by about a third. Leave room for
			// headers and body within Graph's simple request size limit.
			if total > 2<<20 {
				return fault(400, "Outlook replies support up to 2 MiB of attachments")
			}
		}
		return nil
	}
	if d.Target.Route.Provider == "imessage" {
		r := d.Target.Route
		if !canonicalUUID(r.Account) || !imessageChat(r.Chat) || r.Thread != "" {
			return fault(409, "invalid iMessage destination")
		}
		if len(d.Attachments) > 1 {
			return fault(400, "iMessage currently supports one attachment per reply")
		}
		return nil
	}

	if d.Target.Route.Provider == "signal" {
		r := d.Target.Route
		if !canonicalUUID(r.Account) || !signalChat(r.Chat) || r.Thread != "" {
			return fault(409, "invalid Signal destination")
		}
		if len(d.Body) > 20000 {
			return fault(400, "Signal reply is limited to 20000 bytes")
		}
		return nil
	}
	r := d.Target.Route
	if r.Provider != "telegram" || !decimal(r.Account, true) || !decimal(r.Chat, false) || (r.Thread != "" && !decimal(r.Thread, true)) {
		return fault(409, "no live delivery connector is configured; reply remains a draft")
	}
	if len(d.Attachments) > 1 {
		return fault(400, "Telegram currently supports one attachment per reply")
	}
	limit := 4096
	if len(d.Attachments) > 0 {
		limit = 1024
	}
	if len(utf16.Encode([]rune(d.Body))) > limit {
		return fault(400, fmt.Sprintf("Telegram reply is limited to %d UTF-16 units with this content", limit))
	}
	return nil
}
func (s *Store) queue(tx *sql.Tx, actor string, d Draft) (Draft, error) {
	if e := validateLive(d); e != nil {
		return d, e
	}
	c, e := getJSON[Connector](tx, "SELECT payload FROM connectors WHERE account=?", connectorKey(d.Target.Route.Provider, d.Target.Route.Account))
	if e != nil || !c.Send || !c.allows(d.Target.Route.Chat) {
		return d, fault(409, "connector is not configured to send to this conversation; reply remains a draft")
	}
	d.State = "queued"
	d.SenderActor = actor
	d.Attempt = ""
	d.DeliveryError = ""
	d.NativeID = ""
	if _, e = tx.Exec("UPDATE drafts SET payload=? WHERE id=?", encode(d), d.ID); e != nil {
		return d, e
	}
	return d, tx.Commit()
}

// Claim persists the attempt before returning it. Stale attempts are never
// automatically retried: a provider may have accepted them before a disconnect.
func (s *Store) Claim(account, worker string, providers ...string) (*Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, e := getJSON[Connector](s.db, "SELECT payload FROM connectors WHERE account=?", connectorKey(providerName(providers), account))
	if e != nil || c.Worker != worker || s.now().Sub(c.Seen) > 30*time.Second {
		return nil, fault(403, "connector registration required")
	}
	rows, e := s.db.Query("SELECT payload FROM drafts ORDER BY rowid")
	if e != nil {
		return nil, e
	}
	var drafts []Draft
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, e
		}
		var d Draft
		if e = json.Unmarshal([]byte(raw), &d); e != nil {
			rows.Close()
			return nil, e
		}
		drafts = append(drafts, d)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, d := range drafts {
		if d.Target.Demo || d.Target.Route.Provider != providerName(providers) || d.Target.Route.Account != account {
			continue
		}
		if d.State == "sending" && s.now().Sub(d.AttemptAt) > 2*time.Minute {
			d.State = "uncertain"
			d.DeliveryError = "Delivery was interrupted. Check the original conversation before composing another reply."
			if _, e = s.db.Exec("UPDATE drafts SET payload=? WHERE id=?", encode(d), d.ID); e != nil {
				return nil, e
			}
			continue
		}
		if d.State != "queued" {
			continue
		}
		err := permitted(s.db, d.SenderActor, d.Target.ID, true, len(d.Attachments) > 0, s.now())
		if err == nil && (!c.Send || !c.allows(d.Target.Route.Chat)) {
			err = fault(403, "connector sending is disabled for this chat")
		}
		if err != nil {
			d.State = "blocked"
			d.DeliveryError = err.Error()
			if _, e = s.db.Exec("UPDATE drafts SET payload=? WHERE id=?", encode(d), d.ID); e != nil {
				return nil, e
			}
			continue
		}
		p, e := getJSON[Message](s.db, "SELECT payload FROM messages WHERE id=?", d.ReplyTo)
		if e != nil {
			return nil, e
		}
		if d.Target.Route.Provider == "telegram" && !decimal(p.NativeID, true) || d.Target.Route.Provider == "signal" && !signalParent(p.NativeID) {
			return nil, fault(409, "reply parent has no native message ID")
		}
		d.State = "sending"
		d.Attempt = ID()
		d.AttemptAt = s.now().UTC()
		if _, e = s.db.Exec("UPDATE drafts SET payload=? WHERE id=?", encode(d), d.ID); e != nil {
			return nil, e
		}
		return &Delivery{d, p}, nil
	}
	return nil, nil
}
func (s *Store) Complete(r Receipt) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return Draft{}, e
	}
	defer tx.Rollback()
	d, e := getJSON[Draft](tx, "SELECT payload FROM drafts WHERE id=?", r.Draft)
	if e != nil {
		return d, e
	}
	if d.Target.Demo || d.Target.Route.Provider != providerName([]string{r.Provider}) || d.Target.Route.Account != r.Account || d.Attempt == "" || d.Attempt != r.Attempt {
		return d, fault(403, "delivery attempt does not match")
	}
	if r.State != "sent" && r.State != "failed" && r.State != "uncertain" && r.State != "submitted" {
		return d, fault(400, "invalid delivery result")
	}
	// A Slack file share returns the file, not the message timestamp.
	slackFile := d.Target.Route.Provider == "slack" && len(d.Attachments) > 0
	if r.State == "submitted" && ((d.Target.Route.Provider != "imessage" && d.Target.Route.Provider != "outlook" && !slackFile) || r.NativeID != "") {
		return d, fault(400, "submitted requires an unconfirmed provider handoff")
	}
	if r.State == "sent" && (d.Target.Route.Provider == "imessage" || d.Target.Route.Provider == "outlook") {
		return d, fault(400, "provider handoff cannot confirm delivery")
	}
	validReceipt := decimal(r.NativeID, true)
	if d.Target.Route.Provider == "gmail" {
		validReceipt = mailID("gmail", r.NativeID)
	}
	if d.Target.Route.Provider == "slack" {
		validReceipt = slackTimestampRE.MatchString(r.NativeID)
	}
	if r.State == "sent" && !validReceipt {
		return d, fault(400, "delivery requires a native message ID")
	}
	if d.State == "sent" || d.State == "failed" || d.State == "submitted" {
		if d.State != r.State || d.NativeID != r.NativeID {
			return d, fault(409, "delivery already has a different receipt")
		}
		return d, nil
	}
	if d.State != "sending" && d.State != "uncertain" {
		return d, fault(409, "delivery is not in progress")
	}
	d.State = r.State
	d.NativeID = r.NativeID
	d.DeliveryError = Plain(r.Error)
	if len(d.DeliveryError) > 500 {
		d.DeliveryError = d.DeliveryError[:500]
	}
	if d.State == "sent" {
		d.DeliveryError = ""
		m := Message{ID: "reply-" + d.ID, NativeID: r.NativeID, Conversation: d.Target.ID, Sender: "You", Body: d.Body, At: s.now().UTC(), Outgoing: true, ReplyTo: d.ReplyTo, Attachments: d.Attachments}
		if d.Target.Route.Provider == "signal" {
			m.NativeID += "/" + r.Account
		}
		// A provider's outgoing sync may beat the send receipt. Merge the
		// receipt into that observation without duplicating or re-dating it.
		if observed, readErr := getJSON[Message](tx, "SELECT payload FROM messages WHERE conversation=? AND native_id=?", m.Conversation, m.NativeID); readErr == nil {
			if !observed.Outgoing {
				return d, fault(409, "send receipt conflicts with an incoming message")
			}
			m.ID, m.At = observed.ID, observed.At
		}
		if _, e = tx.Exec("INSERT INTO messages VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", m.ID, m.Conversation, m.NativeID, occurredKey(m.At), encode(m)); e != nil {
			return d, e
		}
		p, e := getJSON[Message](tx, "SELECT payload FROM messages WHERE id=?", d.ReplyTo)
		if e != nil {
			return d, e
		}
		p.Handled = true
		if _, e = tx.Exec("UPDATE messages SET payload=? WHERE id=?", encode(p), p.ID); e != nil {
			return d, e
		}
	}
	if _, e = tx.Exec("UPDATE drafts SET payload=? WHERE id=?", encode(d), d.ID); e != nil {
		return d, e
	}
	return d, tx.Commit()
}
func (s *Store) DeliveryAttachment(account, draft, attempt, id string, providers ...string) (Attachment, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, e := getJSON[Draft](s.db, "SELECT payload FROM drafts WHERE id=?", draft)
	if e != nil {
		return Attachment{}, nil, e
	}
	if d.Target.Route.Provider != providerName(providers) || d.Target.Route.Account != account || d.Attempt == "" || d.Attempt != attempt || (d.State != "sending" && d.State != "uncertain") {
		return Attachment{}, nil, fault(403, "attachment requires an active delivery attempt")
	}
	for _, a := range d.Attachments {
		if a.ID == id {
			var b []byte
			e = s.db.QueryRow("SELECT data FROM attachments WHERE id=?", id).Scan(&b)
			return a, b, e
		}
	}
	return Attachment{}, nil, fault(403, "attachment is not part of this reply")
}
func DeliveryStatus(d Draft) string {
	switch d.State {
	case "simulated":
		return "Reply recorded as simulated. No external message sent."
	case "queued":
		return "Reply queued for " + d.Target.Service + "; waits for its worker if offline. Status is in D drafts."
	case "sending":
		return "Sending through " + d.Target.Service + ". Delivery status is in D drafts."
	case "submitted":
		if d.Target.Route.Provider == "outlook" {
			return "Outlook accepted the reply. Delivery is unconfirmed; check Sent Items."
		}
		return "Handed to Messages on your Mac. Delivery is unconfirmed; check Messages."
	case "sent":
		return "Reply sent through " + d.Target.Service + "."
	default:
		return strings.TrimSpace(d.State + ": " + d.DeliveryError)
	}
}

// Recheck grants after files have been staged, immediately before the provider
// call. The attempt stays claimed if authorization fails, until its receipt.
func (s *Store) AuthorizeDelivery(account, draft, attempt string, providers ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, e := getJSON[Draft](s.db, "SELECT payload FROM drafts WHERE id=?", draft)
	if e != nil {
		return e
	}
	if d.State != "sending" || d.Target.Route.Provider != providerName(providers) || d.Target.Route.Account != account || d.Attempt == "" || d.Attempt != attempt {
		return fault(403, "delivery attempt is not active")
	}
	c, e := getJSON[Connector](s.db, "SELECT payload FROM connectors WHERE account=?", connectorKey(providerName(providers), account))
	if e != nil || !c.Send || !c.allows(d.Target.Route.Chat) {
		return fault(403, "connector sending is disabled for this chat")
	}
	return permitted(s.db, d.SenderActor, d.Target.ID, true, len(d.Attachments) > 0, s.now())
}
