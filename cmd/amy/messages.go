package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/find"
	"github.com/base698/amythest/internal/messageconfig"
	"github.com/base698/amythest/internal/messages"
	"github.com/base698/amythest/internal/messageui"
	"github.com/base698/amythest/internal/wall"
	tea "github.com/charmbracelet/bubbletea"
)

func messagesState() string {
	return messageconfig.StateDir()
}
func runMessages(args []string) {
	if e := messageCommand(args); e != nil {
		fmt.Fprintln(os.Stderr, "messages:", e)
		os.Exit(1)
	}
}
func messageCommand(args []string) error {
	op := "focus"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		op = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("amy messages "+op, flag.ContinueOnError)
	configFile := fs.String("config", "", "messaging config file (default: ~/.config/amythest/messages.json)")
	state := fs.String("state", messagesState(), "private local POC state directory")
	endpointDefault := os.Getenv("AMYTHEST_MESSAGES_URL")
	if endpointDefault == "" {
		endpointDefault = "http://127.0.0.1:8642"
	}
	endpoint := fs.String("url", endpointDefault, "messaging gateway URL")
	actor := fs.String("actor", "owner", "credential identity: owner or agent")
	tokenFile := fs.String("token-file", "", "credential file (default: <state>/<actor>.token)")
	listen := fs.String("listen", "127.0.0.1:8642", "gateway listen address (loopback only)")
	ircAddr := fs.String("irc", "127.0.0.1:6669", "IRC listen address (loopback only; empty disables)")
	seed := fs.Bool("demo", false, "seed sample conversations when serving")
	all := fs.Bool("all", false, "include handled conversations and quiet contacts")
	trash := fs.Bool("trash", false, "show conversations in Amythest trash")
	service := fs.String("service", "", "filter focus by service, e.g. Signal")
	body := fs.String("text", "", "reply text")
	replyTo := fs.String("reply-to", "", "message ID to reply to")
	attachment := fs.String("attachment", "", "comma-separated attachment IDs")
	file := fs.String("file", "", "attachment input/output path or JSON ingress file")
	name := fs.String("name", "pasted-text.txt", "attachment name when using -stdin")
	stdin := fs.Bool("stdin", false, "read attachment bytes from standard input")
	conversation := fs.String("conversation", "", "conversation ID")
	duration := fs.Duration("duration", 30*time.Minute, "agent grant duration (up to 24h)")
	allowSend := fs.Bool("send", false, "grant permission to send in this conversation")
	allowFiles := fs.Bool("files", false, "grant permission to use attachments")
	ircClient := fs.String("irc-client", "", "IRC nickname to focus")
	gotoTarget := fs.String("goto", "", "open at msg:ID, conv:ID, focus, all or drafts (jump: send an open view there)")
	pane := fs.String("irc-pane", os.Getenv("AMYTHEST_MESSAGES_IRC_PANE"), "herdr pane containing the IRC client")
	ownPane := fs.Bool("pane", false, "toggle Messages in this Herdr App tab; close its pane on exit")
	if e := fs.Parse(args); e != nil {
		return e
	}
	owned := ""
	if *ownPane && op == "focus" {
		var closed bool
		var e error
		owned, closed, e = wall.ToggleAppPane(os.Getenv("HC_TERMINAL_ID"), "messages", nil)
		if closed || e != nil {
			return e
		}
	}
	cfg, e := messageconfig.Load(*configFile)
	if e != nil {
		return e
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if !set["state"] {
		if op == "serve" || op == "demo" {
			*state = cfg.Server.StateDir
		} else {
			*state = cfg.Client.StateDir
		}
	}
	if !set["url"] {
		*endpoint = cfg.Client.URL
	}
	if !set["listen"] {
		*listen = cfg.Server.Listen
	}
	if !set["irc"] {
		*ircAddr = cfg.Server.IRC
	}
	if !set["demo"] {
		*seed = cfg.Server.Demo
	}
	if !set["irc-client"] {
		*ircClient = cfg.Client.IRCClient
	}
	if !set["irc-pane"] {
		*pane = cfg.Client.IRCPane
	}
	if !set["token-file"] && *actor == "owner" {
		*tokenFile = cfg.Client.TokenFile
	}
	if *actor != "owner" && *actor != "agent" {
		return fmt.Errorf("-actor must be owner or agent")
	}
	if op == "serve" || op == "demo" {
		s, e := messages.Open(*state)
		if e != nil {
			return e
		}
		defer s.Close()
		if *seed || op == "demo" {
			if e = s.Seed(); e != nil {
				return e
			}
		}
		owner, e := messages.Token(*state, "owner")
		if e != nil {
			return e
		}
		agent, e := messages.Token(*state, "agent")
		if e != nil {
			return e
		}
		g := &messages.Gateway{Store: s, OwnerToken: owner, AgentToken: agent}
		g.ConnectorToken, e = messages.Token(*state, "telegram")
		if e != nil {
			return e
		}
		signalToken, e := messages.Token(*state, "signal")
		if e != nil {
			return e
		}
		imessageToken, e := messages.Token(*state, "imessage")
		if e != nil {
			return e
		}
		g.ConnectorTokens = map[string]string{"signal": signalToken, "imessage": imessageToken}
		for _, provider := range []string{"gmail", "outlook", "slack", "outlook-web", "outlook-calendar"} {
			token, err := messages.Token(*state, provider)
			if err != nil {
				return err
			}
			g.ConnectorTokens[provider] = token
		}
		httpL, e := messageListen(*listen)
		if e != nil {
			return e
		}
		defer httpL.Close()
		if *ircAddr != "" {
			l, e := messageListen(*ircAddr)
			if e != nil {
				return e
			}
			defer l.Close()
			g.IRC = messages.NewIRC(s, owner)
			defer g.IRC.Close()
			go g.IRC.Serve(l)
		}
		server := &http.Server{Handler: g.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
		go server.Serve(httpL)
		defer messages.Shutdown(context.Background(), server)
		addr := "http://" + httpL.Addr().String()
		if op == "demo" {
			return showMessages(messages.NewClient(addr, owner), messageui.Options{IRCClient: *ircClient}, "")
		}
		fmt.Printf("Messaging gateway: %s\nIRC: %s\nState: %s\nDemo replies are simulated. Live delivery requires a connected provider.\n", addr, *ircAddr, *state)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		return nil
	}
	if op == "tunnel" {
		return cfg.Client.EnsureTunnel(context.Background())
	}
	if op == "config" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"path": cfg.Path, "client": cfg.Client, "server": cfg.Server})
	}
	client, e := loadMessageClientWithConfig(cfg.Client, *endpoint, *state, *actor, *tokenFile)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var result any
	switch op {
	case "focus":
		opts := messageui.Options{IRCClient: *ircClient, All: *all, Service: *service, Trash: *trash, Listener: messageListener(thisPane())}
		if opts.Goto, e = parseGoto(*gotoTarget); e != nil {
			return e
		}
		if *attachment != "" {
			available, e := client.Attachments(ctx)
			if e != nil {
				return e
			}
			for _, id := range strings.Split(*attachment, ",") {
				found := false
				for _, a := range available {
					if a.ID == id {
						opts.Attachments = append(opts.Attachments, a)
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("attachment %s is unavailable", id)
				}
			}
		}
		if *pane != "" {
			target := *pane
			opts.FocusPane = func() error { return find.Focus(target, nil) }
		}
		return showMessages(client, opts, owned)
	case "jump":
		j, e := parseGoto(*gotoTarget)
		if e != nil {
			return e
		}
		if j == nil {
			return fmt.Errorf("usage: amy messages jump -goto msg:ID|conv:ID|focus|all|drafts")
		}
		res, taken, e := sendJump(client, *j, "")
		if e != nil {
			return e
		}
		switch {
		case taken:
		case len(res.Listeners) > 0:
			fmt.Fprintln(os.Stderr, "Sent to the Messages view open on", res.Listeners[0].Machine+"; none is open here.")
		default:
			fmt.Fprintln(os.Stderr, "No Messages view is open; the next one opened within 30s goes there.")
		}
		result = res
	case "list":
		if *trash {
			var items []messages.Item
			e = client.Do(ctx, "GET", "/items?all=1&trash=1", nil, &items)
			result = items
		} else {
			result, e = client.Items(ctx, *all)
		}
	case "contact":
		if *conversation == "" || !set["name"] {
			return fmt.Errorf("use -conversation ID -name NAME (empty clears the name)")
		}
		e = client.Do(ctx, "PUT", "/contacts", map[string]string{"conversation": *conversation, "name": *name}, nil)
		result = map[string]bool{"ok": e == nil}
	case "trash", "restore":
		if *conversation == "" {
			return fmt.Errorf("use -conversation CONVERSATION_ID")
		}
		e = client.Do(ctx, "POST", "/trash", map[string]any{"conversation": *conversation, "trashed": op == "trash"}, nil)
		result = map[string]string{"conversation": *conversation, "action": op}
	case "thread":
		result, e = client.Thread(ctx, *conversation)
	case "draft":
		in := messages.DraftInput{RequestID: messages.ID(), ReplyTo: *replyTo, Body: *body}
		if *attachment != "" {
			in.Attachments = strings.Split(*attachment, ",")
		}
		result, e = client.Draft(ctx, in)
	case "send":
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: amy messages send [flags] DRAFT_ID")
		}
		result, e = client.Send(ctx, fs.Arg(0))
	case "drafts":
		var ds []messages.Draft
		e = client.Do(ctx, "GET", "/drafts", nil, &ds)
		result = ds
	case "connectors":
		var cs []messages.Connector
		e = client.Do(ctx, "GET", "/connectors", nil, &cs)
		result = cs
	case "grant":
		e = client.Do(ctx, "POST", "/grants", messages.Grant{Conversation: *conversation, Expires: time.Now().Add(*duration), Send: *allowSend, Attachments: *allowFiles}, nil)
		result = map[string]string{"grant": *conversation}
	case "revoke":
		e = client.Do(ctx, "DELETE", "/grants?conversation="+url.QueryEscape(*conversation), nil, nil)
		result = map[string]string{"revoked": *conversation}
	case "attach":
		var data []byte
		if *stdin {
			data, e = io.ReadAll(io.LimitReader(os.Stdin, messages.MaxAttachment+1))
		} else {
			if *file == "" {
				return fmt.Errorf("use -file PATH or -stdin")
			}
			data, e = readMessageFile(*file, messages.MaxAttachment)
			*name = filepath.Base(*file)
		}
		if e == nil {
			result, e = client.Attach(ctx, *name, data)
		}
	case "attachment":
		if fs.NArg() != 1 || *file == "" {
			return fmt.Errorf("usage: amy messages attachment -file OUTPUT ATTACHMENT_ID")
		}
		req, e := http.NewRequestWithContext(ctx, "GET", client.URL+"/attachment?id="+url.QueryEscape(fs.Arg(0)), nil)
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+client.Token)
		resp, e := client.HTTP.Do(req)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("attachment download failed: HTTP %d", resp.StatusCode)
		}
		f, e := os.OpenFile(*file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = io.Copy(f, io.LimitReader(resp.Body, messages.MaxAttachment+1))
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		result = map[string]string{"saved": *file}
	case "ingest":
		if *file == "" {
			return fmt.Errorf("use -file with an ingress JSON fixture")
		}
		b, e := readMessageFile(*file, 1<<20)
		if e != nil {
			return e
		}
		var in messages.Ingress
		if e = json.Unmarshal(b, &in); e != nil {
			return e
		}
		var m messages.Message
		e = client.Do(ctx, "POST", "/ingest", in, &m)
		result = m
	default:
		return fmt.Errorf("unknown command %q; use focus, config, demo, serve, list, thread, draft, send, drafts, connectors, attach, attachment, grant, revoke, or ingest", op)
	}
	if e != nil {
		return e
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
func readMessageFile(path string, max int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if int64(len(b)) > max {
		return nil, fmt.Errorf("file exceeds %d bytes", max)
	}
	return b, e
}
func loadMessageClient(endpoint, state, actor, tokenFile string) (*messages.Client, error) {
	cfg, e := messageconfig.Load("")
	if e != nil {
		return nil, e
	}
	if endpoint == "" {
		endpoint = cfg.Client.URL
	}
	if state == "" {
		state = cfg.Client.StateDir
	}
	if tokenFile == "" && actor == "owner" {
		tokenFile = cfg.Client.TokenFile
	}
	return loadMessageClientWithConfig(cfg.Client, endpoint, state, actor, tokenFile)
}
func loadMessageClientWithConfig(cfg messageconfig.Client, endpoint, state, actor, tokenFile string) (*messages.Client, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid messaging URL")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, fmt.Errorf("messaging requires HTTPS or a loopback HTTP tunnel")
		}
	}
	if endpoint == cfg.URL {
		if e := cfg.EnsureTunnel(context.Background()); e != nil {
			return nil, e
		}
	}
	token := os.Getenv("AMYTHEST_MESSAGES_TOKEN")
	if token == "" {
		if tokenFile == "" {
			tokenFile = filepath.Join(state, actor+".token")
		}
		b, e := os.ReadFile(tokenFile)
		if e != nil {
			return nil, fmt.Errorf("no messaging credential; start amy messages serve -demo, or configure -token-file")
		}
		token = strings.TrimSpace(string(b))
	}
	return messages.NewClient(endpoint, token), nil
}
func messageListen(addr string) (net.Listener, error) {
	host, _, e := net.SplitHostPort(addr)
	if e != nil {
		return nil, e
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("POC listeners must use a numeric loopback address; use SSH forwarding for remote clients")
	}
	l, e := net.Listen("tcp", addr)
	if e != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, e)
	}
	return l, nil
}
func showMessages(client *messages.Client, opts messageui.Options, pane string) error {
	opts.Open = func(raw string) error {
		if strings.HasPrefix(raw, "slack://user?") {
			u, err := url.Parse(raw)
			if err != nil {
				return err
			}
			expected := messages.SlackPersonURL(messages.Conversation{Service: "Slack", Account: u.Query().Get("team") + ":U0"}, messages.Message{SenderID: u.Query().Get("id")})
			if expected == "" || raw != expected {
				return fmt.Errorf("invalid Slack person link")
			}
			return openDesktopURL(raw)
		}
		return openURL(raw)
	}
	if opts.Notes == nil {
		cfg, err := apiclient.LoadConfig(nil)
		if err != nil {
			return err
		}
		opts.Notes = apiclient.New(cfg)
	}
	_, e := tea.NewProgram(messageui.New(client, opts), tea.WithAltScreen()).Run()
	if e == nil && pane != "" {
		return wall.ClosePane(pane, nil)
	}
	if errors.Is(e, tea.ErrProgramKilled) {
		return nil
	}
	return e
}
