package messages

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func readUntil(t *testing.T, c net.Conn, r *bufio.Reader, part string) string {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var out strings.Builder
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			t.Fatalf("IRC did not receive %q: %v; %s", part, e, out.String())
		}
		out.WriteString(line)
		if strings.Contains(line, part) {
			return out.String()
		}
	}
}
func TestIRCFocusContextAndReply(t *testing.T) {
	s := testStore(t)
	m := incoming(t, s, "chat", "personal", true)
	token := strings.Repeat("a", 64)
	irc := NewIRC(s, token)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	defer irc.Close()
	go irc.Serve(l)
	c, e := net.Dial("tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	r := bufio.NewReader(c)
	fmt.Fprintf(c, "CAP LS 302\r\nPASS %s\r\nNICK test\r\nUSER test 0 * :test\r\nCAP END\r\n", token)
	readUntil(t, c, r, " 376 ")
	result, e := irc.Focus(m.ID, "test")
	if e != nil {
		t.Fatal(e)
	}
	ch := result.(map[string]string)["channel"]
	text := readUntil(t, c, r, ">>> ")
	if !strings.Contains(text, " 290 test "+ch+" "+m.ID) || !strings.Contains(text, "JOIN "+ch) || !strings.Contains(text, "Can you send the patch?") {
		t.Fatalf("focus lost message identity or context: %s", text)
	}
	fmt.Fprintf(c, "PRIVMSG %s :Here is my reply\r\n", ch)
	readUntil(t, c, r, "Reply recorded as simulated")
	th, e := s.Thread("owner", m.Conversation)
	if e != nil || len(th.Messages) != 2 || th.Messages[1].ReplyTo != m.ID || th.Messages[1].Body != "Here is my reply" {
		t.Fatalf("IRC reply lost routing: %+v %v", th, e)
	}
	// Repeated broadcasts are idempotent per connected client.
	readUntil(t, c, r, "Here is my reply")
	irc.Broadcast(th.Messages[1].ID)
	fmt.Fprint(c, "PING :after-duplicate\r\n")
	text = readUntil(t, c, r, "after-duplicate")
	if strings.Contains(text, "PRIVMSG") {
		t.Fatalf("duplicate IRC delivery: %s", text)
	}
}
func TestIRCRejectsAgentOrMissingOwnerToken(t *testing.T) {
	s := testStore(t)
	irc := NewIRC(s, strings.Repeat("a", 64))
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	defer irc.Close()
	go irc.Serve(l)
	c, e := net.Dial("tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	fmt.Fprint(c, "PASS wrong\r\nNICK test\r\nUSER test 0 * :test\r\n")
	readUntil(t, c, bufio.NewReader(c), " 464 ")
}
