// Package messages is the optional messaging proof of concept. Provider IDs and
// reply destinations are stored before anything is rendered as IRC or a TUI.
package messages

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const MaxAttachment = 5 << 20

// MaxAttachmentStore bounds all stored files, sent and received.
const MaxAttachmentStore = 2 << 30

type Conversation struct {
	ID        string `json:"id"`
	Service   string `json:"service"`
	Account   string `json:"account"`
	Name      string `json:"name"`
	Thread    string `json:"thread,omitempty"`
	Recipient string `json:"recipient"`
	Channel   string `json:"channel"`
	Demo      bool   `json:"demo"`
	Route     Route  `json:"route,omitzero"`
}

// Route contains provider identifiers, never display names or usernames.
type Route struct {
	Provider string `json:"provider"`
	Account  string `json:"account"`
	Chat     string `json:"chat"`
	Thread   string `json:"thread,omitempty"`
}
type Attachment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int    `json:"size"`
}
type Message struct {
	SourceURL    string       `json:"source_url,omitempty"`
	ID           string       `json:"id"`
	NativeID     string       `json:"native_id"`
	Conversation string       `json:"conversation"`
	Sender       string       `json:"sender"`
	SenderID     string       `json:"sender_id,omitempty"`
	Body         string       `json:"body"`
	At           time.Time    `json:"at"`
	Outgoing     bool         `json:"outgoing"`
	ReplyTo      string       `json:"reply_to,omitempty"`
	Category     string       `json:"category"`
	Reason       string       `json:"reason"`
	Handled      bool         `json:"handled"`
	SnoozedUntil time.Time    `json:"snoozed_until,omitempty"`
	Attachments  []Attachment `json:"attachments,omitempty"`
}
type Item struct {
	Conversation Conversation `json:"conversation"`
	Message      Message      `json:"message"`
	LastActivity time.Time    `json:"last_activity"`
}
type Thread struct {
	Conversation Conversation `json:"conversation"`
	Messages     []Message    `json:"messages"`
	// Set only on a page of a longer conversation (see Store.ThreadPage).
	HasBefore bool `json:"has_before,omitempty"`
	HasAfter  bool `json:"has_after,omitempty"`
	// Slack only, for the owner: threads under this conversation's messages
	// (keyed by message ID), or the message this thread hangs from.
	Replies map[string]ReplySummary `json:"replies,omitempty"`
	Parent  *ThreadParent           `json:"parent,omitempty"`
	// For the owner: the walls (projects) each shown message is tagged to.
	Tags map[string][]string `json:"tags,omitempty"`
}
type Draft struct {
	ID            string       `json:"id"`
	Actor         string       `json:"actor"`
	Target        Conversation `json:"target"`
	ReplyTo       string       `json:"reply_to"`
	Body          string       `json:"body"`
	Attachments   []Attachment `json:"attachments,omitempty"`
	State         string       `json:"state"`
	Created       time.Time    `json:"created"`
	SenderActor   string       `json:"sender_actor,omitempty"`
	Attempt       string       `json:"attempt,omitempty"`
	AttemptAt     time.Time    `json:"attempt_at,omitempty"`
	NativeID      string       `json:"native_id,omitempty"`
	DeliveryError string       `json:"delivery_error,omitempty"`
}
type DraftInput struct {
	RequestID   string   `json:"request_id"`
	ReplyTo     string   `json:"reply_to"`
	Body        string   `json:"body"`
	Attachments []string `json:"attachments,omitempty"`
	// Thread replies in a Slack thread under ReplyTo, starting it if needed,
	// instead of posting to the channel or DM.
	Thread bool `json:"thread,omitempty"`
}
type Grant struct {
	Conversation string    `json:"conversation"`
	Expires      time.Time `json:"expires"`
	Send         bool      `json:"send"`
	Attachments  bool      `json:"attachments"`
}
type Ingress struct {
	Conversation Conversation `json:"conversation"`
	Message      Message      `json:"message"`
	// Correct replaces the text of a message this connector already
	// imported, when it was decoded wrongly. Nothing else changes.
	Correct bool `json:"correct,omitempty"`
}
type Fault struct {
	Status int
	Text   string
}

func (f *Fault) Error() string            { return f.Text }
func fault(status int, text string) error { return &Fault{status, text} }
func ID() string                          { return uuid.NewString() }
func validID(s string) bool {
	if len(s) == 0 || len(s) > 200 {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.:@/", r)) {
			return false
		}
	}
	return true
}
func classify(body string) (string, string) {
	b := strings.ToLower(body)
	for _, s := range []string{"can you", "could you", "please", "need your", "?"} {
		if strings.Contains(b, s) {
			return "Reply", "A question or request is waiting on you"
		}
	}
	for _, s := range []string{"deadline", "changed", "announcement", "release", "due "} {
		if strings.Contains(b, s) {
			return "Don't miss", "A deadline, change, or announcement"
		}
	}
	return "Review", "Unclassified messages stay visible"
}

// Plain removes terminal control sequences, including OSC and CSI payloads.
// Message bytes remain unchanged in storage; only display text is sanitized.
func Plain(s string) string {
	var b strings.Builder
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		c := r[i]
		if c == 27 {
			if i+1 < len(r) {
				i++
				switch r[i] {
				case '[':
					for i+1 < len(r) {
						i++
						if r[i] >= 0x40 && r[i] <= 0x7e {
							break
						}
					}
				case ']', 'P', '_', '^', 'X':
					for i+1 < len(r) {
						i++
						if r[i] == 7 {
							break
						}
						if r[i] == 27 && i+1 < len(r) && r[i+1] == '\\' {
							i++
							break
						}
					}
				}
			}
			continue
		}
		if c == '\uFFFD' || c == '\uFFFC' {
			continue
		}
		if c == '\n' || c == '\t' || (!unicode.IsControl(c) && c != 0x7f) {
			b.WriteRune(c)
		}
	}
	return b.String()
}
func DisplayService(c Conversation) string {
	if c.Route.Provider == "imessage" {
		return "Messages"
	}
	return Plain(c.Service)
}
func AccountDisplay(c Conversation) string {
	switch c.Route.Provider {
	case "telegram", "signal", "imessage", "slack", "outlook-web", "outlook-calendar":
		return DisplayService(c)
	}
	return Plain(c.Service + " / " + c.Account)
}
func Destination(c Conversation) string {
	if c.Service == "Slack" {
		s := "Slack → " + Plain(c.Name)
		if c.Thread != "" {
			s += " · thread"
		}
		return s
	}
	if mailProvider(c.Route.Provider) {
		return Plain(c.Service + " / " + c.Account + " → " + c.Name)
	}
	if c.Route.Provider == "telegram" || c.Route.Provider == "signal" || c.Route.Provider == "imessage" {
		name := c.Name
		if name == "" {
			name = c.Recipient
		}
		s := DisplayService(c) + " → " + Plain(name)
		if c.Thread != "" {
			s += " · topic " + Plain(c.Thread)
		}
		return s
	}
	s := fmt.Sprintf("%s / %s → %s", c.Service, c.Account, c.Recipient)
	if c.Thread != "" {
		s += " · " + c.Thread
	}
	return Plain(s)
}
