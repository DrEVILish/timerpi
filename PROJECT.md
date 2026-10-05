# PROJECT.md — TimerPi feature tracker & remaining-work ladder

> The single source of truth for WHAT is required, WHAT is done, and WHAT is
> open. PLAN.md records architecture and history; this file records truth on
> the ground. Update it in the same commit that changes a feature.
>
> **Ordering: sections run from EASIEST to HARDEST remaining work** (§1 Done
> summary first, then the ladder §2: quick wins → show-floor features →
> plumbing → appliance/hardware → external).
>
> Status: ✅ done+verified · 🟡 code-complete, unproven (needs hardware/live
> check) · ⏳ planned · ❌ not started · ⚠️ gap (feature missing entirely)
>
> Last updated: 2026-10-05 (Tier E1–E6 shipped: clone, notice widget, blackout, action log, auto-start, presets. Code-side complete — remaining: bench execution).

---

## 1. Done — summary of the shipped base (all ✅ unless noted)

| Area | What exists |
|---|---|
| Platform | Go single binary, HTTP+WS one port on 0.0.0.0:80, custom-domain/reverse-proxy safe, graceful shutdown, config.json (0600, env overrides), amd64+arm64 builds, ops scripts (update/backup/restore/healthcheck), TimerPi rebrand |
| Domain | Shows with unique 8-char alphanumeric 4-4 share codes (code-only URLs), cue rows (label/duration/kind/tags/speaker/hold/alerts 1+2 w/ colors/end actions HOLD-OVERTIME-BLANK/autocontinue/COUNTDOWN-COUNTSTOP-CLOCK/notes/color), schedule engine (computed starts/ends, holds+breaks, over/under **computed but not shown**, day bar), multiple shows, XLSX/CSV/JSON import + examples, show-file JSON bundles, stage messages, per-cue `updatedAt` merge fields |
| Realtime | WS hub on `/ws` (same port), server-authoritative + `serverTime` re-anchor, local-clock smooth digits, rate ×0.5–×2 re-anchor, `schedule` frame on structural change, multi-display fanout |
| Operator UI | Dashboard (cue table, transport, ±30s/±1m, GO/prev/next/pause/reset, jump, keyboard map + dedupe, honest row states, self-scrolling active row), first-run `/setup` wizard, QR connect sheet, `/settings` identity page incl. degraded-mesh stub, import dropzone, day-bar needle honesty, kiosk-window display links |
| Display | Stage TV page + `?view=next|daysheet|clock` + `?view=board` widget board (11 widgets, drag/resize, per-show layouts, edit lock, stage/lobby presets), urgency chip, alarm-scale digits, fullscreen hint, message overlay |
| Resilience | Browser P2P WebRTC mesh (no STUN/TURN), offline master transport + **full offline editing** with tombstone merge + "Merged N" toast, device mesh (mDNS `_timerpi._tcp`, first-up primary, 8 s takeover, (host,port) identity), single-host drill passed |
| Pi-layer code | mDNS announce, framebuffer splash + ready-file handshake, DRM/fbdev 1080p50 renderer wired behind `TIMERPI_DISPLAY`, `install-pi.sh`, systemd units (incl. timers, staged) |
| Quality | 10 Go packages green, edge-test package (engine/mesh/mdns/merge/schedule/code adversarial suites), brand discipline (purple/green-logo-only), UX waves U1+U2 |

---

## 2. Remaining work — EASY → HARD ladder

### Tier A — quick wins (hours each)

| # | Task | New/gap? | Status | Where it lands |
|---|---|---|---|---|
| A1 | **Wire the operator password** — `config.AuthPassword` exists and is settable but nothing checks it; add login gate (cookie/basic) on mutating endpoints + WS join | ✅ 2026-10-03 | `routes/auth.go` (AuthGate + /login + /api/login + /api/auth/password), ws join gate (controls needs token, display/mesh open), command refusal for non-controls roles, 5 tests + live drill (302/401/cookie/display-open/password lifecycle) |
| A2 | **Per-row over/under display** — `schedule.go` already computes `DeltaMS`; surface "on plan / +0:35 / −1:02" | ✅ 2026-10-03 | `views.CurrentCue.DeltaMS/DeltaFmt` + `#tp-delta` chip in frag-current; `timerpi.js paintDelta()` keeps it live per frame (COUNTDOWN only); `FmtDurSigned` unit-tested |
| A3 | **Cue search/filter** (long days) — client-side filter box on the cue table | ✅ 2026-10-03 | dashboard header `/`-shortcut filter; hide-rows + `n/m` count + no-match note; applied inside `renderRows` so it survives oob swaps (browser-verified), Esc clears |
| A4 | **Duplicate cue button** — server `cueDup` exists (ws/commands.go:156); no UI affordance | ✅ 2026-10-04 | Row copy button (`icon-copy`) between pencil + bin in `frag-cuelist` → `data-cmd="cueDup"`; undo inverse wired in `undo.js` (`cueDel` of the copy at pos+1, B3 ledger); drag/reorder-friendly. Tests: `TestCueDupCommand` (ws: copy lands after source, oob fanout, bad-pos err) + `TestDuplicateTemplateShips`; live curl drill on :8123 — buttons ship per row with correct pos/aria |
| A5 | **Tag chips + per-cue colour editing UI** — columns exist in DB/snapshot; no operator editing affordance beyond duration/label | ✅ (complete via B1, 2026-10-04) | Chips render per row (`frag-cuelist`); editing lives in the B1 inspector modal (`#tp-insp-tags` comma-separated + `#tp-insp-color` row accent hex — both commit through ONE cueEdit), plus label/speaker/duration inline editing; column display stays the row-bound one |
| A6 | **"Start day now"** — engine `SetDayStart`/`dayStartTS` exists; no operator button to anchor the day clock at show open (day-bar needle stays hidden otherwise) | ✅ 2026-10-03 | "Day starts now" button rides the day-bar header (outside the oob swap); sends `settings{ts:now}`; re-anchor asks confirm; browser drill: needle unhides, dayStartTS lands |
| A7 | **Per-show notes / day memo** — free-text field shown on daysheet/stage footer | ✅ 2026-10-03 | `shows.notes` column + `POST /api/shows/:ident/notes` (show-gated, 4 000-char cap); dashboard panel autosaves debounced; renders on the printable daysheet; rides snapshots |
| A8 | **Homepage privacy rework** — show codes/titles are credentials; nothing public may list them | ✅ 2026-10-03 | homepage lists nothing (Create/Join/Recent-from-localStorage only); `GET /api/shows` strips code+title unless the operator password is set (AuthGate); `/frag/shows` removed |
| A9 | **Per-show extra password** — privacy tier 2 on top of the share code | ✅ 2026-10-03 | `routes/showauth.go`: passphrase on the show, lock pages for `/c/` **and** `/d/`, gated show-rest (snapshot/sync/import/messages/boards), WS join token `showToken` + `tp_show_<code>` cookie; self-gating passphrase API; browser drill + tests green |
| A10 | **Rate UX**: slider double-click resets ×1.0; inline editable ×1.xx readout (dblclick; touch = double-tap) | ✅ 2026-10-03 | `initRateExtras` + `beginRateEdit` (×0.5–×2.0 clamp); paint-loop guard `_rateEditing`; input-time value capture fixed a paint-stomp that snapped drags back to ×1.0 |
| A11 | **Inline table editing** — all operator-owned cue cells editable in row | ✅ 2026-10-03 | label/speaker/duration inline (dblclick/two-tap → `cueEdit`); computed cells non-editable; oob swaps deferred while editing |

### Tier B — medium (about a day each)

| # | Task | Notes | Status |
|---|---|---|---|
| B1 | **Cue inspector modal** (per-cue full editing: alerts 1/2 + colours, end action, timer kind, autocontinue, tags, speaker, hold, notes) — server fields all exist; WS `cueEdit` needs field expansion + modal | ✅ code-complete 2026-10-03 | `<dialog id="tp-inspector">` opened by the row pencil (`data-insp`); fills from snapshot, ONE `cueEdit` commit (server accepts every field); hex/parse validation client-side mirrors server; contract test `TestInspectorTemplateShips`. Browser live-open blocked only by the sandbox middlebox (see §6 note); all layers below it green |
| B2 | **Drag-and-drop reorder** — the `#` cell is the drag handle (pointer-drag, mouse + touch) | ✅ 2026-10-03 | WS `cueMove` extended with the `{pos,to}` form (full-slot splice, upstream of the existing `DB.MoveCue`; dir form untouched); REST twin `POST /api/shows/:code/moveto` (show-gated, clamps per `MoveCue`); pointer-drag with lift threshold + live insertion line + drop-slot math (= slot you become); listeners DOCUMENT-delegated (oob swaps replace tbody — bound handlers die with old rows, caught in the browser drill); tests `TestCueMoveTargetSlot` + `TestRemoteMoveToB2`; drag + undo integration verified browser-side (drag cue4→slot2, UNDO restored exactly) |
| B3 | **Undo stack** — client-side last-op undo | ✅ code-complete 2026-10-03 | `public/src/undo.js` (pure `createUndo(bus)`: capture/observe/perform, 10-deep): DELETE re-creates + cueMove-up chain (server-side order-verified `TestUndoRestoreChain`), EDIT re-sends the pre-values of exactly the touched keys, ADD waits for its row via `observe()` then queues an add-inverse. Wired into `sendCommand` + Ctrl/Cmd+Z + the header UNDO button; works online and while offline-master (same send path). Transport is deliberately not undoable; interleave caveat documented |
| B4 | **Scheduled day start + cue starts** — per-show "day begins at HH:MM" automation | ✅ 2026-10-03 | `shows.day_start` column + `DayStartTSFrom` parser; `POST /api/shows/:code/daystart {hhmm}` persists+anchors in one call (single call → needle turns wall-clock immediately, survives reboots: `Engines.Get` auto-anchors freshly constructed engines for scheduled shows — reboot-shaped `TestEngineAutoDayStart`); garbage-guarded; WS `settings{dayStart}` too; UI rides the day-bar header, prefilled from truth |
| B5 | **Webhooks / Companion / Stream Deck endpoints** | ✅ 2026-10-03 | `POST /api/shows/:code/cmd/:action` — engine-verb aliases (go/start/pause/resume/reset/next/prev/jump/rate/daystart), BodyLog = WS-command args JSON; rate clamped server-side (`timerpi.ClampRate` ×0.5–×2.0); show-gated + operator-auth-gated + OriginGuard CSRF semantics intact; snapshot returned; `TestRemoteCmdB5` |
| B7 | **Server-side theme defaults** | ✅ 2026-10-03 (owner decision: default = **blue-future**, 2026-10-03) | `config.DefaultTheme` falls back to `blue-future` (was xbmc — wrong for this product); charset-sanitised setter, `GET/POST /api/theme` with installed-bundle list from `third_party/ftl-themes/dist`; `/settings` picker ("blue-future (product default)" copy); ALL roots (base/display/display_board/setup/settings + Go micro-pages) now render `html[data-theme]`, default `<link>` bundle AND `<meta name="tp-default-theme">` server-side — plus the real bug this flushed out: the old bootstrap compared against a hardcoded xbmc link (never loaded the default bundle for any other theme; also starved icons), fixed by server-rendering the default link; JS `initTheme` now always applies the ACTIVE theme's icon sprite (`applyIconTheme`) at boot + after oob swaps (templates ship sprite *names*; JS retargets `<use>` hrefs). Tests: `TestDefaultThemeB7`; browser-verified: attr=`blue-future`, bundle link=blue-future.css, sprites=blue-future.svg |

### Tier C — plumbing-integration (multi-day)

| # | Task | Status |
|---|---|---|
| C1 | **Two-Pi LAN mesh drill on real hardware** (single-host drill passed; 3 same-hostname bugs fixed) | ❌ needs bench — runbook: `docs/HW-DRILLS.md` §1 (claim → join → fast-restart → takeover → reunion → name-conflict, ~10 min) |
| C2 | **Live two-browser offline drill** (P2P works in-code; never exercised end-to-end with real tabs) | 🟡 2026-10-04 — master-side loop PROVEN live (2 tabs, 1 sandbox browser): server death → deterministic master holds → offline add/delete/WRAP-UP repaint the table+messages INSTANTLY (new offline mirror renderer) → server back → auto-push merges (no manual kick: server adopted Doors-deleted/6-cue ledger + 2 messages) → peer adopts merged snapshot. **Two real bugs found & fixed in-mesh (rev v35):** ① split-brain — `_reElect` counted only OPEN channels while both tabs' channels were handshaking → BOTH self-crowned; now counts every non-dead pc with a known joinedAt. ② master flip — client re-stamped `joinedAt=Date.now()` on every reconnect → original master lost "earliest join" to a never-dropped peer and didn't push; now the first-join stamp is reused (hub honors client stamps, verified). **Still pending:** the P2P data-channel hop itself — this sandbox browser gathers ZERO ICE candidates (offer/answer SDPs exchange fine through the hub relay, channels stay `new`), so the mesh-cmd replication tab↔tab needs a real machine re-run (`docs/HW-DRILLS.md` §3: `__tpmesh` master agreement + `open` channels + dark replication + merge toast); toast-region content retrieved elsewhere: repetition deduped (4 s same-text) |
| C3 | **DRM renderer hardware run** (`TIMERPI_HW_TEST=1`, 1080p50 page-flip verify) | ❌ needs Pi — runbook: `docs/HW-DRILLS.md` §2 (free card → TestHW RUN → drm_info → live tick) |
| C4 | **Pi deploy acceptance** (flash → splash → boot → mDNS → service units enabled) + enable cron/healthcheck timers | ❌ needs Pi — flash/install: `docs/PI-DEPLOY.md` §§1–3 (ready-file contract §6 now closed: main writes/removes `/run/timerpi/ready`); bench drills: `docs/HW-DRILLS.md` |

### Tier D — external / waiting

| # | Item | Depends on |
|---|---|---|
| D1 | `timerpi` theme built into ftl-themes (spec: `docs/TIMERPI-THEME-SPEC.md`) | ✅ 2026-10-04 (this repo = the ftl developer): `themes/timerpi/` (theme.css + README + icons.svg), dist bundles + manifest + merged icon sprite built & committed in the submodule (3 commits). `check.sh` 0 failures / 0 warnings for the theme; `theme-ready.sh`: build/lint/budgets PASS (bundle 117 KiB, theme gz 4.3 KiB); rendered-core-regress/v5-audit/a11y gates need Playwright — not runnable in this container (PROJECT §5.12 sandbox note); picker can offer `timerpi` + `high-contrast` (shellAware dark). Product default stays **blue-future** (owner decision B7); submodule pointer bump still owed to the outer repo once git lands there |
| D2 | Optional: Wi-Fi hotspot/first-boot AP fallback (PLAN §8.5, out-of-scope unless requested) | owner decision + hardware |

### Tier E — product completeness (owner-approved 2026-10-05, all ✅)

| # | Task | Status |
|---|---|---|
| E1 | **Clone show** — duplicate the day (fresh code, same cues/notes/day-start, no passphrase, stopped runtime) + share-panel form | ✅ `DB.CloneShow` + `POST /:ident/clone` (201 `{code,title,cueCount,control,display}`); tests `TestCloneShowE1`, `TestCloneShowDefaultsE1`, `TestCloneFormShipsE1` |
| E2 | **Free-text notice board widget** — 12th tile type for static lobby text (welcome/sponsors/Wi-Fi) | ✅ registry + `frag-b-notice` (escaped) + palette + settings textarea (256 cap mirrors server) + `.b-notice` style + factory Welcome tile; tests `TestNoticeWidgetRegistered`, `TestNoticeWidgetE2` |
| E3 | **Global display blank** — one-tap blackout of every display to STANDBY | ✅ `shows.blanked` → snapshot + `ShowRef`; WS `blank`/`unblank` + REST; all `/d/` surfaces (CSS-flag overlay) + HDMI (`drm_provider`); dashboard `#tp-blank` toggle (confirm to blank); tests `TestBlankRoundTripE3`, `TestBlankVerbsE3` |
| E4 | **Operator action log** — who did what (multi-operator + post-show review) | ✅ `actions` table (200/show cap, fail-safe writes); WS logs `role:peer` on success only (`mutDone` store-ops / `engDone` engine verbs — no double Notify); REST logs `api`; `GET /:ident/actions` + dashboard `#actions-panel` + `frag-actions` oob; tests `TestActionLogTailAndCap`, `TestActionLogE4`, `TestActionLogRestE4` |
| E5 | **Per-cue wall-clock auto-start** — fire timed cues when idle, never yank live ones | ✅ `cues.start_at` HH:MM (validated everywhere incl. merge); Tick fires first due cue past playhead iff `!Running && !Paused`; WS `cueEdit{startAt}`; inspector time input (past-time confirm); `⏰ HH:MM` row chips; 6 engine tests + merge/WS/surface tests |
| E6 | **Quick-add duration presets** — `+1m/+5m/+10m` chips fill m:ss (no auto-submit) | ✅ dashboard chips + delegated filler; test `TestQuickAddPresetsShipE6` |

### Tier F — display operations (owner-requested 2026-10-05)

| # | Task | Status |
|---|---|---|
| F1 | **Screens panel on the dashboard** — see all connected screens/displays from the main browser; per-screen layout/theme customization or match-all button | ✅ `screens` registry + identity (`?screen=` URL override → per-window sessionStorage → generated `Screen-XXXX`; multiple windows never group); join carries `screen` (upsert + pushed assignment: `{t:"display",theme}` / `{t:"screen-board"}` on join, peers frames carry screen); `routes/screens.go` config/match/rename/forget with targeted `SendToScreen` pushes; dashboard `#screens-panel` live-refresh (`{t:"screens"}` + peers churn). Tests: `TestScreenJoinAndPushF1`, `TestScreensFlowF1` |
| F2 | **Display presets** — named bundles (theme + per-screen assignments) surviving server + browser restarts; export/import as JSON | ✅ `display_presets` table (SQLite = survives restarts; panel always reads truth from server = survives browser refresh); save-snapshot / apply / export (download JSON, `kind:"timerpi-display-preset"` v1) / import endpoints; `TestPresetsRoundTripF2` |
| F3 | **Viewport-unit display CSS** — text/positioning/containers in vw/vh on ALL screens so 720p/1080p/4K render identically | ✅ `html:has(body.tp-display){font-size:1.5vh}` scales every display rem with the output; display surfaces already vh/vw-tuned; 1–3px hairlines intentionally device-px; operator pages untouched |
| F4 | **Client error reporting** — `window.onerror`/`unhandledrejection` batch-post to the server; server log store for debugging (no UI surface, deliberate) | ✅ `theme.js initClientLog()` on all pages (throttled, batched); `client_errors` table (200/show) + journal lines + JSON tail; tests `TestClientLogF4`, `TestClientErrorStoreF4` |
| F5 | **Quick-add duration rule** — bare `30` = 30 min, `1:30` = 1 h 30 m, `30s` = 30 s (quick-add + inspector; imports keep their seconds rule) | ✅ `views.ParseDuration` + JS `parseDur` rewritten in parity; labels/placeholders/toasts updated; E6 chips now bare minutes; `TestParseDurationTable` rewritten |
| F6 | **GO restarts held cues** — timer at 0 + GO re-runs the full duration (explicit Start still refuses) | ✅ `goLocked` clears spent elapsed before firing; `TestGoRestartsHeldCue` (+ `TestEngineStartHeldCueIgnored` intact) |
| F7 | **Oob-death hardening** — blank toggle, rate cluster, day-start controls all moved to document delegation (direct listeners died on fragment re-renders) | ✅ `#tp-blank` + `#tp-day-start` + day-start form + rate input/dblclick/tap all delegated; dead `initDayStart`/`initBlankToggle` bindings removed |
| F8 | **Message-form ghost sends** — clicks inside `data-cmd` forms bubbled to the click delegation, firing empty-arg ghost commands (the "addMsg needs text" error + bogus undo inverses) | ✅ delegation skips `form[data-cmd]` subtree; custom form gains checked "Show now" (`value="true"`); `TestMessageFormShowsNow`, `TestAddMsgFormDataShapes` |
| F9 | **Drag-and-drop rebuild** — down-drags landed a slot low, end-drop line misplaced, dead CSS lock, no autoscroll, no mid-drag swap guard, post-drag click leak | ✅ slot compensation (`to = slot>from ? slot-1 : slot`), end line below last row, `body.tp-drag-body` lock, wrap autoscroll, detached-row abort, click suppression; `TestMoveCueDownContract` |
| F10 | **Reconnect after outage** (owner bug report) — displays/controls never rejoined when the network returned; peer channels stayed dead so tabs stopped talking | ✅ mesh.js: 6 s handshake watchdog (a dead network parks WebSockets in CONNECTING with no events — the retry loop stalled forever), `_wsDown` detach+dedupe (onerror+onclose double-penalty), `online`/`visibilitychange` instant rejoin, `_pruneStaleConnections()` on re-join (half-dead pcs stuck `new` blocked re-negotiation). Regression harness: `tools/mesh-reconnect-smoke.mjs` (28 checks, stubbed browser globals) |
| F11 | **Instant BLANK** (owner decision) — the blackout confirm dialog removed; press = dark, press again = live | ✅ `#tp-blank` delegated handler sends immediately, no `confirm()` |

---

## 3. Done evidence by subsystem (detail for the ledger)

### Platform & service (all ✅)

| Requirement | Status | Evidence |
|---|---|---|
| Single Go binary, HTTP+WS one port, 0.0.0.0:80 | ✅ | systemd `timerpi`; reverse-proxy/custom-domain safe (open-by-default host guard, strict `allowed_hosts` opt-in) |
| Settings/config (config.json, env overrides, atomic 0600) | ✅ | config/ tests |
| Graceful shutdown (SIGTERM, ready-file removal) | ✅ | journal evidence |
| Build amd64 + arm64 (CGO cross) | ✅ | Makefile; aarch64 ELF verified |
| Ops suite: update/backup/restore/healthcheck scripts + docs | ✅ | `docs/OPS.md`, scripts verified incl. rollback |
| Rebrand to **TimerPi**; `/opt/capacitimer` untouched for reference | ✅ | branding sweep + logo assets |
| Role override (primary/member/auto) + `/api/network` identity APIs | ✅ | mesh device tests + `/settings` UI live |
| systemd Pi install script (`install-pi.sh`, 1080p50 cmdline, hostname option) | 🟡 | sandbox-idempotent; never run on real Pi (=C4) |

### Show domain (all ✅)

| Requirement | Status | Evidence |
|---|---|---|
| 8-char alphanumeric 4-4 share codes, code-only public URLs | ✅ | `timerpi/gen.go` + `code_test.go`; numeric 404s everywhere |
| Cue domain incl. timer kinds, per-cue alerts, end actions, autocontinue | ✅ | engine + domain tests |
| Schedule computation (+ `schedule` WS frame) | ✅ | schedule tests; over/under display = A2 |
| Multiple shows / homepage / create-delete | ✅ | routes tests |
| Import XLSX/CSV/JSON (+legacy xls) + example docs | ✅ | importdocs 18 tests |
| Show-file JSON export/import whole-day | ✅ | `routes/setup.go` |
| Stage messages overlay | ✅ | hub + REST + UI |
| Per-cue `updatedAt` merge fields | ✅ | merge/sync tests |

### Realtime & operator (all ✅)

| Requirement | Status | Evidence |
|---|---|---|
| WS hub same-port, server-authoritative, `serverTime` re-anchor | ✅ | hub tests |
| Smooth local-clock digits + rate ×0.5–×2 re-anchor | ✅ | continuity tests |
| Dashboard/home/setup surfaces + QR sheet + kiosk display links | ✅ | pages 200; U1/U2 passes |
| Settings/device identity page + `/api/network` | ✅ | 200 5KB live; degraded stub themed |
| Transport semantics incl. boundary refusals | ✅ | engine edge tests (empty/single/zero/dup-rate/held-Start/concurrent) |

### Resilience (P2P + offline all ✅ code; drill pending)

| Requirement | Status | Evidence |
|---|---|---|
| Browser P2P mesh (WebRTC, no STUN/TURN) | ✅ code / 🟡 live | real 2-tab drill = C2 |
| Offline master transport + **full offline editing** (tombstone merge + toast) | ✅ | `docs/OFFLINE-EDIT.md`; tombstone edges (fresh tombstone beats stale row; stale loses to server edit) |
| Server death + recovery, master pushes back | ✅ / 🟡 drill | sync LWW + merge; single-host drill passed |
| Device mesh (first-up primary, takeover, (host,port) identity) | ✅ / 🟡 live | device tests; 2-Pi drill = C1 |
| Displays keep ticking sans server; zero-cross off-grid | ✅ | mesh.js master tick |

### Display outputs (all ✅ but theme)

| Requirement | Status | Evidence |
|---|---|---|
| Stage + variants (`next`/`daysheet`/`clock`) + tap-fullscreen + urgency chip | ✅ | routes/display.go |
| Widget board: 11 widgets, drag/resize, layouts, presets, edit-lock | ✅ | boards tests |
| Shareability QR + hostname.local on every surface | ✅ | share + join cards |
| Brand: purple/green-logo-only/orange-free | ✅ | sweeps + U2 harness |
| Theme: `timerpi` ftl-themes bundle | ⏳ | spec delivered; awaiting developer (D1) |
| Default ftl theme for the appliance | ✅ | blue-future is the product default on all surfaces (owner decision 2026-10-03); the ftl contract itself (data-theme scope, dist bundles, icon bundles) was already correct — the bug was our defaults plumbing |

### Pi appliance

| Requirement | Status | Evidence |
|---|---|---|
| mDNS announce at boot | ✅ container | zeroconf round-trip |
| Boot splash + ready-file handshake | 🟡 | splash tests; Pi boot = C4 |
| DRM/fbdev 1080p50 renderer (25 tests + provider integration) | 🟡 | `TIMERPI_HW_TEST=1` = C3 |
| Two-Pi drill / actual deploy | ❌ | runbook `docs/PI-DEPLOY.md` = C1/C4 |
| cron/healthcheck timers enabled | 🟡 | unit files staged, enable on Pi = C4 |

---

## 4. UX waves (Agent U1 operator + Agent U2 display, 2026-10-03)

| Requirement | Status | Evidence |
|---|---|---|
| Operator: one-tap WRAP UP (was form-blocked), quick ±30s/±1m against latest snapshot, `?` keyboard help + same-action dedupe | ✅ | reviews/UX1-operator-report.md |
| Operator: honest cue table (per-row Pause/Reset removed, two-tap armed delete), running row self-scroll, sticky header + 44px touch floors on phones | ✅ | UX1 |
| Operator: day-bar needle hidden until day anchored (was pinned 100% unanchored), import dropzone, degraded-state voice (`Server down · show keeps running from here`), appliance-voiced copy | ✅ | UX1 |
| TV: OVERTIME/WRAP-UP urgency chip (count-up, near-black ink on danger), alarm-scale digits (36vh alert / 34vh rest ≥ spec floors) | ✅ | reviews/UX2-display-report.md |
| TV: fullscreen hint corner chip (no flicker, survives theme swaps), widget 1080p vh floors, crisp progress fill | ✅ | UX2 |
| TV: widget colour discipline (authored colour left edge), edit-overlap nudge (revert + 1 s shake; drag≠gears), status-line declutter | ✅ | UX2 + 17-assertion engine.js harness |

---

## 5. Timer-gap analysis — obvious features NOT implemented NOR previously tracked

*(This section is the answer to "what's missing"; each item maps to the ladder above.)*

1. **~~Operator auth~~** (`config.AuthPassword` dead config field — no gate exists) → **done** (A1, 2026-10-03).
2. **~~Over/under on the cue table~~** vs scheduled end (`DeltaMS` computed engine-side, never surfaced) → **done** (A2: live chip in the current-cue panel).
3. **~~Cue search/filter~~** for long cue lists → **done** (A3, `/` shortcut + `/`-hinted box, oob-proof).
4. **~~Duplicate-cue UI~~** (server op existed, no button) → **done** (A4, 2026-10-04: row copy button + undo wiring).
5. **Rich cue editor** (alerts/colour/kind/notes/tags per row) → **done** (B1 inspector modal, 2026-10-03; label/speaker/duration remain inline-editable too).
6. **Drag-and-drop reorder** (endpoints exist for ±1 only) → B2.
7. **Undo after mistakes** → B3.
8. **~~Remote-automation hooks~~** (Companion/Stream Deck/webhooks) → **done** (B5 cmd aliases).
9. **~~Audible alerts~~** → **dropped from scope** (owner decision 2026-10-03): alert surfaces stay visual.
10. **~~Server-side defaults for theme~~** → **done** (B7 default theme).
11. **~~Scheduled day-start automation~~** → **done** (B4: persists + anchors, reboots keep it).
12. ⚠️ Sandbox verification note: the middlebox between the session's desktop browser and this appliance serves STALE bytes for any static path it has already fetched (ignores `Cache-Control: no-cache` and ?v=; even brand-new paths grabbed mid-edit copies). Production LANs don't have this layer — the shipped mitigations (no-cache + path-rev'd asset names via `tools/bump-assets.sh`, directory-rev rewriting in `routes.registerStatic`) are correct and shell-verifiable. Anything not browser-provable here is Go-test-proved instead.
13. ✅ 2026-10-04 (C2 drill): **offline mutations were invisible** — row/list creation rode only server oob fragments, so an offline master's add/delete/WRAP-UP applied to `mesh.snap` but the cue table + messages panel stayed frozen (badge 0, no rows). Fixed with a client-side mirror renderer in `timerpi.js` (`rebuildRowsIfNeeded`/`renderMessagesList`): heals the DOM from snap whenever the row set diverges — server oob remains owner when online (signature guard makes both paths converge, no fight). Drill-verified: offline add/delete/two-tap/undo/WRAP-UP all repaint instantly; restart auto-push then merges.
14. ✅ 2026-10-04 (C2 drill): **two mesh election bugs fixed** — ① split-brain: `_reElect` counted only open channels; while channels were `connecting`, every tab crowned ITSELF (each became offline master after a server death). ② master flip on reconnect: the client re-stamped `joinedAt=Date.now()` per reconnect, so the original master rejoined with a LATER stamp than a never-dropped peer, lost the election, and skipped its offline push (C2-drill first-run blocked the finale until fixed; hub already honors client stamps, `ws/session.go:131`). Both fixed in `mesh.js`, rev v35.
15. ✅ 2026-10-04 (operator UX): "lots of red popups" — danger toasts come from the client (`mesh onLog`→`toast(danger)` + link transitions), NOT the server journal (verified against server.log during the drill window). Identical toasts within 4 s are now swallowed; the idle current-cue rail got its layout polish (idle paints BLANK via LED-dark convention — JS `''` + `min-height` rule; zero meter parked via `#tp-now.tp-idle-cue`). Layout follow-ups SHIPPED: the day bar now rides UNDER the running order (inside the panel, outside the #cuelist oob swap) with one-line [label][m:ss][Add] + [time][Save] clusters (app-side width override against core's block `.input`); live-verified + needle marching.
16. ✅ 2026-10-04 (C2 drill): **"Day starts now" never fanned out** — the WS `settings{ts}` path re-anchored via `ApplyCmd("daystart")` without `eng.Notify()`, so the START/END columns + day-bar scale stayed stale (00:00:00 offsets) until the next unrelated structural change, while the REST twin always notifies (`routes/api.go`). Fixed both settings{ts} and settings{dayStart} branches; regression `TestDayStartFanout` (state frame + cuelist/daybar oob after the anchor); browser-verified: columns + scale repaint within 2 s of the confirm.
17. ✅ 2026-10-04 (plumbing): **board edit auth shipped** — `?edit=1` on `/d/` used to be the only gate (NOTES-board §5.4): once an operator password is set, the editing chrome ships only to credential-carrying requests (`routes/boards.go boardEditAllowed`, cookie/Basic); strangers get an honest read-only board (same tiles, no chrome); the board REST was already behind A1's gate. Tests `TestBoardEditAuthGate` (both sides) + live curl drill green (0 toolbar hits unauthed / 1 authed + 44 tile hazards). Same pass: A5 closed (inspector covers tags + row colour), CONTRACT-UI §4 documents the offline render parity.
18. ✅ 2026-10-04 (re-verified live): **board screens run ONE mesh session** — after the guard, booting a board page produced exactly ONE hub join (`ws: bd202f72… joined show 1 as display (1 connected)` — was one per MODULE before), with all 11 tiles server-rendered and live (clock 5:00 armed, LINK LIVE, theme blue-future); operator-pushed theme swaps repaint there via the shared `theme.js` leaf module (rev v41).
19. ✅ 2026-10-04 (board follow-ups complete): **mobile tile editor shipped** (NOTES-board §5.3 — the open board follow-ups are now all closed). Under 760px, while editing, `#b-editor` lists every tile with X/Y/W/H steppers (same `clampTile`/`applyGeometry`/`scheduleSave`/overlap-revert contracts as the pointer grid; ⚙ reuses the settings form, 🗑 the reloadEditing re-lock). CSS keeps it off every other tier. Live drill: 11 rows build on Edit; a blocked move reverts with "Blocked: tiles overlap"; a free move repaints and commits (`Saved …`). Rev v42.
20. ✅ 2026-10-04 (hygiene): ledger self-audit — one drifted word in §18 and a mangled merge of these history lines repaired; grep sweep for the known drift tokens found no surviving corruption in code or docs.

---

## 6. Verification discipline

- `go build ./...`, `go test ./...` green on every feature commit (currently ALL green, 10 packages).
- Live curl smoke on :80 after every deploy (health + c/d pages + settings).
- Every feature lands with tests in the same change.
- PROJECT.md updated in the same commit (this file is the tracker).

---

## 7. History Log (resolutions)

1. ✅ 2026-10-03: "tombstone-only master resurrect" was a mis-stamped edge test, not a code bug. With a realistic sequence (stale row ← master's offline tombstone newer) the merge drops the row and leaves the show empty (`TestSyncEdgeTombstoneOnlyMaster` + corollary `TestSyncEdgeStaleTombstoneLosesToServerEdit` green). Edge-coverage wave added: empty/single-cue shows, zero-length cue, broken-rate contract, held-Start refusal, concurrent transport, schedule empty/all-break/huge, code adversarial, tombstone cap, same-host mDNS dedupe, junk TXT peers.
2. ✅ 2026-10-03: `/settings` degraded state returns an honest themed stub ("MESH UNAVAILABLE" + what still works); `/frag/network` serves an inline note rather than silent no-op. **Also recovered:** `templates/settings.html` + `fragments/frag-network.html` had been lost with a cancelled agent session — recreated and verified (200, 5KB; frag renders table). Dashboard idle shows now pre-render the day's first cue as "Next" (client-mirror parity).
3. ✅ 2026-10-03: **Current-cue rail was unreadable** — `.tp-clock-xl` (up to 13vw, a stage-TV scale) ran inside the narrow operator rail with no nowrap, shattering "00:00" into wrapped lines and pushing state/meter/label below the fold. The rail now has its own scale (`.tp-now .tp-clock` → clamp(3.2rem, 11vh, 7rem) + nowrap, phone variant 17vw) and the Rate row stopped wrapping. Fixed the root while here: **no cache policy at all** — `Cache-Control: no-cache` now goes on every response (HTML revalidates; static mounts re-set it), **plus** an `?v=tpav…` asset-REV token on timerpi.css/timerpi.js/board.js and the ES-import specifiers (the header only revalidates entries served after it existed; already-cached operators need the URL change). Browser-verified: clock one line at ~75px, whole stack in one viewport, fresh `?v=tpav6`.
4. 📌 Runbook rule added with #3: future CSS/JS changes bump `?v=` in `templates/*.html` AND `public/src/*.js` import specifiers together (comment lives in base.html).
5. ✅ 2026-10-05: **edge-case test wave #2 landed, suite back to green** — 21 new regression tests (`timerpi/edge_extra_test.go`: MoveCue-from-beyond-last, CreateCue clamp-to-append, DuplicateCue tail, migrate re-run, past-zero crash recovery, armed-cue Start{}, rate-0 schedule, tombstone Deleted=false, duplicate-incoming LWW, unknown-ID tombstone collapse, ParseNumericID guards, ClampRate; `ws/frame_edges_test.go`: unparseable frame, unknown frame type, settings title/whitespace/bogus, addMsg bad color; `routes/edge_routes_test.go`: notes 4000/4001 cap, QR clamp dimensions, import-example fmt, Origin null/Referer, ShowUnlockedJoin unit, passphrase clear). Three failures were test bugs, not code bugs (notes snapshot path, QR byte-floor vs 1-char compressibility, missing unlock cookie on clear — the 401 is the intended self-gate, now locked in as a test). One real find: duplicate UNKNOWN-positive-ID rows became two adds instead of LWW-collapsing (`timerpi/merge.go` dedupes all incoming IDs first now); the rewrite also keeps first-seen order so same-Pos adds stay deterministic across Go map iteration.
6. ✅ 2026-10-05: **bench runbooks shipped** (`docs/HW-DRILLS.md`): one-shot C1 (claim → member-join → fast-restart-no-flap → 8 s takeover → seniority reunion → name-conflict, waits derived from the mesh tunables), C3 (free card → `TestHW` must RUN → drm_info → live tick), C2-P2P (real machine only: `__tpmesh` master agreement + `open` channels + server-dark tab↔tab replication + merge toast), plus a result-log template. Same pass closed the stale `PI-DEPLOY.md` §6 "open contract": `main.go` has written/removed `/run/timerpi/ready` around the live listener for a while — verified, not aspirational.
7. ✅ 2026-10-05: **edge-case wave #3, +22 tests, suite green (228→250)**. One REAL bug found by probe: `ReorderCues` with an unknown ID let it consume a position (hole at pos 1, rows at 2..N) despite its "unknown IDs are ignored" contract — fixed in `timerpi/db.go` (filter to known IDs before numbering). New: `timerpi/edge_wave3_test.go` (Validate enums, Normalize defaults, Reorder-unknown, Replace-empty), `views/edge_wave3_test.go` (ParseDuration table, FmtAgo), `config/setters_test.go` (Title/DeviceName), `routes/edge_wave3_test.go` (rate default/floor, moveto guards, replace validation, REST messages, boardBid, 49/0-widget layouts, explicit import kind, append salvage, setup wizard, showfile version), `ws/wave3_test.go` (unknown command + route hint, show/hide/clear cycle, signal errors), `boards/edge_wave3_test.go` (corrupt-row factory fallback). Two observations left open (not changed): `ShowMessage` on a deleted id is a silent no-op success (UPDATE matches zero rows); the `signal data must be an object` branch is unreachable (frame JSON already parsed). Skipped as too heavy/flaky: the 512-session cap (would need 513 live joins).
8. ✅ 2026-10-05: **Tier E1–E6 shipped** (owner-approved set). E1 clone (`DB.CloneShow` — passphrase never inherited); E2 notice widget (12th type, factory Welcome tile); E3 blackout (`shows.blanked`, WS+REST, all `/d/` + HDMI); E4 action log (`actions` table ≤200/show, `mutDone`/`engDone` success-only logging, `frag-actions` oob); E5 auto-start (`cues.start_at`, Tick fires iff `!Running && !Paused`, past-time confirm); E6 presets. Two fixes found along the way: `ReorderCues` hole already closed in wave #3; WS `mutDone` initially double-broadcast engine verbs (`runMutation` already fans out — shifted one-state-per-command readers; split into `mutDone` store-ops + `engDone` engine-verbs). Asset rev v43 (JS/CSS changed). Suite green throughout.
9. ✅ 2026-10-05: **robustness batch + Tier F progress**. Fixed: blank toggle + rate cluster + day-start controls moved to document delegation (oob-death class); message-form ghost sends (guard + checked Show-now + string-shape tests); GO restarts held cues (`TestGoRestartsHeldCue`); drag rebuild (down-slot compensation, end line, CSS lock, autoscroll, detach abort, click suppression; `TestMoveCueDownContract`); rate slider works again. New rule: bare `30` = 30 min / `1:30` = 1 h 30 m / `30s` = 30 s (Go+JS parity, imports unchanged). Removed the Recent Actions section (table/endpoint/logging kept). Shipped F3 (viewport-root identity) + F4 (client error reports). Asset rev v44. Remaining from the batch: F1 screens panel + F2 presets (tracked, unstarted).
10. ✅ 2026-10-05: **F1/F2 screens + presets shipped; F10/F11 reconnect + instant blank.** Screen identity (URL `?screen=` → localStorage → generated) rides the WS join; the registry drives per-screen theme/board assignment with targeted pushes + join-time adoption push, a live dashboard panel (presence via peers/screens frames), rename/forget/match-all, and server-persisted presets with JSON export/import. The outage bug: stuck-CONNECTING sockets froze the retry loop (fixed with a 6 s handshake watchdog + online/visibility instant rejoin + `_wsDown` dedupe) and half-dead peer pc entries blocked channel re-negotiation after the server returned (`_pruneStaleConnections` on re-join) — `tools/mesh-reconnect-smoke.mjs` locks the whole sequence. Asset rev v45.
11. ✅ 2026-10-05: **review-fix batch (full-repo review, 25 findings — all closed).** Security: renderRecent XSS (textContent rebuild), client-log journal hygiene + kind bounds, AuthGate now exempts the two TV surfaces (QR image, client-log) while handlers keep their own gating, global body ceilings (8 MiB/32 MiB). Concurrency: `broadcast` serialized per show (was a process-fatal data race: engine.notify runs inline on every mutating goroutine vs sh.sessions/sh.sigs), `fanout` map-guard, `UpsertScreen` out of the hub lock, `DefaultTheme` takes the RWMutex. Correctness: auto-start day-in-progress guard (clone surprise-start), goLocked rollback on failed GO, identity RenameScreen no-op, board assignment validation + clear-on-delete, honest match counts, clearMsgs partial fanout, ParseDuration parity (negatives/`1::30` rejected both engines), rune-safe ClipUTF8 truncation. Client: offline-mirror verbs (blank/unblank/settings), heartbeat offline-only + accepted-push accounting + ?screen= URL sync (mesh.js), undo no-capture loop fix, screens select restore vs accidental clears, notice 256-BYTE clamp, geom-listener teardown, client-log keepalive. Regressions: `TestConcurrentBroadcastNoRace` (green under -race), `tools/mesh-reconnect-smoke.mjs` 38 checks, `routes/review_fixes_test.go`, `timerpi/review_fixes_test.go`. Suite green incl -race on all touched packages; rev v46; service restarted and healthy.
12. ✅ 2026-10-05: **screens gallery + waiting room + session delete.** `GET /screens/:ident` gallery page: live preview card per screen (layout map + current values via enriched screens payload, 4 s poll) with theme/board Apply, Match-all, Forget, and an Edit-layout maximised modal (iframe board editor in `preview=1` mode — edits live without registering a phantom screen; tile ⯇/⯈ align buttons added). Orphaned displays (badshow) raise a full-screen TimerPi logo + "waiting for connection…" (`waiting.js`, `body[data-waiting]`), register in `waiting_screens` (AuthGate-exempt TV paths) and can be CAPTURED by any operator gallery — claim consumed exactly once, navigation keeps `?screen=` identity. Session delete: `DELETE /api/shows/:c/sessions/:peer` → `Hub.KickSession` delivers a "session deleted" err frame before close; mesh.js stands down until reload. ftl component vocabulary used for every new element; all DOM text via textContent. Tests: TestGalleryPageF, TestWaitingRoomFlow, TestSessionKickFlow; suite green, rev v47, service restarted + healthy.
13. ✅ 2026-10-05: **screen identity made per-window** (owner report: 3 display windows showed grouped as one "LIVE ×3"). Identity was localStorage-scoped — shared by every tab on the origin. Now: `?screen=` URL param wins (persistent override), else **sessionStorage** (reload keeps the window's name; every fresh window gets its own `Screen-XXXX`), rename writes sessionStorage + URL. Three windows now render three cards with LIVE ×1 each. Legacy localStorage-named rows simply age out (Forget them once). Tests: `TestSeparateWindowsSeparateRows` + smoke "second window gets a separate name"; rev v48.
14. ✅ 2026-10-05: **13-theme UI/UX critic audit + fix round.** Strict critic agent scored every surface (dashboard/stage/board) per the 0–9.9 theme-fidelity rubric: nothing reached 8, and the blockers were OUR bugs, not the bundles (all 43 pass required tokens + 100% component-vocabulary coverage). LOCAL fixes shipped: board ReferenceError `fmtRemaining` (missing engine import — froze every board clock on every theme), board-page `clockUI.start()` TypeError (boot-time crash since C2), per-letter tag chips (mirror iterated the tags STRING — split to match `views.tagsOf`), BLANK clipped out of the rail (`.transport` wrap), stage status muted-on-bg (2.09:1 on win95 — now `--text` + join-card gutter), HELD brown-on-glass (display clocks use the bright fill token), toast over the app-bar Theme label, inspector field collapse (7rem floors), tile frames on true-black themes (accent-blended border hardening). UPSTREAM (drafted in `reviews/upstream-issues/`, no gh CLI here): windows95 `.badge` white-on-white inside `.panel-header` (missing from the theme's caption-exception reset); `--border` == background on lcars/death-star. Scores after local fixes: aqua/ios-skeuomorphic ~8-class pending board re-score, windows95 capped by its upstream badge bug. Browser re-verification pending (desktop browser dropped mid-session); static + Node verification done, rev v49, suite green.
15. ✅ 2026-10-05: **ftl-themes upstream update merged; both audit issues confirmed fixed and local hardening retired.** Rebased the timerpi theme commits onto the new release (19c1e02, 46 themes incl. cockpit/desktop/live additions), rebuilt all dist bundles with the hardened tooling (shared version hash → every bundle regenerated), check.sh 0 failures. Verified in the served CSS: windows95 `.badge` joins the panel-header caption reset; lcars/death-star `--border` now visible on true-black. Removed the `.b-widget` accent-blend workaround (public/css/timerpi.css), rev v50, service restarted healthy. Note: the timerpi bundle now warns at 144.6 KiB/144 budget — driven by upstream's new core components (37 other bundles warn likewise), not theme content. ftl clone: main ahead of origin by 4 (theme + dist rebuild) — kept LOCAL per AGENTS.md rule: we are not ftl-themes' primary developer; upstream contributions go through `reviews/upstream-issues/`, never pushes.
16. ✅ 2026-10-05: **PLAN major v2 "Rooms" + phase 0 stitch.** Plan §11 (multi-room event platform: slot layouts, role ladder, audience capacity lane, CuTePi OSC destination; §11.8 cross-check addendum incl. audience-only reduced-motion; §11.9 full-fidelity show bundle) + reduced-motion scoping + CuTePi live-endpoint cue request filed upstream (DrEVILish/CuTePi#4). Phase 0: the parallel session's uncommitted WIP (zones, polls/audience surface, oscbridge) stitched — see PLAN §11.3 for the full list (OSC wire rewrite, route mounting, main wiring, passphrase gate on the audience lane, audience.html stub v51, 3 new test files). Two pre-existing -race flakes fixed en route (mdns collectEntries snapshot, drm test window). Suite 12 pkgs green incl. -race; deployed + smoke-tested live (/a/<code> renders, audience read shape, passphrase gate).
17. ✅ 2026-10-05: **Phase 1 — `/d/` no-code display + home "Open as display".** Route `/d/` (static sibling of `/d/:ident`; bare `/d` 301s) renders the READY overlay; `waiting.js runWaiting()` starts the orphaned-display register/poll/hop loop without a mesh; identity via the mesh's own rules so multiple `/d/` tabs stay separate waiting rows. Home page: new panel with a plain `target="_blank"` anchor. Tests `routes/dready_test.go` (page/redirect/button/capture-loop incl. consume-once); rev v52; suite 12 pkgs green + `-race`; deployed, live capture loop smoke-tested.
18. ✅ 2026-10-05: **Test-gap audit of the stitched WIP — 6 uncovered surfaces closed.** New: `routes/oscapi_test.go` (settings round-trip incl. bad-port refusal, full UDP integration — saved settings arm the listener, a wire packet drives engine GO/blank, disable silences it; ask-burst 429 per-peer isolation; poll list/delete/QR incl. PNG signature), `timerpi/engine_hook_test.go` (OnStart fires on go/start only — never pause/resume/reset; the CuTePi lockstep contract), `ws/polls_frame_test.go` (pollsFn merge into every fanout frame + nil strip), `routes/zone_test.go` (zone label round-trip, walk-in board grouping, empty state). Root-cause fixes found while testing: `templates/zone.html` was MISSING (WIP rendered a blank page live — authored now), zone route rendered by filename not define name (same bug audience.html had), `DeletePoll` of an unknown id was a silent 200 → now `ErrNoRows` → 404 like the waiting room. Suite 12 pkgs green plain + `-race`; deployed; live zone-page smoke.
19. ✅ 2026-10-05: **Phase 2 — display templates/slots (PLAN §11.2).** 5 new board widget types (poll/qa/wordcloud/map/joinqr): registry defs, server fragments, board.js renderers, gear settings; Rooms templates event/room/main/dsm in Go (overlap-tested) + GET /api/board-templates + chrome buttons; assets blob table + upload/serve/delete routes (sniffed mime, 4 MiB, public GET exempt in AuthGate) + zone-map pointer on the walk-in page; PollView.children for approved words; per-tile animation enum (fade/slide/pop/none + animMS) with entrance+exit keyframes, displays-only by owner rule; rev v53. Design fixes: child approval accumulates (SetPollState single-focus now top-level only), ActivePoll focus top-level only. Tests: boards/templates_test.go, timerpi/polls_children_test.go, routes/assets_test.go (10 cases); suite 12 pkgs green plain + -race; deployed + live smoke (templates endpoint, asset upload→serve, zone map render).
