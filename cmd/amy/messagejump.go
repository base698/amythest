package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/base698/amythest/internal/chatui"
	"github.com/base698/amythest/internal/find"
	"github.com/base698/amythest/internal/messageconfig"
	"github.com/base698/amythest/internal/messages"
	"github.com/base698/amythest/internal/messageui"
	"github.com/base698/amythest/internal/wall"
	tea "github.com/charmbracelet/bubbletea"
)

// parseGoto reads where `-goto` points:
//
//	msg:MESSAGE_ID        a message (its conversation is looked up)
//	conv:CONVERSATION_ID  a whole conversation
//	focus | all | drafts  a section of the view
func parseGoto(s string) (*messages.Jump, error) {
	switch {
	case s == "":
		return nil, nil
	case s == "focus" || s == "all" || s == "drafts":
		return &messages.Jump{Section: s}, nil
	case strings.HasPrefix(s, "msg:") && len(s) > 4:
		return &messages.Jump{Message: s[4:]}, nil
	case strings.HasPrefix(s, "conv:") && len(s) > 5:
		return &messages.Jump{Conversation: s[5:]}, nil
	}
	return nil, fmt.Errorf("-goto takes msg:ID, conv:ID, focus, all or drafts")
}

// thisPane names the herdr pane this program runs in, if any. Terminal
// herdr comes first: a herdr started inside a Herdr App terminal inherits
// that terminal's HC_TERMINAL_ID too.
func thisPane() string {
	if os.Getenv("HERDR_WORKSPACE_ID") != "" {
		return os.Getenv("HERDR_PANE_ID")
	}
	return os.Getenv("HC_TERMINAL_ID")
}

func thisMachine() string {
	h, _ := os.Hostname()
	return h
}

// messageListener is how an open Messages view introduces itself, so a jump
// can bring its pane forward.
func messageListener(pane string) messages.Listener {
	return messages.Listener{Machine: thisMachine(), Pane: pane}
}

// sendJump hands a jump to the open Messages views and brings one on this
// machine forward. It reports whether a view here took it; when none did,
// the caller opens one (which picks up the jump).
func sendJump(client *messages.Client, j messages.Jump, except string) (messages.JumpResult, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := client.Jump(ctx, j)
	if err != nil {
		return res, false, err
	}
	here := thisMachine()
	for _, l := range res.Listeners {
		if l.Machine != here || l.Pane == "" || l.Pane == except {
			continue
		}
		if err := find.Focus(l.Pane, nil); err != nil {
			return res, true, fmt.Errorf("Messages has it, but its pane %s could not be focused: %w", l.Pane, err)
		}
		return res, true, nil
	}
	return res, false, nil
}

// openMessagesAt shows a message from amy find: in the open Messages view if
// there is one, else in find's own pane, which becomes Messages.
func openMessagesAt(ref *find.MessageRef, pane string) error {
	client, err := loadMessageClient("", "", "owner", "")
	if err != nil {
		return err
	}
	res, taken, err := sendJump(client, messages.Jump{Conversation: ref.Conversation, Message: ref.Message}, pane)
	if err != nil || taken {
		if err == nil && pane != "" {
			wall.ClosePane(pane, nil)
		}
		return err
	}
	if cfg, err := messageconfig.Load(""); err == nil && cfg.Client.View == "chat" {
		return showChat(client, chatui.Options{Goto: &res.Jump, Listener: messageListener(pane), EventsAfter: res.Seq}, pane)
	}
	return showMessages(client, messageui.Options{Goto: &res.Jump, Listener: messageListener(pane), EventsAfter: res.Seq}, pane)
}

// showChat runs the irssi-style client, closing its own pane on exit.
func showChat(client *messages.Client, opts chatui.Options, pane string) error {
	// Pictures are drawn as the wall draws them; AMY_WALL_IMAGES overrides.
	out := &serialOut{File: os.Stdout}
	var w wall.Options
	if e := configureWallGraphics("auto", &w, out); e != nil {
		return e
	}
	opts.Graphics, opts.Out, opts.CellW, opts.CellAspect = w.Graphics, out, w.CellW, w.CellAspect
	m := chatui.New(client, opts)
	_, e := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(out)).Run()
	m.Cleanup()
	if e == nil && pane != "" {
		return wall.ClosePane(pane, nil)
	}
	if errors.Is(e, tea.ErrProgramKilled) {
		return nil
	}
	return e
}

// messageSearch is amy find's Messages source, or nil when messaging is not
// set up on this machine (no credential), so find does not show an error
// for something never configured.
func messageSearch() func(context.Context, string) ([]find.Result, error) {
	cfg, err := messageconfig.Load("")
	if err != nil {
		return nil
	}
	if os.Getenv("AMYTHEST_MESSAGES_TOKEN") == "" {
		tokenFile := cfg.Client.TokenFile
		if tokenFile == "" {
			tokenFile = cfg.Client.StateDir + "/owner.token"
		}
		if _, err := os.Stat(tokenFile); err != nil {
			return nil
		}
	}
	var (
		mu     sync.Mutex
		client *messages.Client
	)
	return func(ctx context.Context, q string) ([]find.Result, error) {
		mu.Lock()
		if client == nil {
			c, err := loadMessageClient("", "", "owner", "")
			if err != nil {
				mu.Unlock()
				return nil, err
			}
			client = c
		}
		mu.Unlock()
		hits, err := client.Search(ctx, q, "", 20)
		if err != nil {
			return nil, err
		}
		out := make([]find.Result, 0, len(hits))
		for _, h := range hits {
			out = append(out, find.Result{
				Group: "messages", Kind: "msg",
				Title:        messages.Plain(h.Message.Sender),
				Snippet:      strings.Join(strings.Fields(h.Snippet), " "),
				Where:        messages.Destination(h.Conversation),
				When:         h.Message.At,
				Conversation: h.Conversation.ID,
				Message:      h.Message.ID,
			})
		}
		return out, nil
	}
}
