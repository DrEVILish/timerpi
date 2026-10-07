# AGENTS.md — rules for working on this repo

For every contributor, human or AI. Keep it short and current.

**Always in English**: replies, commits, docs, code comments.

## 1. Read before you build

1. [docs/PRODUCT.md](docs/PRODUCT.md) says **what** to build. If a task conflicts with it, stop and ask the owner. Do not quietly redesign.
2. [STATUS.md](STATUS.md) says what is open. Pick work from it and cite its IDs (B1, N3, …) in commits.
3. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) and [PROTOCOL.md](PROTOCOL.md) say how the code works today.
4. `docs/archive/` is history. Never treat it as a spec.

## 2. Doc discipline (why the reset happened)

The previous docs became append-only logs: three trackers and many handoff notes, each partly stale. Avoid that:

- **One fact, one place.**
  - Requirements → PRODUCT.
  - Status and open work → STATUS.
  - Design → ARCHITECTURE.
  - Wire contract → PROTOCOL.
- **Update in the same commit** as the code change. A change to a route or frame touches PROTOCOL; a change to a feature's status touches STATUS.
- **Edit in place; don't append batch logs.** History belongs in `git log`, not in docs.
- **Never cite a file that doesn't exist.** No new `NOTES-*.md` handoff files; put lasting facts in the four docs above.
- Owner decisions go in PRODUCT §7 with a date.

## 3. Build and test

- Go 1.25, CGO (gcc) for SQLite. For the Pi: `aarch64-linux-gnu-gcc` + `make build-arm64`.
- `make run`: dev server on :8080 with data in `./data`. Never point development at `/var/lib/timerpi`.
- Before finishing: `go build ./... && go vet ./... && go test ./...` must pass. Every feature or bug fix lands with a test. UI behaviour that only a browser shows gets a test in `tools/browser-tests/` (`make browser-test`).
- If the machine runs `timerpi.service` from this checkout: `make build` → `systemctl restart timerpi` → `curl -s localhost/health`. Never leave a stale binary serving.
- **Do not run `go mod tidy`.** Dependencies are pinned by `tools/deps/deps.go`; add new deps there first.
- Changing JS/CSS: bump the asset version with `tools/bump-assets.sh`, so screens don't run cached code (see ARCHITECTURE §12).

## 4. Code conventions

- Config is read and written only through `config` getters/setters (RWMutex).
- `routes.OriginGuard` wraps everything. The WS upgrader's `CheckOrigin` must call `routes.SameOriginRequest`.
- Static and theme trees resolve relative to the working directory (`routes.FindDir`). The systemd unit pins `WorkingDirectory`.
- Share codes are the only public address. Numeric IDs never resolve.
- User text reaches the DOM through `textContent` only, never `innerHTML`.
- htmx: **htmx 4 only, vendored in `public/src/`**. Never load from a CDN, and never add a second htmx version.
- Use ftl-themes component classes and tokens for all UI. `public/css/timerpi.css` is for layout only. Never use the browser's `confirm`/`prompt`; use the `dialog.js` helpers.
- **Pull components from ftl-themes; don't build fresh ones.** Before writing any UI component (modal, menu, popover, toast, table, tabs, badge, form control…), check `third_party/ftl-themes` (CONTRACT.md, `components-*.html`) and use its markup and classes. Build our own only when ftl-themes has nothing, and then file the gap as a `reviews/upstream-issues/` proposal.
- **Dialogs are ftl-themes modals. Do not change this.** Every dialog is `<dialog class="modal">` (size with `.modal-sm`/`-lg`/`-xl`), with a `.modal-header` holding the title and a `.btn-close`, and a `.modal-footer` with Cancel (`.btn-secondary`) before the primary action. Buttons that only close carry `data-close` (one handler in `ui.js` closes the dialog). No app-owned dialog chrome: no custom backdrop, border, padding or button row in our CSS; size and spacing come from ftl tokens. This keeps the control surface the same everywhere and across every theme.
- Display screens carry no operator chrome. Display screens always animate; only the audience page honours `prefers-reduced-motion`.

## 5. Source control and upstreams

- Push only to `DrEVILish/timerpi`. **Always commit to `main`, test, then push** (owner rule): `go build ./... && go vet ./... && go test ./...` plus the browser tests first. No feature branches; never force-push.
- **ftl-themes** (`third_party/ftl-themes`, a separate clone, git-ignored): don't push to it. Record problems as `reviews/upstream-issues/<topic>.md` with repro and proposed fix, and file them upstream only when the owner says so. ftl-themes is to become a submodule pinned to an upstream commit (STATUS C2).
- The same rule applies to **CuTePi** and any other dependency repo.

## 6. Environment reference

| Role | Port | Data |
|---|---|---|
| Dev | 8080 (`make run`) | `./data` |
| Appliance | 80 (`timerpi.service`) | `/var/lib/timerpi` |

Env: `TIMERPI_DATA_DIR`, `TIMERPI_HTTP_PORT` (legacy `CAPACITIMER_HTTP_PORT` still honoured), `TIMERPI_DISPLAY` / `TIMERPI_FBDEV` / `TIMERPI_DISPLAY_SHOW` (native renderer), `TIMERPI_HW_TEST` (hardware tests), `TP_LOAD=1` (audience load harness).

Reverse proxy and custom domains work by default (open Host guard). To lock an appliance to its domain, set `allowed_hosts` in `/var/lib/timerpi/config.json` and restart.
