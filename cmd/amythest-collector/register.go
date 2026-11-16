package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/base698/amythest/internal/collector/store"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func register(args []string) error {
	args = append([]string{"login"}, args...)
	if len(args) == 0 || args[0] != "login" {
		return errors.New("usage: amy auth login --server https://host/notes --name agent --preset reader|collector --output credential.json")
	}
	fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
	server := fs.String("server", "", "deployed server URL, including its path prefix")
	name := fs.String("name", "", "name shown during approval")
	preset := fs.String("preset", "collector", "collector")
	output := fs.String("output", filepath.Join(store.Dir(), "credential.json"), "private credential file; must not already exist")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	u, err := url.Parse(*server)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("provide the deployed server URL")
	}
	host := u.Hostname()
	local := host == "localhost" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return errors.New("server must use HTTPS (except loopback development)")
	}
	if strings.TrimSpace(*name) == "" || (*preset != "collector") {
		return errors.New("provide a name and reader or collector preset")
	}
	if *output == "" {
		return errors.New("provide --output for the mode-0600 credential file")
	}
	if _, err := os.Lstat(*output); !os.IsNotExist(err) {
		return errors.New("credential output already exists or cannot be checked; choose a new path")
	}
	base := strings.TrimRight(*server, "/")
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(path string, in, out any) (int, error) {
		b, _ := json.Marshal(in)
		req, err := http.NewRequest("POST", base+path, bytes.NewReader(b))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer res.Body.Close()
		err = json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(out)
		return res.StatusCode, err
	}
	var reg struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URL        string `json:"verification_uri"`
		Expires    int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	status, err := post("/api/auth/registrations", map[string]string{"label": *name, "kind": *preset}, &reg)
	if err != nil || status != 200 {
		return fmt.Errorf("registration failed (HTTP %d): %v", status, err)
	}
	verify, err := url.Parse(reg.URL)
	if err != nil || verify.Scheme != u.Scheme || verify.Host != u.Host || reg.DeviceCode == "" || reg.Expires <= 0 || reg.Expires > 600 {
		return errors.New("invalid registration response")
	}
	fmt.Printf("Open %s\nCompare code %s, then approve %s.\n", reg.URL, reg.UserCode, *name)
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	if cmd := exec.Command(opener, reg.URL); cmd.Start() == nil {
		go cmd.Wait()
	}
	interval := time.Duration(reg.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(reg.Expires) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		var result struct {
			Token      string          `json:"token"`
			Credential json.RawMessage `json:"credential"`
			Error      string          `json:"error"`
		}
		status, err = post("/api/auth/registrations/token", map[string]string{"device_code": reg.DeviceCode}, &result)
		if err != nil {
			return fmt.Errorf("token exchange failed; check Access settings before registering again: %w", err)
		}
		if status == 200 && result.Token != "" {
			b, _ := json.MarshalIndent(map[string]any{"server": base, "token": result.Token, "credential": result.Credential}, "", "  ")
			if err = os.MkdirAll(filepath.Dir(*output), 0700); err != nil {
				return err
			}
			f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return fmt.Errorf("credential issued but could not be saved; revoke it in Access settings: %w", err)
			}
			_, err = f.Write(b)
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				return fmt.Errorf("credential save failed; revoke it in Access settings: %w", err)
			}
			fmt.Printf("Connected. Credential saved to %s (readable only by you).\n", *output)
			return nil
		}
		switch result.Error {
		case "pending":
		case "slow_down":
			interval += 5 * time.Second
		default:
			return fmt.Errorf("registration %s", result.Error)
		}
	}
	return errors.New("registration expired; run the command again")
}
