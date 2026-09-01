package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/base698/amythest/internal/config"
	"github.com/base698/amythest/internal/logging"
	"github.com/base698/amythest/internal/server"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "import" {
		runImport(os.Args[2:])
		return
	}
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	logging.Setup(logging.Config{
		Format: cfg.LogFormat,
		Level:  cfg.LogLevel,
		Output: cfg.LogOutput,
		Source: cfg.LogSource,
	})

	srv, err := server.New(cfg)
	if err != nil {
		slog.Error("server init", "err", err)
		os.Exit(1)
	}

	// Routes are registered unprefixed; base_url only decorates emitted
	// links. Accepting the prefixed form too means the server works whether
	// or not the fronting proxy strips the prefix.
	var handler http.Handler = srv
	if prefix := strings.TrimSuffix(cfg.BaseURL, "/"); prefix != "" {
		unprefixed := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				http.StripPrefix(prefix, unprefixed).ServeHTTP(w, r)
				return
			}
			unprefixed.ServeHTTP(w, r)
		})
	}

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Generous: share uploads up to 100MB pass through this server.
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("listening", "addr", cfg.Listen, "vault", cfg.Vault)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve", "err", err)
			stop()
		}
	}()

	go srv.RunRescanLoop(ctx)

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	// In-flight requests are done (or timed out); close the server so the
	// SQLite databases checkpoint cleanly.
	if err := srv.Close(); err != nil {
		slog.Error("close", "err", err)
	}
}
