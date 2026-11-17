package messages

import (
	"bufio"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// IRC is a deliberately small IRC presentation of stored conversations, not an
// IRC network server. It only accepts the owner's token and fixed channel IDs.
type IRC struct {
	Store   *Store
	Token   string
	mu      sync.Mutex
	clients map[*ircClient]bool
}
type ircClient struct {
	server                          *IRC
	conn                            net.Conn
	mu                              sync.Mutex
	nick                            string
	authenticated, registered, user bool
	joined                          map[string]string // channel -> conversation
	seen                            map[string]bool
}

var nickPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,29}$`)

func NewIRC(s *Store, token string) *IRC {
	return &IRC{Store: s, Token: token, clients: map[*ircClient]bool{}}
}
func (s *IRC) Serve(l net.Listener) error {
	for {
		c, e := l.Accept()
		if e != nil {
			return e
		}
		x := &ircClient{server: s, conn: c, joined: map[string]string{}, seen: map[string]bool{}}
		s.mu.Lock()
		s.clients[x] = true
		s.mu.Unlock()
		go x.run()
	}
}
func (s *IRC) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		c.conn.Close()
	}
}
func (s *IRC) list() []*ircClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*ircClient{}
	for c := range s.clients {
		out = append(out, c)
	}
	return out
}
func (c *ircClient) send(line string) {
	// Limit every line by bytes, never split UTF-8 or allow a second IRC command.
	line = strings.ReplaceAll(strings.ReplaceAll(Plain(line), "\r", " "), "\n", " ")
	if len(line) > 500 {
		line = line[:497]
		for !utf8.ValidString(line) {
			line = line[:len(line)-1]
		}
		line += "..."
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, _ = fmt.Fprint(c.conn, line+"\r\n")
}
func (c *ircClient) notice(text string) { c.send(":amy.local NOTICE " + c.nick + " :" + text) }
func splitIRC(line string) (string, []string) {
	head, tail, has := strings.Cut(line, " :")
	parts := strings.Fields(head)
	if len(parts) == 0 {
		return "", nil
	}
	cmd := strings.ToUpper(parts[0])
	args := parts[1:]
	if has {
		args = append(args, tail)
	}
	return cmd, args
}
func (c *ircClient) run() {
	defer func() { c.conn.Close(); c.server.mu.Lock(); delete(c.server.clients, c); c.server.mu.Unlock() }()
	_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 4096), 65536)
	for sc.Scan() {
		cmd, args := splitIRC(sc.Text())
		c.mu.Lock()
		keep := c.command(cmd, args)
		c.mu.Unlock()
		if !keep {
			return
		}
	}
}
func (c *ircClient) command(cmd string, a []string) bool {
	switch cmd {
	case "PASS":
		if len(a) > 0 {
			c.authenticated = sameToken(a[0], c.server.Token)
		}
	case "NICK":
		if len(a) > 0 && nickPattern.MatchString(a[0]) && !c.registered {
			c.nick = a[0]
		}
	case "USER":
		c.user = true
	case "CAP":
		if len(a) > 0 {
			switch strings.ToUpper(a[0]) {
			case "LS":
				c.send(":amy.local CAP * LS :")
			case "REQ":
				c.send(":amy.local CAP * NAK :" + strings.Join(a[1:], " "))
			}
		}
	case "PING":
		if len(a) > 0 {
			c.send(":amy.local PONG amy.local :" + a[0])
		}
		return true
	case "QUIT":
		return false
	}
	if !c.registered && c.nick != "" && c.user {
		if !c.authenticated {
			c.send(":amy.local 464 " + c.nick + " :Owner token required")
			return false
		}
		c.registered = true
		_ = c.conn.SetReadDeadline(time.Time{})
		c.send(":amy.local 001 " + c.nick + " :Amythest messaging — demo replies are simulated; live Telegram replies send")
		c.send(":amy.local 005 " + c.nick + " CHANTYPES=# CASEMAPPING=ascii :are supported")
		c.send(":amy.local 375 " + c.nick + " :- Message gateway")
		c.send(":amy.local 372 " + c.nick + " :Use /LIST, then /JOIN. Load amythest.pl for focus jumps.")
		c.send(":amy.local 376 " + c.nick + " :End of MOTD")
	}
	if !c.registered {
		return true
	}
	switch cmd {
	case "LIST":
		items, e := c.server.Store.Items("owner", true)
		if e != nil {
			c.notice(e.Error())
			break
		}
		c.send(":amy.local 321 " + c.nick + " Channel :Conversations")
		for _, it := range items {
			c.send(fmt.Sprintf(":amy.local 322 %s %s 1 :%s", c.nick, it.Conversation.Channel, Destination(it.Conversation)))
		}
		c.send(":amy.local 323 " + c.nick + " :End of LIST")
	case "JOIN":
		if len(a) > 0 {
			for _, ch := range strings.Split(a[0], ",") {
				c.join(ch, true)
			}
		}
	case "PART":
		if len(a) > 0 {
			if _, ok := c.joined[a[0]]; ok {
				c.send(":" + c.nick + "!owner@amy.local PART " + a[0] + " :Leaving")
				delete(c.joined, a[0])
			}
		}
	case "MODE":
		if len(a) > 0 && strings.HasPrefix(a[0], "#") {
			c.send(":amy.local 324 " + c.nick + " " + a[0] + " +nt")
		}
	case "WHO":
		if len(a) > 0 {
			c.send(":amy.local 315 " + c.nick + " " + a[0] + " :End of WHO")
		}
	case "NAMES":
		if len(a) > 0 {
			c.names(a[0])
		}
	case "PRIVMSG":
		if len(a) < 2 {
			break
		}
		id, ok := c.joined[a[0]]
		if !ok {
			c.notice("Join a conversation before replying")
			break
		}
		t, e := c.server.Store.Thread("owner", id)
		if e != nil {
			c.notice(e.Error())
			break
		}
		parent := ""
		for _, m := range t.Messages {
			if !m.Outgoing {
				parent = m.ID
			}
		}
		d, e := c.server.Store.Draft("owner", DraftInput{RequestID: ID(), ReplyTo: parent, Body: a[1]})
		if e == nil {
			d, e = c.server.Store.Send("owner", d.ID)
		}
		if e != nil {
			c.notice(e.Error())
		} else {
			c.notice(DeliveryStatus(d))
			go c.server.Broadcast("reply-" + d.ID)
		}
	}
	return true
}
func (c *ircClient) names(ch string) {
	c.send(":amy.local 353 " + c.nick + " = " + ch + " :" + c.nick + " amy")
	c.send(":amy.local 366 " + c.nick + " " + ch + " :End of NAMES")
}
func (c *ircClient) join(ch string, replay bool) {
	if _, ok := c.joined[ch]; ok {
		return
	}
	items, e := c.server.Store.Items("owner", true)
	if e != nil {
		c.notice(e.Error())
		return
	}
	for _, it := range items {
		if it.Conversation.Channel != ch {
			continue
		}
		c.joined[ch] = it.Conversation.ID
		c.send(":" + c.nick + "!owner@amy.local JOIN " + ch)
		c.send(":amy.local 332 " + c.nick + " " + ch + " :" + Destination(it.Conversation))
		c.names(ch)
		if replay {
			t, e := c.server.Store.Thread("owner", it.Conversation.ID)
			if e == nil {
				start := max(0, len(t.Messages)-20)
				for _, m := range t.Messages[start:] {
					c.message(ch, m, false)
				}
			}
		}
		return
	}
	c.send(":amy.local 403 " + c.nick + " " + ch + " :Conversation not found")
}
func (c *ircClient) message(ch string, m Message, selected bool) {
	c.seen[m.ID] = true
	marker := ""
	if selected {
		marker = ">>> "
	}
	prefix := fmt.Sprintf("%s%s %s [%s] ", marker, m.At.Local().Format("15:04"), m.Sender, m.ID)
	for i, line := range strings.Split(Plain(m.Body), "\n") {
		if i >= 30 {
			c.send(":amy!gateway@amy.local PRIVMSG " + ch + " :[continued in Amythest]")
			break
		}
		c.send(":amy!gateway@amy.local PRIVMSG " + ch + " :" + prefix + line)
		prefix = "    "
	}
	for _, a := range m.Attachments {
		c.send(":amy!gateway@amy.local PRIVMSG " + ch + " :[attachment] " + a.Name + " — download with amy messages attachment -file OUTPUT " + a.ID)
	}
}
func (s *IRC) Broadcast(id string) {
	m, e := s.Store.Message("owner", id)
	if e != nil {
		return
	}
	for _, c := range s.list() {
		c.mu.Lock()
		if c.registered && !c.seen[id] {
			for ch, conv := range c.joined {
				if conv == m.Conversation {
					c.message(ch, m, false)
				}
			}
		}
		c.mu.Unlock()
	}
}
func (s *IRC) Focus(id, nick string) (any, error) {
	m, e := s.Store.Message("owner", id)
	if e != nil {
		return nil, e
	}
	t, e := s.Store.Thread("owner", m.Conversation)
	if e != nil {
		return nil, e
	}
	candidates := []*ircClient{}
	for _, c := range s.list() {
		c.mu.Lock()
		if c.registered && (nick == "" || nick == c.nick) {
			candidates = append(candidates, c)
		}
		c.mu.Unlock()
	}
	if len(candidates) == 0 {
		return nil, fault(409, "No IRC client connected. Preview and direct reply are available here.")
	}
	if len(candidates) > 1 {
		return nil, fault(409, "Several IRC clients are connected; choose one with -irc-client")
	}
	c := candidates[0]
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := t.Conversation.Channel
	c.join(ch, false)
	c.send(":amy.local 290 " + c.nick + " " + ch + " " + id + " :Amythest focus")
	c.send(":amy!gateway@amy.local PRIVMSG " + ch + " :── Selected message context ──")
	idx := 0
	for i, x := range t.Messages {
		if x.ID == id {
			idx = i
			break
		}
	}
	for _, x := range t.Messages[max(0, idx-3):min(len(t.Messages), idx+4)] {
		c.message(ch, x, x.ID == id)
	}
	return map[string]string{"channel": ch, "message": id, "client": c.nick}, nil
}
