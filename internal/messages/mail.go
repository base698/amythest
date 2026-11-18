package messages

import (
	"crypto/sha256"
	"encoding/hex"
	"net/mail"
	"strings"
)

func mailProvider(provider string) bool { return provider == "gmail" || provider == "outlook" }

func mailAccount(account string) bool {
	a, err := mail.ParseAddress(account)
	return err == nil && a.Address == account && a.Name == "" && len(account) <= 200 && strings.ToLower(account) == account
}

// Gmail exposes hexadecimal message/thread IDs. Outlook's opaque IDs are
// hashed by its private connector, which keeps the exact immutable ID mapping.
func mailID(provider, id string) bool {
	if len(id) == 0 || len(id) > 64 || (provider == "outlook" && len(id) != 64) {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func MailConversationID(r Route) string {
	h := sha256.Sum256([]byte(r.Account + "\x00" + r.Chat))
	return r.Provider + "/" + hex.EncodeToString(h[:])
}
