# NOTES — timerpi (Agent B: domain, DB, engine)

Read this if you're wiring main.go / ws hub / routes. Everything below is also
visible in the package godoc; this file lists deviations from PROTOCOL.md and
the exact integration seams.

## API surface (what other agents call)

```go
// main.go — open the DB, build the engine registry
db, err := timerpi.Open("<data dir>/timerpi.db")     // WAL, busy_timeout, FKs on
engines := timerpi.NewEngines(db)                    // one *Engine per show, lazy
defer db.Close()

// ws hub (ws/hub.go) — subscribe + command + snapshots
eng, err := engines.Get(showID)                      // returns existing or builds it
cancel := eng.Subscribe(func(s timerpi.Snapshot) { /* fan out to ws conns */ })
defer cancel()                                       // on client disconnect

snap, err := eng.Snapshot()                          // full JSON state (join reply, REST GET /api/shows/:id)
tf, err := eng.Timer()                               // small frame: {remainingMS, overtime, alertState, alertColor, rate, paused, blank, timerKind}
err = eng.ApplyCmd(action, args map[string]any)      // "start|pause|resume|reset|next|prev|go|jump|rate|daystart"
                                                     // returns ErrUnknownCmd for anything else
err = eng.Tick(nowMS)                                // server-wide ticker (~250 ms): zero crossings, alert edges,
                                                     // auto-advance; emits ONLY on state change (digits are client-ticked)
err = eng.Notify()                                   // call after cue/message CRUD (DB ops outside the engine) to re-broadcast
engines.Drop(showID)                                 // after DeleteShow; the cached engine must go too
```

WS command mapping (`{"t":"cmd","action":…,"args":{…}}`): cue/message CRUD —
`cueAdd/cueEdit/cueDel/cueMove/cueDup/cueReorder` → `db.CreateCue /
UpdateCue / DeleteCue / MoveCue / DuplicateCue / ReorderCues`;
`addMsg` → `db.CreateMessage`; `showMsg` / `hideMsg` → `db.ShowMessage /
ClearMessage`; `rate`/`jump`/`start`/… → `eng.ApplyCmd`. After any DB-side
mutation call `eng.Notify()` so other clients re-sync.

Schedule for templates / day bar: `timerpi.ComputeSchedule(cues, dayStartTS, rate)`
and `timerpi.ComputeScheduleRuntime(cues, runtime, now)` (adds actual/delta per
running row).

HTTP method/other types as needed: `timerpi.DisplayedRemaining(cue, runtime, now)`
is the exported client-parity countdown formula (`remaining = duration - ((now -
anchor)*rate + pausedElapsed)`); JS must mirror it.

## Deviations from PROTOCOL.md (deliberate)

1. **Message.ShowID added** — messages are per-show in the snapshot; the
   protocol's Message lacked the column. Wire JSON is unchanged shape
   (`showId` is `omitempty`).
2. **`panic` on Resume**: protocol command list has no explicit resume; hub
   can map client "pause" (while paused → resume) or use `ApplyCmd("resume")`.
3. **RuntimeView extras in the snapshot's runtime object**: `prevPos`,
   `endAction`, `alertState`, `alertColor`, `overtime`, `blank`,
   `remainingMS` are additive; all protocol keys present with exact tags.
   `TimerFrame` likewise carries `alertColor/paused/blank/timerKind` beyond
   the spec'd 4 fields.
4. **Alert saturation**: once remaining passes a threshold (or zero), the
   alert state stays at the deepest configured state (stops flickering at
   the boundary). Documented in `DisplayedRemaining`.
5. **AutoContinue**: advances only on zero crossing of an
   `AutoContinue`+`EndAction=Hold` cue, per the v1 note in PROTOCOL (never on
   OVERTIME/BLANK; the exhausted cue's HoldMS is schedule-only).
6. **`ComputeSchedule` ignores rate for math** — kept as a parameter per the
   protocol signature and stored in the output, but schedule math is pure
   wall-clock (rate re-anchors the countdown display only). It DOES hold for
   break rows too (HoldsMS includes a break row's own HoldMS).
7. **Cue.UpdatedAt / Message.UpdatedAt / Runtime.UpdatedAt rows** exist on
   the structs with `json:"-"` (schema bookkeeping for the snapshot's
   `updatedAt` max) — not part of the wire objects.
8. **Go/Danger errors**: `Go()` at the end of the list returns `ErrNoNextCue`
   (normal operator condition, not a fault); unknown commands error with
   `ErrUnknownCmd`; malformed `rate <= 0` errors with `ErrBadRate`.
9. **Engine crash recovery**: if the persisted runtime was already past zero
   when the process restarts, `NewEngine` marks the crossing done so the
   first Tick doesn't replay a fresh zero-crossing event.

## Build seams for later agents

- Engine package has **zero imports** beyond stdlib; IO is injected via
  `EngineDeps{Now, Show, Cues, Messages, Stamp, Load, Save}`.
  `db.EngineDeps(showID)` is the production wiring.
- `Engines` (registry) is safe for concurrent Get/Drop; engines serialize on
  one mutex; subscriber callbacks are invoked outside the lock and must not
  block (hub: buffered channel + goroutine).
- The snapshot comes out fully built (protocol shape) — the hub decides frame
  types (`state`, plus `timer` from `Timer()` if it wants a lean frame).

## Test/verify status (2026-10-03)

- `go test ./timerpi/ -count=1` — PASS (31 tests: engine table tests with
  mocked clock, rate re-anchor continuity 0.5/1.0/2.0, zero-crossing per
  EndAction, alert transitions, auto-advance, transport, pubsub, formula
  parity, schedule math with breaks/holds, DB CRUD + reorder invariants)
- `gofmt -l timerpi/` — clean.
- Build scope: `go build ./timerpi/...` green regardless of other agents'
  in-flight changes.

## Colour sweep note (product-owner correction, 2026-10-02)

Ack now: no #BF4E34 or any orange appears in `/opt/timerpi/timerpi/**`
(verified by grep). Alert colors `#ffaa00` / `#ff4444` come from the
PROTOCOL's example and are campaign threshold colours, not brand UI accents —
left as-is.
