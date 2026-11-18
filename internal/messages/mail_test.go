package messages

import (
	"strings"
	"testing"
)

func TestMailAccountsAndReplyReceipts(t *testing.T) {
	for _, provider := range []string{"gmail", "outlook"} {
		t.Run(provider, func(t *testing.T) {
			s := testStore(t)
			chat, parent := "abc1", "def2"
			if provider == "outlook" {
				chat, parent = strings.Repeat("a", 64), strings.Repeat("b", 64)
			}
			r := Route{Provider: provider, Account: "inbox@example.test", Chat: chat}
			service := map[string]string{"gmail": "Gmail", "outlook": "Outlook"}[provider]
			c := Connector{Provider: provider, Account: r.Account, Worker: "mail-worker", AllChats: true, Send: true}
			if err := s.Heartbeat(c); err != nil {
				t.Fatal(err)
			}
			in := Ingress{Conversation: Conversation{ID: MailConversationID(r), Service: service, Account: r.Account, Recipient: r.Chat, Name: "Review", Route: r}, Message: Message{NativeID: parent, Sender: "sender@example.test", Body: "Please review"}}
			if err := s.ValidateIngress(in); err != nil {
				t.Fatal(err)
			}
			// The same native IDs in another mailbox must never reuse a conversation.
			other := r
			other.Account = "second@example.test"
			if MailConversationID(other) == in.Conversation.ID {
				t.Fatal("mailboxes share conversation")
			}
			bad := in
			bad.Conversation.Route.Account = other.Account
			status(t, s.ValidateIngress(bad), 400)
			m, err := s.Ingest(in)
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: m.ID, Body: "Reviewed"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Send("owner", d.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Claim(r.Account, c.Worker, "signal"); err == nil {
				t.Fatal("wrong provider claimed email")
			}
			job, err := s.Claim(r.Account, c.Worker, provider)
			if err != nil || job == nil {
				t.Fatal(err)
			}
			receipt := Receipt{Provider: provider, Account: r.Account, Draft: d.ID, Attempt: job.Draft.Attempt, State: "sent", NativeID: "ff03"}
			if provider == "outlook" {
				_, err = s.Complete(receipt)
				status(t, err, 400)
				receipt.State = "submitted"
				receipt.NativeID = ""
			}
			finished, err := s.Complete(receipt)
			if err != nil {
				t.Fatal(err)
			}
			again, err := s.Send("owner", d.ID)
			if err != nil || again.State != finished.State {
				t.Fatal("reply retried", err)
			}
			if _, err = s.Complete(receipt); err != nil {
				t.Fatal("receipt replay", err)
			}
			items, err := s.Items("owner", false)
			if err != nil {
				t.Fatal(err)
			}
			if provider == "outlook" && len(items) != 1 {
				t.Fatal("unconfirmed handoff cleared attention")
			}
			if provider == "gmail" && len(items) != 0 {
				t.Fatal("confirmed send did not handle reply")
			}
		})
	}
}

func TestMailDestinationAndAttachmentValidation(t *testing.T) {
	for _, account := range []string{"Name <a@example.test>", "a@example.test\r\nBcc:b@example.test", "UPPER@example.test", "missing"} {
		if mailAccount(account) {
			t.Fatalf("accepted invalid account %q", account)
		}
	}
	r := Route{Provider: "outlook", Account: "a@example.test", Chat: strings.Repeat("a", 64)}
	c := Conversation{ID: MailConversationID(r), Service: "Outlook", Account: r.Account, Recipient: r.Chat, Name: "Review", Route: r}
	if err := validateLive(Draft{Target: c, Attachments: []Attachment{{Size: 2<<20 + 1}}}); err == nil {
		t.Fatal("oversize Outlook attachment accepted")
	}
	if strings.Contains(Destination(c), r.Chat) {
		t.Fatal("opaque thread shown as recipient")
	}
}
