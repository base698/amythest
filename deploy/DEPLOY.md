# Deploying Amythest

Amythest is a single binary. A typical small-server deployment:

1. Build: `make assets && GOOS=linux GOARCH=arm64 go build -o amythest ./cmd/amythest`
   (pick your GOARCH; pure Go, no cgo).
2. Copy the binary and `deploy/amythest.service` to the host
   (`./deploy/deploy.sh user@host` stages both).
3. Create `~/.config/amythest/env` from `deploy/amythest.env.example`
   (kanban credentials, MCP token — omit either to disable that feature).
4. `systemctl --user daemon-reload && systemctl --user enable --now amythest`.
5. Put a TLS-terminating proxy in front (Tailscale Serve, Caddy, nginx).
   Amythest binds loopback by default. If it serves under a path prefix,
   pass `-base-url /notes`; the server accepts both stripped and unstripped
   request paths, so the proxy may forward either form. Proxies should send
   `X-Forwarded-Prefix` (the kanban SPA uses it to point its asset URLs at
   the public mount). Serve everything from one prefix — do not add a
   second top-level route into the same instance.

Monitoring: `/metrics` exposes Prometheus series (request histograms,
vault gauges, rescan health). A reasonable alert is resident memory
sustained above ~300MB — normal steady state is well under 100MB with
`GOMEMLIMIT=150MiB` set (the provided unit sets it).

Everything under the data dir is derived from the vault and safe to
delete; the first start after deletion just reindexes.
