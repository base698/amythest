package messageconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func isolated(t *testing.T) string {
	t.Helper()
	for _, k := range []string{"AMYTHEST_MESSAGES_CONFIG", "AMYTHEST_MESSAGES_URL", "AMYTHEST_MESSAGES_STATE", "AMYTHEST_MESSAGES_IRC_PANE"} {
		t.Setenv(k, "")
	}
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_STATE_HOME", root)
	return filepath.Join(root, "messages.json")
}
func TestLoadPrivateConfiguration(t *testing.T) {
	path := isolated(t)
	if _, e := Load(""); e != nil {
		t.Fatal(e)
	}
	if _, e := Load(path); e == nil {
		t.Fatal("explicit missing file must fail")
	}
	os.WriteFile(path, []byte(`{"client":{"url":"http://127.0.0.1:9452","token_file":"~/private/token"},"server":{"demo":false},"connectors":{"future-provider":{"private_setting":true}}}`), 0600)
	c, e := Load(path)
	if e != nil {
		t.Fatal(e)
	}
	home, _ := os.UserHomeDir()
	if c.Client.URL != "http://127.0.0.1:9452" || c.Client.TokenFile != filepath.Join(home, "private/token") || c.Server.Listen != "127.0.0.1:8642" {
		t.Fatalf("bad defaults/expansion: %+v", c)
	}
	t.Setenv("AMYTHEST_MESSAGES_URL", "http://127.0.0.1:9453")
	t.Setenv("AMYTHEST_MESSAGES_STATE", "~/private/state")
	c, e = Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if c.Client.URL != "http://127.0.0.1:9453" || c.Server.StateDir != filepath.Join(home, "private/state") {
		t.Fatal("environment precedence lost")
	}
	for _, raw := range []string{`{"client":{"typo":true}}`, `{} {}`, `{} trailing`} {
		os.WriteFile(path, []byte(raw), 0600)
		if _, e = Load(path); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestTunnelRejectsExposedForwardsAndOptionInjection(t *testing.T) {
	isolated(t)
	good := Client{URL: "http://127.0.0.1:9452", SSH: &SSH{Host: "private-host", HTTPRemote: "127.0.0.1:9453", IRCLocal: "127.0.0.1:6452", IRCRemote: "127.0.0.1:6453", ControlPath: "/tmp/private-test-ssh"}}
	args, _, e := good.TunnelArgs()
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(args[len(args)-5:], []string{"-L", "127.0.0.1:9452:127.0.0.1:9453", "-L", "127.0.0.1:6452:127.0.0.1:6453", "private-host"}) {
		t.Fatal(args)
	}
	for _, host := range []string{"-oProxyCommand=bad", "two hosts", "host\narg"} {
		c := good
		s := *good.SSH
		c.SSH = &s
		c.SSH.Host = host
		if _, _, e := c.TunnelArgs(); e == nil {
			t.Fatal(host)
		}
	}
	for _, address := range []string{"0.0.0.0:9452", "example.com:9452", "127.0.0.1:0", "127.0.0.1:70000"} {
		c := good
		c.URL = "http://" + address
		if _, _, e := c.TunnelArgs(); e == nil {
			t.Fatal(address)
		}
	}
	c := good
	c.URL += "/path"
	if _, _, e = c.TunnelArgs(); e == nil {
		t.Fatal("accepted path")
	}
	c = good
	s := *good.SSH
	c.SSH = &s
	c.SSH.ControlPath = strings.Repeat("x", 101)
	if _, _, e = c.TunnelArgs(); e == nil {
		t.Fatal("accepted oversized socket path")
	}
}
