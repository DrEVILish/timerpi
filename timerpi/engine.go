package timerpi

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Engine is the per-show cue state machine (PROTOCOL §Engine). It owns the
// Runtime, persists every mutation through an injected Save func and
// broadcasts the new Snapshot to subscribers (the ws hub fans them out).
// The engine imports nothing but stdlib: it is IO-free apart from the
// injected deps, so it stays unit-testable with a fake clock.
type Engine struct {
	mu   sync.Mutex
	deps EngineDeps
	rt   Runtime

	subs    map[int64]func(Snapshot) // change callbacks (pubsub), id → fn
	nextSub int64

	// Tick bookkeeping (not persisted): a fresh start resets both.
	lastAlert   int
	lastCrossed bool
	rtDirty     bool // reconcileLocked repaired rt; Tick must commit it
	// lastRollCheck throttles the new-day check in Tick (once a minute).
	lastRollCheck int64

	// activeID is the cue id behind rt.ActivePos (0 = unknown). Cue CRUD
	// renumbers positions behind the engine's back; cuesLocked re-finds
	// the active cue by id so the playhead follows it (BUGLOG RC2).
	activeID int64

	// onFire is the outbound media hook (OSCbridge wiring, proposal #8):
	// invoked after a mutation leaves a cue RUNNING, with the running pos.
	onFire func(pos int64)
}

// EngineDeps inject the edges. Nil entries get defaults; see DB.EngineDeps
// for the production wiring.
type EngineDeps struct {
	Now      func() int64          // wall clock, epoch ms
	Show     func() (Show, error)  // current show (for snapshots)
	Cues     func() ([]Cue, error) // current cue list, ordered by Pos
	Messages func() ([]Message, error)
	Stamp    func() int64                  // updatedAt max stamp (0 → use Now)
	Load     func() (Runtime, bool, error) // initial runtime
	Save     func(Runtime) error           // persist runtime after mutations
}

// Engine errors. ErrNoNextCue is a normal GO at the end of the list; the hub
// surfaces it to the operator without treating it as a fault.
var (
	ErrNoShow     = errors.New("timerpi: show not found")
	ErrNoCues     = errors.New("timerpi: show has no cues")
	ErrUnknownPos = errors.New("timerpi: no cue at that position")
	// ErrNoTimeLeft: Start against a cue already held at zero (≤0 remaining
	// with a HOLD end action). Reachable from the web UI (Start on a held
	// cue); must be a no-op so a dead timer can't be resurrected mid-show.
	ErrNoTimeLeft = errors.New("timerpi: cue has no time left")
	ErrNoNextCue  = errors.New("timerpi: nothing next to go")
	ErrBadArgs    = errors.New("timerpi: bad command args")
	ErrUnknownCmd = errors.New("timerpi: unknown command")
	ErrBadRate    = errors.New("timerpi: rate must be > 0")
)

// NewEngine builds an engine for showID and loads its persisted runtime.
func NewEngine(showID int64, deps EngineDeps) (*Engine, error) {
	d := deps.withDefaults()
	e := &Engine{
		deps: d,
		rt:   Runtime{ShowID: showID, Rate: DefaultRate},
		subs: map[int64]func(Snapshot){},
	}
	if d.Load != nil {
		rt, found, err := d.Load()
		if err != nil {
			return nil, err
		}
		if found {
			rt.ShowID = showID
			if rt.Rate <= 0 {
				rt.Rate = DefaultRate
			}
			e.rt = rt
			if cues, cerr := d.Cues(); cerr == nil {
				if c := cueAtPos(cues, rt.ActivePos); c != nil {
					e.activeID = c.ID
				}
			}
			// Crash recovery: a countdown that crossed zero while the server
			// was down does not replay the crossing as a fresh event (no
			// auto-continue march, no start hook). A HOLD/BLANK cue is still
			// frozen at zero here, exactly as the crossing would have left
			// it, instead of reading as overtime forever (BUGLOG RC7).
			if rt.Running && !rt.Paused && rt.ActivePos > 0 {
				if cues, cerr := d.Cues(); cerr == nil {
					if c := cueAtPos(cues, rt.ActivePos); c != nil && c.TimerKind == TimerCountdown {
						rem, _, _ := DisplayedRemaining(c, e.rt, d.Now())
						e.lastCrossed = rem <= 0
						if e.lastCrossed && (c.EndAction == EndHold || c.EndAction == EndBlank) {
							e.rt.Running = false
							e.rt.PausedElapsedMS = c.DurationMS
							e.rtDirty = true
						}
					}
				}
			}
		}
	}
	return e, nil
}

// withDefaults fills nil deps with harmless stand-ins.
func (d EngineDeps) withDefaults() EngineDeps {
	if d.Now == nil {
		d.Now = func() int64 { return time.Now().UnixMilli() }
	}
	if d.Show == nil {
		d.Show = func() (Show, error) { return Show{}, ErrNoShow }
	}
	if d.Cues == nil {
		d.Cues = func() ([]Cue, error) { return nil, nil }
	}
	if d.Messages == nil {
		d.Messages = func() ([]Message, error) { return nil, nil }
	}
	if d.Stamp == nil {
		stamp := d.Now
		d.Stamp = func() int64 { return stamp() }
	}
	if d.Save == nil {
		d.Save = func(Runtime) error { return nil }
	}
	return d
}

// Runtime returns a copy of the current engine state.
func (e *Engine) Runtime() Runtime {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rt
}

// ---------------------------------------------------------------------------
// Pubsub — the ws hub subscribes and fans out snapshots.

// Subscribe registers a change callback; the returned func unsubscribes.
// Callbacks run on the engine's goroutine after the state lock is released —
// they must not block (hub: push into a channel, broadcast async).
func (e *Engine) Subscribe(fn func(Snapshot)) (cancel func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextSub++
	id := e.nextSub
	e.subs[id] = fn
	return func() {
		e.mu.Lock()
		delete(e.subs, id)
		e.mu.Unlock()
	}
}

// notify invokes subscribers outside the state lock; a panicking subscriber
// is contained so the ticker/transport can't be taken down by one bad hub.
func (e *Engine) notify(snap Snapshot) {
	e.mu.Lock()
	fns := make([]func(Snapshot), 0, len(e.subs))
	for _, fn := range e.subs {
		fns = append(fns, fn)
	}
	e.mu.Unlock()
	for _, fn := range fns {
		func() {
			defer func() { _ = recover() }()
			fn(snap)
		}()
	}
}

// ---------------------------------------------------------------------------
// Transport commands

// ApplyCmd applies a client command (`{"t":"cmd","action":…,"args":{…}}`).
// Only transport commands live here; cue/message CRUD go through the DB (the
// hub calls Engine.Notify() afterwards to re-broadcast).
func (e *Engine) ApplyCmd(action string, args map[string]any) error {
	switch action {
	case "start":
		return e.Start(argInt(args, "pos"))
	case "pause":
		return e.Pause()
	case "resume":
		return e.Resume()
	case "reset":
		return e.Reset()
	case "next":
		return e.Next()
	case "prev":
		return e.Prev()
	case "go":
		return e.Go()
	case "jump":
		return e.Jump(argInt(args, "pos"))
	case "rate":
		return e.SetRate(argFloat(args, "rate"))
	case "daystart":
		// 0 clears the anchor; anything else must be within about a day of
		// now (BUGLOG RS23: {"ts":1} anchored the day in 1970).
		ts := argInt(args, "ts")
		if ts != 0 && (ts < e.now()-36*3600*1000 || ts > e.now()+36*3600*1000) {
			return ErrBadArgs
		}
		return e.SetDayStart(ts)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownCmd, action)
	}
}

// maxArgInt bounds float64 args before the int64 conversion: beyond 2^53
// a JSON number is no longer exact, and past int64 the conversion is
// platform-defined (MinInt64 on amd64, MaxInt64 on arm64).
const maxArgInt = 1 << 53

// argInt reads an int-ish arg (JSON numbers arrive as float64); missing → 0.
func argInt(args map[string]any, key string) int64 {
	if args == nil {
		return 0
	}
	switch v := args[key].(type) {
	case float64:
		if !(v > -maxArgInt && v < maxArgInt) { // NaN or past float precision: refuse (RW30)
			return 0
		}
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// argFloat reads a float arg; missing/invalid → 0.
func argFloat(args map[string]any, key string) float64 {
	if args == nil {
		return 0
	}
	if v, ok := args[key].(float64); ok {
		return v
	}
	return 0
}

// ---------------------------------------------------------------------------
// Transport commands on the runtime

// Start anchors the cue at pos (0 = the armed cue, else the first cue) at
// the wall clock and runs it.
func (e *Engine) Start(pos int64) error {
	err := e.runMutation(func() error { return e.startLocked(pos) })
	if err == nil { // a refused start must not re-fire the running cue (BUGLOG RW24)
		e.fireStart()
	}
	return err
}

// Pause freezes the running cue: scaled elapsed is parked in
// PausedElapsedMS. No-op when not running.
func (e *Engine) Pause() error {
	return e.runMutation(e.pauseLocked)
}

// Resume re-anchors at the wall clock keeping the parked elapsed. No-op when
// not paused.
func (e *Engine) Resume() error {
	return e.runMutation(e.resumeLocked)
}

// Reset returns the active cue to the anchor-less armed state.
func (e *Engine) Reset() error {
	return e.runMutation(e.resetLocked)
}

// Go fires the next unarmed cue: the armed one when idle/held, otherwise the
// cue after the running one; from a fresh idle show it starts cue 1.
func (e *Engine) Go() error {
	err := e.runMutation(e.goLocked)
	if err == nil { // GO past the last cue must not re-fire the running one (RW24)
		e.fireStart()
	}
	return err
}

// Next arms the next cue after the active one (or the first when idle).
func (e *Engine) Next() error {
	return e.runMutation(func() error { return e.jumpRelLocked(1) })
}

// Prev arms the previous cue.
func (e *Engine) Prev() error {
	return e.runMutation(func() error { return e.jumpRelLocked(-1) })
}

// Jump arms the cue at pos (stopping any running cue): anchor-less, elapsed 0.
func (e *Engine) Jump(pos int64) error {
	return e.runMutation(func() error { return e.jumpLocked(pos) })
}

// fireStart ticks the outbound media hook when the mutation left a cue
// RUNNING (start/go; resume/pause re-anchor without firing). Reads the
// post-mutation runtime — the hook consumer replays only the fresh cue.
func (e *Engine) fireStart() {
	if e.onFire == nil {
		return
	}
	e.mu.Lock()
	pos, running := e.rt.ActivePos, e.rt.Running
	e.mu.Unlock()
	if running && pos > 0 {
		e.onFire(pos)
	}
}

// SetRate changes the countdown rate multiplier, re-anchoring so the
// displayed time is continuous (never jumps): the displayed elapsed at the
// change instant becomes the new PausedElapsedMS and the anchor moves to
// now. Schedule math is untouched (PROTOCOL §Engine).
func (e *Engine) SetRate(rate float64) error {
	return e.runMutation(func() error { return e.setRateLocked(rate) })
}

// SetDayStart sets the schedule/day-bar anchor explicitly.
func (e *Engine) SetDayStart(ts int64) error {
	return e.runMutation(func() error {
		e.rt.DayStartTS = ts
		return nil
	})
}

// ---------------------------------------------------------------------------
// Tick — called by the server's ticker (or on demand); nowMS comes from the
// caller so tests can drive it. Emits a snapshot only on a state change
// (zero crossing, auto-advance, alert transition) — digits are client-ticked.

func (e *Engine) Tick(nowMS int64) error {
	e.mu.Lock()
	changed := false
	startedPos := int64(0) // a cue auto-started this tick (media hook)
	cues, err := e.cuesLocked()
	if err != nil {
		e.mu.Unlock()
		return err
	}
	if nowMS-e.lastRollCheck >= 60_000 {
		e.lastRollCheck = nowMS
		e.rolloverLocked(cues, nowMS)
	}
	if c := cueAtPos(cues, e.rt.ActivePos); c != nil && e.rt.Running && !e.rt.Paused && c.TimerKind == TimerCountdown {
		remaining, _, _ := DisplayedRemaining(c, e.rt, nowMS)
		crossed := remaining <= 0
		if crossed && !e.lastCrossed {
			e.lastCrossed = true
			switch c.EndAction {
			case EndHold, EndBlank:
				// Freeze the countdown at zero (HOLD shows 00:00, BLANK shows
				// blank + scheduled end). Running=false + PausedElapsedMS =
				// duration reads back as remaining 0 everywhere.
				e.rt.Running = false
				e.rt.PausedElapsedMS = c.DurationMS
			case EndOvertime:
				// Keep counting up; remaining simply goes negative.
			}
			changed = true
			// AutoContinue (v1): the next cue starts immediately at zero
			// crossing; the exhausted cue's HoldMS is schedule-only.
			if c.AutoContinue && c.EndAction == EndHold {
				if next := nextPosOf(cues, e.rt.ActivePos); next > 0 {
					if serr := e.startLocked(next); serr == nil {
						e.lastCrossed = false
						startedPos = next // PLAN §11.6: media hook hears auto-advance
					}
				}
				// No next cue: hold at zero.
			}
		}
	}
	// Alert transitions emit even when nothing else changed.
	alert := 0
	if c := cueAtPos(cues, e.rt.ActivePos); c != nil {
		_, _, alert = DisplayedRemaining(c, e.rt, nowMS)
	}
	if alert != e.lastAlert {
		e.lastAlert = alert
		changed = true
	}
	// E5 wall-clock auto-start: while nothing runs and nothing is paused,
	// the first cue past the playhead whose startAt has come fires itself.
	// Hand operation always wins (running/paused runtimes never yank),
	// firing advances ActivePos so a cue never refires, and jumping back
	// before a passed time re-arms it (clear startAt to stop that).
	// ActivePos > 0 keeps the "day under way" promise: a brand-new (or
	// freshly cloned) show with inherited startAt times must NOT auto-
	// start cue 1 at 14:00 on a day nobody has begun.
	if !e.rt.Running && !e.rt.Paused && e.rt.ActivePos > 0 {
		if next := autoStartDue(cues, e.rt.ActivePos, e.rt.DayStartTS, nowMS); next > 0 {
			if serr := e.startLocked(next); serr == nil {
				changed = true
				startedPos = next // E5 scheduled start fires the hook too
			}
		}
	}
	if e.rtDirty {
		changed = true
	}
	if !changed {
		e.mu.Unlock()
		return nil
	}
	snap, err := e.commitLocked()
	// PLAN §11.6: auto-advance (AutoContinue) and E5 wall-clock auto-start
	// started a cue under the lock — the outbound media hook must hear
	// about them exactly like a hand GO. fireStart locks, so emit after
	// the unlock.
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.notify(snap)
	if startedPos > 0 && e.onFire != nil {
		e.onFire(startedPos)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Snapshot / timer frame

// Snapshot builds the current wire state.
func (e *Engine) Snapshot() (Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

// Timer is the small frame for the display path
// (`{"t":"timer", …}`): what the digits need beyond the state snapshot.
func (e *Engine) Timer() (TimerFrame, error) {
	e.mu.Lock()
	cues, err := e.cuesLocked()
	rt, now := e.rt, e.now()
	e.mu.Unlock()
	if err != nil {
		return TimerFrame{}, err
	}
	return timerFrame(cueAtPos(cues, rt.ActivePos), rt, now), nil
}

// TimerFrame is the small JSON timer frame: {remainingMS, overtime,
// alertState, rate} plus render hints (blank/paused/timerKind/alertColor).
type TimerFrame struct {
	RemainingMS int64   `json:"remainingMS"`
	Overtime    bool    `json:"overtime"`
	AlertState  int     `json:"alertState"`
	AlertColor  string  `json:"alertColor"`
	Rate        float64 `json:"rate"`
	Paused      bool    `json:"paused"`
	Blank       bool    `json:"blank"`
	TimerKind   string  `json:"timerKind"`
}

func timerFrame(c *Cue, rt Runtime, now int64) TimerFrame {
	rem, overtime, alert := DisplayedRemaining(c, rt, now)
	tk := ""
	if c != nil {
		tk = c.TimerKind
	}
	blank := c != nil && !rt.Running && rem <= 0 && c.EndAction == EndBlank && c.TimerKind == TimerCountdown
	if blank {
		rem = 0
		overtime = false
	}
	return TimerFrame{
		RemainingMS: rem,
		Overtime:    overtime,
		AlertState:  alert,
		AlertColor:  AlertColor(c, alert),
		Rate:        rt.Rate,
		Paused:      rt.Paused,
		Blank:       blank,
		TimerKind:   tk,
	}
}

// Notify re-broadcasts the current snapshot — the hub calls it after cue or
// message CRUD (which bypass the engine) so every client re-syncs.
func (e *Engine) Notify() error {
	e.mu.Lock()
	snap, err := e.commitLocked()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.notify(snap)
	return nil
}

// ---------------------------------------------------------------------------
// Internal (callers hold e.mu)

// now is the deps' clock (must be called with the lock held only when the
// result feeds a mutation; reads are harmless).
func (e *Engine) now() int64 { return e.deps.Now() }

// runMutation runs a mutation under the lock, then persists + emits.
func (e *Engine) runMutation(mut func() error) error {
	e.mu.Lock()
	if err := mut(); err != nil {
		e.mu.Unlock()
		return err
	}
	snap, err := e.commitLocked()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.notify(snap)
	return nil
}

// commitLocked persists the runtime and builds the snapshot.
func (e *Engine) commitLocked() (Snapshot, error) {
	snap, err := e.snapshotLocked()
	e.rtDirty = false
	if serr := e.deps.Save(e.rt); err == nil {
		err = serr
	}
	return snap, err
}

// snapshotLocked builds the wire snapshot from the deps' view of the show.
func (e *Engine) snapshotLocked() (Snapshot, error) {
	show, err := e.deps.Show()
	if err != nil {
		return Snapshot{}, err
	}
	cues, err := e.cuesLocked()
	if err != nil {
		return Snapshot{}, err
	}
	msgs, err := e.deps.Messages()
	if err != nil {
		return Snapshot{}, err
	}
	stamp := e.deps.Stamp()
	if stamp == 0 {
		stamp = e.now()
	}
	return BuildSnapshot(show, cues, msgs, e.rt, stamp, e.now()), nil
}

// startLocked anchors cue pos at the wall clock and runs it.
func (e *Engine) startLocked(pos int64) error {
	cues, err := e.cuesLocked()
	if err != nil {
		return err
	}
	if pos == 0 {
		switch {
		case e.rt.ActivePos > 0:
			pos = e.rt.ActivePos // start (or restart) the armed cue
		case len(cues) > 0:
			pos = cues[0].Pos
		default:
			return ErrNoCues
		}
	}
	c := cueAtPos(cues, pos)
	if c == nil {
		return ErrUnknownPos
	}
	now := e.now()
	// Held-cue guard FIRST: a countdown cue already held at zero cannot be
	// STARTed (old semantics: ≤0 remaining is dead). The runtime must not
	// be touched — checking after mutation would resurrect the anchor.
	// Operators reach for Prev/Next/Reset to leave the hole.
	if c.TimerKind == TimerCountdown && c.EndAction == EndHold &&
		c.DurationMS > 0 {
		// LOCAL runtime currently points at whatever was active before.
		// Eligibility for "held" is about THIS cue's own elapsed state,
		// which only exists when it was the active one and consumed.
		if e.rt.ActivePos == c.Pos && !e.rt.Running && e.rt.PausedElapsedMS >= c.DurationMS && e.rt.EndAction == EndHold {
			return ErrNoTimeLeft
		}
		// Fresh anchor of a fully-consumed zero-duration row would cross
		// instantly; that is handled by Tick, not here.
		_ = now
	}
	e.rt.ActivePos = c.Pos
	e.activeID = c.ID
	e.rt.PrevPos, e.rt.NextPos = neighborsOf(cues, c.Pos)
	e.rt.Running = true
	e.rt.Paused = false
	e.rt.AnchorTS = now
	e.rt.PausedElapsedMS = 0
	e.rt.EndAction = c.EndAction
	if e.rt.DayStartTS == 0 {
		e.rt.DayStartTS = startOfDay(now)
	}
	e.lastAlert = 0
	e.lastCrossed = false
	return nil
}

// pauseLocked freezes the scaled elapsed. Order matters: the scaled elapsed
// must be captured BEFORE Paused flips, or cueElapsedMS would see a paused
// runtime and return the stale value.
func (e *Engine) pauseLocked() error {
	if !e.rt.Running || e.rt.Paused {
		return nil // friendly no-op
	}
	e.rt.PausedElapsedMS = cueElapsedMS(nil, e.rt, e.now())
	e.rt.Paused = true
	return nil
}

// resumeLocked re-anchors at the wall clock.
func (e *Engine) resumeLocked() error {
	if !e.rt.Paused {
		return nil
	}
	e.rt.Paused = false
	e.rt.AnchorTS = e.now() // PausedElapsedMS keeps the parked scaled elapsed
	return nil
}

// resetLocked arms the active cue: no anchor, elapsed zero.
func (e *Engine) resetLocked() error {
	if e.rt.ActivePos == 0 {
		return nil
	}
	e.rt.Running = false
	e.rt.Paused = false
	e.rt.AnchorTS = 0
	e.rt.PausedElapsedMS = 0
	e.lastAlert = 0
	e.lastCrossed = false
	return nil
}

// goLocked fires the next unarmed cue.
func (e *Engine) goLocked() error {
	cues, err := e.cuesLocked()
	if err != nil {
		return err
	}
	if len(cues) == 0 {
		return ErrNoCues
	}
	switch {
	case e.rt.ActivePos == 0:
		return e.startLocked(cues[0].Pos) // nothing selected: first cue
	case !e.rt.Running:
		// GO restarts a held-at-zero cue (owner decision 2026-10-05):
		// the explicit Start(pos) still refuses ErrNoTimeLeft, but GO is
		// the operator's "run it again" — clear the spent elapsed first
		// so startLocked's held guard passes. Armed cues already sit at
		// zero, so this is a no-op for them. runMutation has no rollback:
		// a failed startLocked must leave the runtime exactly as it was
		// (a half-mutated held cue would un-hold on the NEXT commit).
		saved := e.rt.PausedElapsedMS
		e.rt.PausedElapsedMS = 0
		if gerr := e.startLocked(e.rt.ActivePos); gerr != nil {
			e.rt.PausedElapsedMS = saved
			return gerr
		}
		return nil
	case e.rt.NextPos > 0:
		return e.startLocked(e.rt.NextPos)
	default:
		return ErrNoNextCue
	}
}

// jumpLocked arms the cue at pos.
func (e *Engine) jumpLocked(pos int64) error {
	cues, err := e.cuesLocked()
	if err != nil {
		return err
	}
	c := cueAtPos(cues, pos)
	if c == nil {
		return ErrUnknownPos
	}
	e.rt.ActivePos = c.Pos
	e.activeID = c.ID
	e.rt.PrevPos, e.rt.NextPos = neighborsOf(cues, c.Pos)
	e.rt.Running = false
	e.rt.Paused = false
	e.rt.AnchorTS = 0
	e.rt.PausedElapsedMS = 0
	e.rt.EndAction = c.EndAction
	e.lastAlert = 0
	e.lastCrossed = false
	return nil
}

// jumpRelLocked arms the cue ±1 from the active one.
func (e *Engine) jumpRelLocked(dir int64) error {
	cues, err := e.cuesLocked()
	if err != nil {
		return err
	}
	if len(cues) == 0 {
		return ErrNoCues
	}
	if e.rt.ActivePos == 0 {
		return e.jumpLocked(cues[0].Pos)
	}
	if dir > 0 {
		if e.rt.NextPos > 0 {
			return e.jumpLocked(e.rt.NextPos)
		}
		return nil // last cue: stay
	}
	if e.rt.PrevPos > 0 {
		return e.jumpLocked(e.rt.PrevPos)
	}
	return nil // first cue: stay
}

// setRateLocked re-anchors so the displayed time never jumps on a rate
// change (PROTOCOL §Engine):
//
//	before: displayed(now) = pausedElapsed + (now - anchor) * oldRate
//	after:  anchor = now, pausedElapsed = displayed(now), rate = newRate
//	→ displayed stays identical at the change instant, then scales by newRate.
func (e *Engine) setRateLocked(rate float64) error {
	if rate <= 0 {
		return ErrBadRate
	}
	if e.rt.Running && !e.rt.Paused && e.rt.AnchorTS > 0 {
		e.rt.PausedElapsedMS = cueElapsedMS(nil, e.rt, e.now())
		e.rt.AnchorTS = e.now()
	}
	e.rt.Rate = rate
	return nil
}

// dayRolloverGrace: how long after a day's planned end the room may start
// a new day (overruns and late finishes stay on their day).
const dayRolloverGrace int64 = 4 * 3600 * 1000

// rolloverLocked starts a new day for an idle room (BUGLOG RW26, owner
// 2026-10-06 "handle midnight and day roll over"). The stored day anchor
// used to stay on the first day forever, so day 2 showed yesterday's times
// and every session as done. A new day starts only when ALL hold:
//   - nothing is running or paused (a show over midnight is never cut);
//   - the anchor is on an earlier calendar day than now;
//   - the anchored day's planned end plus 4 h has passed (late finishes,
//     overruns and paused breaks after midnight keep their day).
//
// Then the day re-anchors to the room's scheduled start ("09:00" → today
// 09:00; none → unanchored until the first GO) and the playhead goes back
// to the top, so GO starts the first session again.
func (e *Engine) rolloverLocked(cues []Cue, now int64) bool {
	if e.rt.Running || e.rt.Paused || e.rt.DayStartTS == 0 {
		return false
	}
	if startOfDay(e.rt.DayStartTS) >= startOfDay(now) {
		return false
	}
	end := e.rt.DayStartTS + ComputeSchedule(cues, e.rt.DayStartTS, 1).TotalMS
	if now < end+dayRolloverGrace {
		return false
	}
	anchor := int64(0)
	if e.deps.Show != nil {
		if sh, err := e.deps.Show(); err == nil {
			anchor = DayStartTSFrom(sh.DayStart, now)
		}
	}
	e.rt.DayStartTS = anchor
	e.rt.ActivePos, e.rt.PrevPos, e.rt.NextPos = 0, 0, 0
	e.activeID = 0
	e.rt.AnchorTS, e.rt.PausedElapsedMS = 0, 0
	e.lastAlert, e.lastCrossed = 0, false
	e.rtDirty = true
	return true
}

// ---------------------------------------------------------------------------
// Pure helpers shared with schedule.go / snapshot.go

// autoStartDue returns the pos of the first cue past activePos whose
// wall-clock startAt has come (0 when none). Cues at or before the playhead
// are never re-armed by the clock — firing moves ActivePos forward, which
// is also what stops refires.
// Times resolve inside the room's day (ClockAt), so "00:15" in a day that
// started at 18:00 means tonight after midnight, not this morning.
func autoStartDue(cues []Cue, activePos, dayStartTS, nowMS int64) int64 {
	for i := range cues {
		if cues[i].Pos <= activePos {
			continue
		}
		if cues[i].StartAt == "" {
			continue
		}
		if ts := ClockAt(cues[i].StartAt, dayStartTS, nowMS); ts != 0 && ts <= nowMS {
			return cues[i].Pos
		}
	}
	return 0
}

// cueAtPos finds the cue at pos in an ordered list.
func cueAtPos(cues []Cue, pos int64) *Cue {
	for i := range cues {
		if cues[i].Pos == pos {
			return &cues[i]
		}
	}
	return nil
}

// cuesLocked loads the cue list and re-points the playhead at the active
// cue by id: cue CRUD (delete, move, insert, duplicate, replace) renumbers
// positions without telling the engine. If the active cue moved, its pos
// and neighbours follow it. If it was deleted, the cue that took its slot
// (or the new last cue) is armed; an empty list goes idle. Returns whether
// the runtime changed, via e.rtDirty, so Tick commits the repair.
func (e *Engine) cuesLocked() ([]Cue, error) {
	cues, err := e.deps.Cues()
	if err != nil {
		return nil, err
	}
	e.reconcileLocked(cues)
	return cues, nil
}

func (e *Engine) reconcileLocked(cues []Cue) {
	if e.rt.ActivePos == 0 {
		return
	}
	if e.activeID == 0 {
		if c := cueAtPos(cues, e.rt.ActivePos); c != nil {
			e.activeID = c.ID
		}
		return
	}
	for i := range cues {
		if cues[i].ID != e.activeID {
			continue
		}
		prev, next := neighborsOf(cues, cues[i].Pos)
		if cues[i].Pos != e.rt.ActivePos || prev != e.rt.PrevPos || next != e.rt.NextPos {
			e.rt.ActivePos, e.rt.PrevPos, e.rt.NextPos = cues[i].Pos, prev, next
			e.rtDirty = true
		}
		return
	}
	// The active cue is gone: arm whatever now sits in its slot.
	e.rtDirty = true
	e.lastAlert, e.lastCrossed = 0, false
	e.rt.Running, e.rt.Paused = false, false
	e.rt.AnchorTS, e.rt.PausedElapsedMS = 0, 0
	if len(cues) == 0 {
		e.activeID = 0
		e.rt.ActivePos, e.rt.PrevPos, e.rt.NextPos = 0, 0, 0
		return
	}
	c := &cues[len(cues)-1]
	for i := range cues {
		if cues[i].Pos >= e.rt.ActivePos {
			c = &cues[i]
			break
		}
	}
	e.activeID = c.ID
	e.rt.ActivePos = c.Pos
	e.rt.PrevPos, e.rt.NextPos = neighborsOf(cues, c.Pos)
	e.rt.EndAction = c.EndAction
}

// neighborsOf returns the positions before/after pos in run order (0 when
// there is none).
func neighborsOf(cues []Cue, pos int64) (prev, next int64) {
	for i := range cues {
		if cues[i].Pos == pos {
			if i > 0 {
				prev = cues[i-1].Pos
			}
			if i+1 < len(cues) {
				next = cues[i+1].Pos
			}
			return prev, next
		}
	}
	return 0, 0
}

// nextPosOf returns the next existing position after pos (0 when last).
func nextPosOf(cues []Cue, pos int64) int64 {
	_, next := neighborsOf(cues, pos)
	return next
}

// cueElapsedMS returns the scaled elapsed of the runtime (cue is unused
// today; kept in the signature so future per-cue scaling has a seam).
func cueElapsedMS(_ *Cue, rt Runtime, now int64) int64 {
	rate := rt.Rate
	if rate <= 0 {
		rate = DefaultRate
	}
	elapsed := rt.PausedElapsedMS
	if rt.Running && !rt.Paused && rt.AnchorTS > 0 {
		elapsed += int64(float64(now-rt.AnchorTS) * rate)
	}
	return elapsed
}

// startOfDay returns local midnight (epoch ms) for an epoch-ms instant — the
// default DayStartTS when a show starts with none set.
func startOfDay(now int64) int64 {
	t := time.UnixMilli(now).Local()
	mid := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return mid.UnixMilli()
}

// ---------------------------------------------------------------------------
// Engines — one engine per show, wired to the DB. The ws hub's entry point.

// Engines lazily builds and caches one Engine per show over a *DB.
type Engines struct {
	mu     sync.Mutex
	db     *DB
	byShow map[int64]*Engine
	// OnStart is the outbound media hook (oscbridge, proposal #8): fire a
	// peer (CuTePi via QLab-OSC) when a cue starts running. Wire once at
	// boot, before any engine exists.
	OnStart func(showID, pos int64)
}

// NewEngines returns an engine registry over the DB.
func NewEngines(db *DB) *Engines {
	return &Engines{db: db, byShow: map[int64]*Engine{}}
}

// Get returns the show's engine, creating and wiring it on first use.
func (r *Engines) Get(showID int64) (*Engine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.byShow[showID]; ok {
		return e, nil
	}
	e, err := NewEngine(showID, r.db.EngineDeps(showID))
	if err != nil {
		return nil, err
	}
	// Outbound media hook (oscbridge, proposal #8): set once at boot, read
	// without the registry lock — assignment happens before any engine
	// exists (main wires it immediately after NewEngines).
	e.onFire = func(pos int64) {
		if r.OnStart != nil {
			r.OnStart(showID, pos)
		}
	}
	// B4: a show with a scheduled day start ("09:00") auto-anchors at
	// engine construction — service reboot before the show should not
	// leave the day-bar needle hidden. Only anchors when the operator
	// hasn't manually anchored (manual anchors stay under the operator).
	// A box switched on the next morning starts the new day straight away
	// (the first Tick runs the day-rollover check).
	if e != nil {
		_ = e.Tick(e.now())
	}
	if e != nil && e.deps.Show != nil {
		if sh, serr := e.deps.Show(); serr == nil {
			if ts := DayStartTSFrom(sh.DayStart, e.now()); ts != 0 && e.rt.DayStartTS == 0 {
				_ = e.SetDayStart(ts)
			}
		}
	}
	r.byShow[showID] = e
	return e, nil
}

// Drop forgets a show's engine (after DeleteShow).
func (r *Engines) Drop(showID int64) {
	r.mu.Lock()
	delete(r.byShow, showID)
	r.mu.Unlock()
}

// EngineDeps wires the engine to this DB's show data (production wiring).
func (d *DB) EngineDeps(showID int64) EngineDeps {
	return EngineDeps{
		Now:      nowMS,
		Show:     func() (Show, error) { return d.GetShow(showID) },
		Cues:     func() ([]Cue, error) { return d.ListCues(showID) },
		Messages: func() ([]Message, error) { return d.ListMessages(showID) },
		Stamp:    func() int64 { return d.UpdatedStamp(showID) },
		Load:     func() (Runtime, bool, error) { return d.LoadRuntime(showID) },
		Save:     d.SaveRuntime,
	}
}
