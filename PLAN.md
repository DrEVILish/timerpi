# TimerPi — Plan (living document, source of truth in this repo)

> Last rolled up 2026-10-03. Service live on 0.0.0.0:80 via `timerpi.service`.
> Status key: ✅ shipped · 🟡 code-complete, hardware-unproven · ⏳ next.

> Cue-list show timer for Raspberry Pi. One cue list for the whole day, run as
> a web app; the Pi displays it on HDMI and multiple TimerPi devices / browsers mesh over LAN.
>
> Stack standard: **Go 1.25 + gin + gorilla/websocket + sqlx +
> mattn/go-sqlite3 + skip2/go-qrcode**, server-rendered **html/template +
> htmx, use htmax.js version 4** fragments (vendored, no CDN), styling **imported as-is from
> ftl-themes** (git submodule). No client framework; vanilla JS only where
> htmx can't reach (smooth countdown rendering, fullscreen, import dialogs).
>
> **Branding: Primary Purple `#7C3AED`, Secondary Green `#22C55E`.
>
> **Identity: shows addressed by 8-char alphanumeric 4-4 codes** (`K7QP-M3XB`,
> Crockford-base32 minus I/L/O/U, typo-tolerant input). Numeric IDs are
> internal-only and never resolve as addresses.

---

## 1. Core product decisions

| Decision | Choice |
|---|---|
| Project | **TimerPi** (code lives in `/opt/timerpi`, module `timerpi`) |
| Server | Go, single binary serving HTTP + WS on **one port**, listening on **0.0.0.0** (default 80; 443 via user-managed reverse proxy) |
| Realtime | WS hub, server-authoritative state; snapshots carry `updatedAt`, newer wins |
| Client | htmx fragments for structure & mutations; **local browser clock** renders the digits so the display is perfectly smooth |
| Time rate | Operator-controllable **rate multiplier** (e.g. ×0.5–×2.0, "1 s per second"); a rate change re-anchors the countdown so on-screen time speeds up without shifting the underlying schedule |
| Storage | **SQLite** (`sqlx` + mattn/go-sqlite3, CGO) |
| Themes | ftl-themes submodule (`third_party/ftl-themes`), served at `/ftl/`, browser-local theme pick (localStorage), tokens/contract v4 |
| Identity | mDNS advertises the machine's **hostname** (`<hostname>.local`); hostname editable in the WebUI (renames host + re-announces); multiple TimerPi devices may share one LAN |
| Mesh | Each TimerPi can be **server, display, or join another server**; devices discover each other over mDNS and mesh directly. **The first TimerPi up on a network holds authority (primary).** Browsers also keep the P2P WebRTC display mesh from the previous build |
| Display | **Custom framebuffer/DRM renderer** — the Pi drives its HDMI connector directly for a rock-solid 1080p50 timer |
| Splash | **Framebuffer splash service**: logo on `/dev/fb0` until the app is ready; no extra packages |
| Target HW | Pi 4/5, 64-bit Raspberry Pi OS bookworm; no X11/Wayland needed |
| Data | SQLite is the single source of truth (shows, cue lists, messages, settings) |

## 2. Domain model (SQLite)

A show is one day's running order; each row is a timed session.

```
shows:    id, title, created_at, updated_at
cues:     id, show_id (FK), pos (stable order), label, duration_ms, kind,
          tags (VT/GFX/COM/CAM/PRES…), speaker,
          hold_ms (explicit changeover/buffer time on purpose),
          break_flag (break / changeover / host slot),
          notes, color,
          timer_kind (COUNTDOWN | COUNTSTOP | CLOCK),
          alert1_ms, alert2_ms,          -- per-cue alert thresholds
          alert_color1, alert_color2,    -- display colour states at thresholds
          end_action (HOLD | OVERTIME | BLANK),
          autocontinue
messages: id, text, color, shown_at       -- stage overlay ("PLEASE WRAP UP!")
settings: key/value (display layout, theme, hostname)
runtime:  active/previous/next cue pos, per-cue progress, rate, day-bar anchors
```

- No implicit advance beyond per-cue `autocontinue` — a cue runs out, holds, and the operator или GOs the next. Changeover/break/host rows are ordinary cues with `break_flag`, so margin is added on purpose.
- Schedule is computed from sequence + durations + holds, updating against wall-clock as the day runs: computed start, resulting end, over/under, and the running **day bar** (0:00 → total).

## 3. Architecture & repo layout (mirrors CuTePi patterns)

```
config/config.go     load/save (~/timerpi/config/config.json, env-overridable, atomic, 0600)
timerpi/             domain types, SQLite schema + migrations (db.go), engine.go (cue engine)
                     engine = pure state machine: start/pause/reset/next/go, alerts, rate;
                     snapshot + pubsub; current/next cue, progress bar, message overlay
ws/hub.go            WebSocket hub: join {show, role} → current snapshot; commands in; OOB html out
routes/index.go      pages & fragments (dashboard, cue list, display, share)
routes/api.go        cue/show CRUD, import/export, settings, WS upgrade (gin → gorilla)
routes/import.go     XLSX/XLS/CSV/JSON import; example-document download
routes/network.go    mDNS announce/detect, hostname change, device-mesh registry
routes/origin.go     allowed_hosts guard (HTTP 421 on unknown public Host — DNS-rebind protection)
routes/qr.go         QR for the share/open-display links
mesh/                browser P2P (WebRTC signaling over the Go WS) + device mesh protocol
drm/                 framebuffer/KMS display renderer (1080p50) + splash handoff
templates/*.html     dashboard, cuesheet, cueinspector, display, share, importmodal, …
public/              css/, src/ (htmx vendored), img/ (TimerPi logo), fonts/
third_party/ftl-themes/  git submodule — theme bundles & tokens
main.go              wiring
```

**WS frames (server→client)**: `state` (full snapshot), `timer` (countdown anchor + rate), `cue` (current/next change), `oob` (htmx fragment swaps), `schedule` (recomputed schedule), `message` (stage overlay), `display` (layout/theme change).
**Client→server**: `start / pause / reset / go / next / prev / jump`, `cue.*` CRUD, `import`, `settings`, `display_cfg`, `rate`, `message.show/clear`, `mesh ops`.

## 4. UI plans

- **Dashboard/operator** (any browser on the LAN, matching the reference shots): left panel — current clock with progress bar, next-up, speaker, running clock; centre — the cue list (running order) with duration, computed start, tags, per-row transport; header — show title, import/export, wrap-up/panic controls; live-connections counter; bottom — day progress bar.
- **Display page** (`/d/<show>`): fullscreen current timer, overtime state, next-up strip, message overlay, themed by ftl-themes; multiple independent displays.
- **Share**: QR + 4-4 format code link on the control page for displays/phones.

## 5. Mesh & roles (protocol)

- **Server-authoritative**: operator taps mutate state; WS fans out snapshots. Conflict rule shared across all layers: newer `updatedAt` wins.
- **Device mesh**: mDNS `_timerpi._tcp` TXT records: hostname, role (`primary` / `display` / `idle`), show id, epoch. First device up = primary; others join as members (apply snapshots locally, serve their own HDMI displays or browser pages). If the primary goes stale (>8 s without presence), the next device promotes itself with the last known snapshot.
- **Browser mesh**: as built for the previous web app — WebRTC data channels, master = earliest `joinedAt`, offline commands execute locally at the master, master pushes state back on reconnect via `POST /api/session/:code/sync`-equivalent (last-writer-wins).
- **Clock sync**: every snapshot carries `serverTime`; clients re-anchor their offset on receipt, so countdowns stay aligned across devices without NTP dependency.

## 6. Phase plan (all phases landed; notes are the delta)

0. **Scaffold** ✅ — as planned + open-by-default host guard (custom domains/reverse proxy work; strict `allowed_hosts` opt-in) + kiosk-window display links (`data-kiosk` → new window, not tab).
1. **Domain + engine** ✅ — as planned; per-cue `updatedAt` now on the wire for merge.
2. **Server + templates I** ✅ — as planned + `schedule` WS frame on every structural change + `/settings` identity page + `/setup` first-run wizard with printable QR connect sheet.
3. **Display + share** ✅ — stage + `?view=next|daysheet|clock` variants + `?view=board` **widget board** (11 draggable widgets, per-show layouts in SQLite, edit-lock toggle) + QR + alphanumeric codes + show-file JSON export/import.
4. **Device mesh** ✅ code, 🟡 two-Pi drill pending — discovery/claim/takeover/hostname-edit live; single-host drill exposed and fixed 3 same-hostname identity bugs (mdns dedupe + exclusion, seniority); live drill: member-yield, 8s takeover, seniority reclaim all PASS.
5. **Import/export** ✅ — as planned + show-file JSON bundles.
6. **Output renderer** ✅ code, 🟡 hardware run pending — `drm/` KMS 1080p50 + fbdev fallback wired behind `TIMERPI_DISPLAY`, 25 tests pass; needs `TIMERPI_HW_TEST=1` on a Pi with HDMI.
7. **Install hardening** ✅ code, 🟡 Pi run pending — units, `install-pi.sh` (idempotent, incl. 1080p50 cmdline pin), `update/backup/restore/healthcheck` scripts with gates, OPS + PI-DEPLOY runbooks, arm64 binaries building.

## 9. Offline editing (shipped post-plan, owner decision)

Operators may rewrite the running order while dark. Offline master executes cue add/edit/delete/move locally (stamped, tombstoned deletes capped at 200); reconnect merges per-cue LWW via `MergeCues` with dense renumber; operator gets a "Merged N remote change(s)" toast. Accepted caveat: concurrent reorders on both sides may interleave; server full-replace racing an offline editor duplicates loudly. Spec: `docs/OFFLINE-EDIT.md`.

## 7. Compatibility notes

- Rate multiplier: the engine anchors each running cue at its wall-clock start; displayed time = elapsed × rate; changing rate re-anchors — official schedule unaffected, display visibly speeds.
- Hostname change updates the system hostname and re-publishes mDNS, so `<newname>.local` works immediately.

## 8. Risks / open points

1. **Cross-compile**: mattn/go-sqlite3 needs CGO → build on the Pi or use an `aarch64-linux-gnu-gcc` cross toolchain in this container (recommended; the Pi needs no toolchain).
2. **Legacy XLS import**: best-effort (tealeg/xlsx); recommend users re-save as XLSX/CSV. XLSX is the supported path in v1.
3. **1080p50 mode persistence**: display mode comes from the kernel cmdline at boot; runtime modesetting via UI is a later phase.
7. **ftl `timerpi` theme**: built 2026-10-04 (see PROJECT D1) — lint/budgets green; rendered/axe gates need Playwright on a machine with a real browser; product default remains blue-future.
4. **Split-brain**: transport/message state resolves LWW by `updatedAt`; cue-list merges per-cue LWW with tombstones (see §9); concurrent reorders may interleave — documented, accepted.
5. **Hotspot/offline first-boot**: device needs network to be reachable; a first-boot fallback (e.g. USB gadget or DHCP retry UI) is out of scope for v1 unless requested.
6. **TimerPi ftl-theme spec** (`docs/TIMERPI-THEME-SPEC.md`) handed to the ftl-themes developer; UI supervised pass complete (coherence rules in `reviews/SUPERVISOR-report.md`).

## 10. What's next (owner-ordered)

1. Two-Pi LAN mesh drill on real hardware (single-host drill passed).
2. Pi hardware deploy (flash → splash → 1080p50 → mDNS → `TIMERPI_HW_TEST=1`).
3. ~~ftl `timerpi` theme build-out~~ ✅ built 2026-10-04 (PROJECT D1).

> Theme bundle note (2026-10-04): the `timerpi` ftl theme ships out of
> `third_party/ftl-themes/themes/timerpi/` (theme.css, README, icons.svg —
> TV-legibility redraws of pause/stop/skip/microphone/warning). Submodule
> commits: `f605048` (theme + dist), `ae648e4` (index row). The outer repo
> must bump the submodule pointer once its own git history exists.
