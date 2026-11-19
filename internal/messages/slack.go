package messages

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

var slackAccountRE = regexp.MustCompile(`^(T[A-Z0-9]+:U[A-Z0-9]+|profile/[0-9a-f]{64})$`)
var slackChatRE = regexp.MustCompile(`^[CDG][A-Z0-9]+$`)
var slackTimestampRE = regexp.MustCompile(`^[0-9]{10,}\.[0-9]{6}$`)

func SlackConversationID(r Route) string {
	h := sha256.Sum256([]byte(r.Account + "\x00" + r.Chat + "\x00" + r.Thread))
	return "slack/" + hex.EncodeToString(h[:])
}
func validSourceURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && len(raw) < 2048 && !strings.ContainsAny(raw, "\r\n\t <>")
}

// Browser sources deliberately have separate identities from API mail: browser
// snapshots cannot promise native message IDs or complete event histories.
var browserServices = map[string]string{"outlook-web": "Outlook (Chrome)", "outlook-calendar": "Calendar (Chrome)"}

func browserProvider(provider string) bool { return browserServices[provider] != "" }
