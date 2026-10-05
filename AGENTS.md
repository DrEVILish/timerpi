# AGENTS.md

Environment notes for agents working on this repo. Modeled on CuTePi's
AGENTS.md; keep it current as machines/services change.

## Dev container

- Everything is built and tested in `/opt/timerpi` (Go 1.25 at
  /usr/local/go, `go` on PATH; `git`; host `gcc` for CGO;
  `aarch64-linux-gnu-gcc` for the Pi cross-build).
- Run the dev server with `make run` (port 8080, data dir `./data`).
  Never develop against `/var/lib/timerpi` on this box.
- `go build ./...` and `go vet ./...` must pass before you finish.
- After finishing a code item, rebuild the server and restart its
  service so the change is live, then smoke-check it:
  `make build` (writes `bin/timerpi`, the binary `timerpi.service`
  runs) → `systemctl restart timerpi` → `systemctl is-active timerpi`
  → `curl -s http://localhost/health`. Never leave a finished item
  running on a stale binary.
- Do **not** run `go mod tidy`: the dependency set is pinned by
  `tools/deps/deps.go` (deleting that file drops requirements from
  go.mod). Add new deps there first, then use them.

## Targets / services

| Role | Port | Notes |
|------|------|-------|
| Dev container | 8080 | `make run`, data in `./data` |
| Pi (target)   | 80   | systemd unit `timerpi.service`, data in `/var/lib/timerpi` |

- Fill in the Pi's addresses/service names here once hardware is assigned
- The origin guard (`routes.OriginGuard`) wraps everything and is
  **open by default**: with `allowed_hosts` empty (the default) ANY Host is
  accepted, so reverse proxies and custom domains work out of the box. A
  non-empty `allowed_hosts` switches to strict mode: only listed names plus
  always-local hosts (IP literals, localhost, dotless names, `.local`/
  `.lan`-style suffixes, the machine hostname) pass — everything else gets
  HTTP 421. To lock a proxied appliance down, list its proxy domain in
  `allowed_hosts` in that machine's `/var/lib/timerpi/config.json`, then
  `systemctl restart timerpi`. The WebSocket upgrader must call
  `routes.SameOriginRequest` in its `CheckOrigin`.

## Conventions

- Config access rides `config`'s RWMutex via getters/setters — never read
  package state directly.
- The `Host`/`Origin` guard (`routes.OriginGuard`) wraps everything; the
  WebSocket upgrader must call `routes.SameOriginRequest` in its
  `CheckOrigin`.
- Static/theme trees are resolved relative to the process working
  directory (`routes.findDir`); the systemd unit pins WorkingDirectory to
  the checkout.
