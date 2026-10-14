package amythest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/source"
)

func TestHealthClassifiesMissingCredentialsAsConfiguration(t *testing.T) {
	t.Setenv("KANBAN_USERNAME", "")
	t.Setenv("KANBAN_PASSWORD", "")
	client := apiclient.New(apiclient.Config{
		Endpoint:    "http://private-endpoint.example",
		Timeout:     time.Second,
		SessionFile: filepath.Join(t.TempDir(), "session.json"),
		EnvFile:     filepath.Join(t.TempDir(), "private-env-file"),
	})

	health := New(client).Health(context.Background())
	if health.State != source.StateMisconfigured {
		t.Fatalf("health = %+v, want missing-credentials", health)
	}
	if health.Detail != "Amythest login credentials are missing. Configure them, then retry." {
		t.Fatalf("unsafe or unclear detail: %q", health.Detail)
	}
}

func TestHealthPreservesConfiguredSuccessPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /kanban/api/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "amythest_kanban_session", Value: "session"})
		_, _ = w.Write([]byte(`{"user":"amy","csrf":"token"}`))
	})
	mux.HandleFunc("GET /kanban/api/boards", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"one"},{"name":"two"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("KANBAN_USERNAME", "user")
	t.Setenv("KANBAN_PASSWORD", "password")
	client := apiclient.New(apiclient.Config{
		Endpoint:    srv.URL,
		Timeout:     time.Second,
		SessionFile: filepath.Join(t.TempDir(), "session.json"),
	})

	health := New(client).Health(context.Background())
	if health.State != source.StateOK || health.Detail != srv.URL+" · 2 boards" {
		t.Fatalf("health = %+v, want configured success", health)
	}
}

func TestHealthClassifiesUnavailableServiceSeparately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := srv.URL
	srv.Close()
	t.Setenv("KANBAN_USERNAME", "user")
	t.Setenv("KANBAN_PASSWORD", "password")
	client := apiclient.New(apiclient.Config{
		Endpoint:    endpoint,
		Timeout:     time.Second,
		SessionFile: filepath.Join(t.TempDir(), "session.json"),
	})

	health := New(client).Health(context.Background())
	if health.State != source.StateUnavailable {
		t.Fatalf("health = %+v, want unavailable", health)
	}
	if health.Detail != "Amythest could not be reached. Check the endpoint and service, then retry." {
		t.Fatalf("unsafe or unclear detail: %q", health.Detail)
	}
}
