// Package messageconfig keeps installation-specific messaging settings outside
// the repository. Tokens stay in credential files, never in this configuration.
package messageconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Client     Client                     `json:"client"`
	Server     Server                     `json:"server"`
	Connectors map[string]json.RawMessage `json:"connectors,omitempty"`
	Path       string                     `json:"-"`
}
type Client struct {
	URL       string `json:"url"`
	StateDir  string `json:"state_dir"`
	TokenFile string `json:"token_file"`
	IRCClient string `json:"irc_client,omitempty"`
	IRCPane   string `json:"irc_pane,omitempty"`
	SSH       *SSH   `json:"ssh,omitempty"`
}
type Server struct {
	StateDir string `json:"state_dir"`
	Listen   string `json:"listen"`
	IRC      string `json:"irc"`
	Demo     bool   `json:"demo"`
}
type SSH struct {
	Host        string `json:"host"`
	HTTPRemote  string `json:"http_remote"`
	IRCLocal    string `json:"irc_local,omitempty"`
	IRCRemote   string `json:"irc_remote,omitempty"`
	ControlPath string `json:"control_path,omitempty"`
}

func Expand(path string) string {
	if path == "~" {
		h, _ := os.UserHomeDir()
		return h
	}
	if strings.HasPrefix(path, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, strings.TrimPrefix(path, "~/"))
	}
	return path
}
func DefaultPath() string {
	if p := os.Getenv("AMYTHEST_MESSAGES_CONFIG"); p != "" {
		return Expand(p)
	}
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		h, _ := os.UserHomeDir()
		root = filepath.Join(h, ".config")
	}
	return filepath.Join(root, "amythest", "messages.json")
}
func StateDir() string {
	if p := os.Getenv("AMYTHEST_MESSAGES_STATE"); p != "" {
		return Expand(p)
	}
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		h, _ := os.UserHomeDir()
		root = filepath.Join(h, ".local", "state")
	}
	return filepath.Join(root, "amythest-messages-poc")
}
func Load(path string) (Config, error) {
	explicit := path != "" || os.Getenv("AMYTHEST_MESSAGES_CONFIG") != ""
	if path == "" {
		path = DefaultPath()
	}
	path = Expand(path)
	c := Config{Path: path, Client: Client{URL: "http://127.0.0.1:8642", StateDir: StateDir()}, Server: Server{StateDir: StateDir(), Listen: "127.0.0.1:8642", IRC: "127.0.0.1:6669"}}
	data, e := os.ReadFile(path)
	if e != nil && (!errors.Is(e, os.ErrNotExist) || explicit) {
		return c, e
	}
	if e == nil {
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if e = dec.Decode(&c); e != nil {
			return c, fmt.Errorf("messaging config: %w", e)
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return c, fmt.Errorf("messaging config must contain one JSON object")
		}
	}
	c.Client.StateDir = Expand(c.Client.StateDir)
	c.Client.TokenFile = Expand(c.Client.TokenFile)
	c.Server.StateDir = Expand(c.Server.StateDir)
	if p := os.Getenv("AMYTHEST_MESSAGES_STATE"); p != "" {
		c.Client.StateDir = Expand(p)
		c.Server.StateDir = Expand(p)
	}
	if p := os.Getenv("AMYTHEST_MESSAGES_URL"); p != "" {
		c.Client.URL = p
	}
	if p := os.Getenv("AMYTHEST_MESSAGES_IRC_PANE"); p != "" {
		c.Client.IRCPane = p
	}
	return c, nil
}
func loopbackAddress(addr string) error {
	host, port, e := net.SplitHostPort(addr)
	if e != nil {
		return e
	}
	ip := net.ParseIP(host)
	n, e := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || e != nil || n < 1 || n > 65535 {
		return fmt.Errorf("SSH forwarding addresses must use numeric loopback IPs and valid ports")
	}
	return nil
}

// TunnelArgs returns argv, never a shell command. Host and forwarding fields
// cannot become SSH options or shell fragments.
func (c Client) TunnelArgs() ([]string, string, error) {
	s := c.SSH
	if s == nil || s.Host == "" {
		return nil, "", nil
	}
	if strings.HasPrefix(s.Host, "-") || strings.ContainsAny(s.Host, " \t\r\n\x00") {
		return nil, "", fmt.Errorf("invalid SSH host")
	}
	u, e := url.Parse(c.URL)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, "", fmt.Errorf("SSH tunnel needs a loopback HTTP URL without a path")
	}
	if e = loopbackAddress(u.Host); e != nil {
		return nil, "", e
	}
	if e = loopbackAddress(s.HTTPRemote); e != nil {
		return nil, "", e
	}
	forwards := []string{"-L", u.Host + ":" + s.HTTPRemote}
	if s.IRCLocal != "" || s.IRCRemote != "" {
		if e = loopbackAddress(s.IRCLocal); e != nil {
			return nil, "", e
		}
		if e = loopbackAddress(s.IRCRemote); e != nil {
			return nil, "", e
		}
		forwards = append(forwards, "-L", s.IRCLocal+":"+s.IRCRemote)
	}
	control := Expand(s.ControlPath)
	if control == "" {
		h, _ := os.UserHomeDir()
		root := os.Getenv("XDG_CACHE_HOME")
		if root == "" {
			root = filepath.Join(h, ".cache")
		}
		sum := sha256.Sum256([]byte(s.Host + strings.Join(forwards, "|")))
		control = filepath.Join(root, "amythest", "ssh-"+hex.EncodeToString(sum[:8]))
	}
	if len(control) > 100 {
		return nil, "", fmt.Errorf("SSH control path is too long; configure a shorter private path")
	}
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-o", "ExitOnForwardFailure=yes", "-o", "ControlPersist=600", "-M", "-S", control, "-fN"}
	args = append(args, forwards...)
	args = append(args, s.Host)
	return args, control, nil
}
func (c Client) EnsureTunnel(ctx context.Context) error {
	args, control, e := c.TunnelArgs()
	if e != nil || len(args) == 0 {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(control), 0700); e != nil {
		return e
	}
	timeout, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if exec.CommandContext(timeout, "ssh", "-S", control, "-O", "check", c.SSH.Host).Run() == nil {
		return nil
	}
	out, e := exec.CommandContext(timeout, "ssh", args...).CombinedOutput()
	if e != nil {
		return fmt.Errorf("messaging SSH tunnel: %w: %s", e, strings.TrimSpace(string(out)))
	}
	return nil
}
