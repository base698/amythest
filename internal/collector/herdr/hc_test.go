package herdr

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// serve answers one events.subscribe request with reply, then sends extra lines.
func serve(t *testing.T, reply string, extra ...string) {
	t.Helper()
	dir, e := os.MkdirTemp("/tmp", "hc") // a unix socket path must stay short
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	l, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close() })
	t.Setenv("AMYTHEST_COLLECTOR_HC_SOCK", path)
	go func() {
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		bufio.NewReader(c).ReadString('\n')
		c.Write([]byte(reply + "\n"))
		for _, x := range extra {
			c.Write([]byte(x + "\n"))
		}
		time.Sleep(2 * time.Second)
	}()
}

func TestHCSubscribeDecodesStatusObjects(t *testing.T) {
	serve(t, `{"jsonrpc":"2.0","id":1,"result":{"seq":7,"terminals":[`+
		`{"address":"t1","state":"live","status":{"app":"claude-code","state":"working"},"labels":{"where":"a / b"},"foreground":{"pid":9,"argv":["claude"]}},`+
		`{"address":"t2","state":"live","status":null}]}}`,
		`{"jsonrpc":"2.0","method":"event","params":{"seq":7,"type":"status","terminal":"t1"}}`,
		`{"jsonrpc":"2.0","method":"event","params":{"seq":8,"type":"turn","terminal":"t1"}}`)
	terms, events, stop, e := HCSubscribe()
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	if len(terms) != 2 || terms[0].Status == nil || terms[0].Status.App != "claude-code" || terms[0].StatusString() != "working" || terms[1].Status != nil || terms[0].Fg.Pid != 9 {
		t.Fatalf("snapshot: %+v", terms)
	}
	select {
	case ev := <-events:
		if ev.Seq != 8 || ev.Type != "turn" { // seq 7 is already in the snapshot
			t.Fatalf("event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	stop()
	stop() // safe to call twice
}

// A reply that does not decode must fail, not wait for a line that never comes.
func TestHCSubscribeReportsUndecodableSnapshot(t *testing.T) {
	serve(t, `{"jsonrpc":"2.0","id":1,"result":{"seq":1,"terminals":[{"address":"t1","status":42}]}}`)
	finished := make(chan error, 1)
	go func() { _, _, _, e := HCSubscribe(); finished <- e }()
	select {
	case e := <-finished:
		if e == nil || !strings.Contains(e.Error(), "decode snapshot") {
			t.Fatalf("error: %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("HCSubscribe hung on an undecodable snapshot")
	}
}

func TestHCSubscribeReportsAbsentCore(t *testing.T) {
	t.Setenv("AMYTHEST_COLLECTOR_HC_SOCK", "/tmp/hc-not-there/s")
	// The collector tells "not running" from other failures by this errno.
	if _, _, _, e := HCSubscribe(); !errors.Is(e, syscall.ENOENT) {
		t.Fatalf("error: %v", e)
	}
}
