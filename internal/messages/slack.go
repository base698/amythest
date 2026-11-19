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
var slackSenderRE = regexp.MustCompile(`^[UBW][A-Z0-9]+$`)
var slackPersonRE = regexp.MustCompile(`^[UW][A-Z0-9]+$`)

// SlackPersonURL opens the verified author's workspace. Profile-bound accounts
// have no team ID, so use the source permalink's workspace profile page instead
// of sending someone to whichever workspace happens to be the Slack default.
func SlackPersonURL(c Conversation, m Message) string {
	if c.Service != "Slack" || c.Demo {
		return ""
	}
	id := m.SenderID
	if id == "" {
		id = m.Sender // Support older imports until their profiles are refreshed.
	}
	if !slackPersonRE.MatchString(id) {
		return ""
	}
	if strings.HasPrefix(c.Account, "T") && slackAccountRE.MatchString(c.Account) {
		return "slack://user?" + url.Values{"team": {strings.SplitN(c.Account, ":", 2)[0]}, "id": {id}}.Encode()
	}
	u, err := url.Parse(m.SourceURL)
	if err != nil || !validSourceURL(m.SourceURL) || u.Port() != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	workspace := strings.TrimSuffix(host, ".slack.com")
	if host == workspace || workspace == "" || strings.Contains(workspace, ".") || workspace == "app" || !strings.HasPrefix(u.Path, "/archives/") {
		return ""
	}
	return "https://" + host + "/team/" + id
}

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
