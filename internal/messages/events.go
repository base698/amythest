package messages

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Jump asks an open Messages view to show something: a message, a whole
// conversation, or a section of the view. A message alone is enough; the
// gateway fills in its conversation.
type Jump struct {
	Conversation string `json:"conversation,omitempty"`
	Message      string `json:"message,omitempty"`
	Section      string `json:"section,omitempty"` // focus, all or drafts
}

var jumpSections = map[string]bool{"": true, "focus": true, "all": true, "drafts": true}

// Event is one thing that happened, numbered in order. Jumps are the only
// kind so far; views ignore kinds they do not know.
type Event struct {
	Seq  int64     `json:"seq"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Jump *Jump     `json:"jump,omitempty"`
}

// Events is one long-poll answer: what happened after the asked sequence
// number, and the number to ask from next.
type Events struct {
	Seq    int64   `json:"seq"`
	Events []Event `json:"events"`
}

// Listener is an open Messages view, known by the pane it runs in.
type Listener struct {
	Machine string `json:"machine"`
	Pane    string `json:"pane"`
}

// JumpResult says which open views will receive a jump. With none, the jump
// waits briefly for the next view to open.
type JumpResult struct {
	Jump      Jump       `json:"jump"`
	Seq       int64      `json:"seq"`
	Listeners []Listener `json:"listeners"`
}

const (
	eventBacklog = 64
	// A jump nobody received is handed to a view that opens within this
	// time: the one opened because nothing was listening.
	pendingJump = 30 * time.Second
	// A view re-polls at once after each answer; one silent for longer has
	// closed.
	listenerLife = 15 * time.Second
	maxEventWait = 10 * time.Second // below the gateway's 20s write timeout
)

type pending struct {
	Event
	delivered bool
}

// hub keeps recent events in memory. Nothing here needs to survive a gateway
// restart: a jump is only useful for seconds.
type hub struct {
	mu        sync.Mutex
	seq       int64
	events    []pending
	wake      chan struct{}
	listeners map[Listener]time.Time
	waiting   map[Listener]int
	now       func() time.Time
}

func newHub(now func() time.Time) *hub {
	return &hub{wake: make(chan struct{}), listeners: map[Listener]time.Time{}, waiting: map[Listener]int{}, now: now}
}

func (h *hub) publish(kind string, j *Jump) (int64, []Listener) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	h.events = append(h.events, pending{Event: Event{Seq: h.seq, At: h.now().UTC(), Kind: kind, Jump: j}})
	if len(h.events) > eventBacklog {
		h.events = h.events[len(h.events)-eventBacklog:]
	}
	close(h.wake)
	h.wake = make(chan struct{})
	return h.seq, h.live()
}

// live lists views that are polling now or polled moments ago.
func (h *hub) live() []Listener {
	now := h.now()
	out := []Listener{}
	for l, seen := range h.listeners {
		if h.waiting[l] > 0 || now.Sub(seen) < listenerLife {
			out = append(out, l)
		} else {
			delete(h.listeners, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Machine+out[i].Pane < out[j].Machine+out[j].Pane })
	return out
}

// take returns events after since. since < 0 means a view that just opened:
// it gets only recent jumps nobody else received.
func (h *hub) take(since int64) Events {
	out := Events{Seq: h.seq, Events: []Event{}}
	now := h.now()
	for i := range h.events {
		e := &h.events[i]
		fresh := since < 0 && e.Kind == "jump" && !e.delivered && now.Sub(e.At) < pendingJump
		if fresh || since >= 0 && e.Seq > since {
			out.Events = append(out.Events, e.Event)
			e.delivered = true
		}
	}
	return out
}

func (h *hub) poll(ctx context.Context, l Listener, since int64, wait time.Duration) Events {
	wait = max(0, min(wait, maxEventWait))
	h.mu.Lock()
	h.listeners[l] = h.now()
	h.waiting[l]++
	defer func() {
		h.mu.Lock()
		h.listeners[l] = h.now()
		if h.waiting[l]--; h.waiting[l] <= 0 {
			delete(h.waiting, l)
		}
		h.mu.Unlock()
	}()
	if got := h.take(since); len(got.Events) > 0 || wait == 0 {
		h.mu.Unlock()
		return got
	}
	if since < 0 {
		since = h.seq
	}
	wake := h.wake
	h.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-wake:
	case <-timer.C:
	case <-ctx.Done():
	}
	h.mu.Lock()
	got := h.take(since)
	h.mu.Unlock()
	return got
}

func (g *Gateway) events() *hub {
	g.hubOnce.Do(func() {
		now := time.Now
		if g.Store != nil && g.Store.now != nil {
			now = func() time.Time { return g.Store.now() }
		}
		g.hub = newHub(now)
	})
	return g.hub
}

// Jump checks that the target exists, then hands it to the open views.
func (g *Gateway) Jump(j Jump) (JumpResult, error) {
	if !jumpSections[j.Section] {
		return JumpResult{}, fault(400, "section must be focus, all or drafts")
	}
	if j.Message != "" {
		m, e := g.Store.Message("owner", j.Message)
		if e != nil {
			return JumpResult{}, e
		}
		if j.Conversation != "" && j.Conversation != m.Conversation {
			return JumpResult{}, fault(400, "message is not in that conversation")
		}
		j.Conversation = m.Conversation
	} else if j.Conversation != "" {
		if _, e := g.Store.ThreadPage("owner", j.Conversation, Page{Limit: 1}); e != nil {
			return JumpResult{}, e
		}
	} else if j.Section == "" {
		return JumpResult{}, fault(400, "jump needs a message, conversation or section")
	}
	seq, live := g.events().publish("jump", &j)
	return JumpResult{Jump: j, Seq: seq, Listeners: live}, nil
}
