package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/messageconfig"
	"github.com/base698/amythest/internal/messages"
	"github.com/base698/amythest/internal/wall"
)

// Messages and the wall meet here: Messages tags a message with the project
// this workspace's wall shows, and the wall lists a project's tagged
// messages and opens one in Messages.

// currentProject is the project the wall would show in this workspace: the
// herdr workspace, matched to a Work project as the wall matches it, or its
// own name when the work server cannot be asked. "" outside herdr.
func currentProject() string {
	place := wall.Locate(nil, nil)
	if place.Project == "" && len(place.Panes) == 0 {
		return ""
	}
	if cfg, err := apiclient.LoadConfig(nil); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if o, err := apiclient.New(cfg).WorkOverview(ctx); err == nil {
			if p := wall.ProjectFor(place, o); p != "" {
				return p
			}
		}
	}
	return place.Project
}

// messagingConfigured reports whether this machine has a Messages owner
// credential, so features that only add to another view stay quiet without.
func messagingConfigured() bool {
	if os.Getenv("AMYTHEST_MESSAGES_TOKEN") != "" {
		return true
	}
	cfg, err := messageconfig.Load("")
	if err != nil {
		return false
	}
	tokenFile := cfg.Client.TokenFile
	if tokenFile == "" {
		tokenFile = cfg.Client.StateDir + "/owner.token"
	}
	_, err = os.Stat(tokenFile)
	return err == nil
}

// withWallMessages gives a wall its tagged messages when Messages is set up.
func withWallMessages(o *wall.Options) {
	if o.Messages != nil || !messagingConfigured() {
		return
	}
	o.Messages = func(ctx context.Context, project string) ([]wall.MessageItem, error) {
		client, err := loadMessageClient("", "", "owner", "")
		if err != nil {
			return nil, err
		}
		tagged, err := client.Tagged(ctx, project, 200)
		if err != nil {
			return nil, err
		}
		out := make([]wall.MessageItem, 0, len(tagged))
		for _, t := range tagged {
			where := messages.Plain(t.Conversation.Name)
			if t.Conversation.Service != "" {
				where += " · " + t.Conversation.Service
			}
			body := t.Message.Body
			if body == "" && len(t.Message.Attachments) > 0 {
				body = "[" + messages.Plain(t.Message.Attachments[0].Name) + "]"
			}
			out = append(out, wall.MessageItem{ID: t.Message.ID, Conversation: t.Conversation.ID,
				Sender: messages.Plain(t.Message.Sender), Body: messages.Plain(body), Where: where, At: t.Tag.At})
		}
		return out, nil
	}
	o.OpenMessage = openMessageFromWall
}

// openMessageFromWall shows a message in an open Messages view; with none
// open, it opens Messages beside the wall, which then lands on it (the
// gateway holds the jump for the next view for a short while).
func openMessageFromWall(conversation, message string) error {
	client, err := loadMessageClient("", "", "owner", "")
	if err != nil {
		return err
	}
	_, taken, err := sendJump(client, messages.Jump{Conversation: conversation, Message: message}, "")
	if err != nil || taken {
		return err
	}
	bin, err := exec.LookPath("herdr-app")
	if err != nil {
		return errors.New("open Messages (amy messages) within half a minute to see it")
	}
	args, _ := json.Marshal(map[string]string{"name": "amythest-messages"})
	if out, err := exec.Command(bin, "do", "mod.open", string(args)).CombinedOutput(); err != nil {
		return errors.New("herdr-app mod.open: " + strings.TrimSpace(string(out)))
	}
	return nil
}
