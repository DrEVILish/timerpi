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
			// Crash recovery: if the persisted state was already past zero,
			// don't replay the crossing as a fresh event on the first Tick.
			if rt.Running && !rt.Paused && rt.ActivePos > 0 {
				if cues, cerr := d.Cues(); cerr == nil {
					if c := cueAtPos(cues, rt.ActivePos); c != nil && c.TimerKind == TimerCountdown {
						rem, _, _ := DisplayedRemaining(c, rt, d.Now())
						e.lastCrossed = rem <= 0
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
		return e.SetDayStart(argInt(args, "ts"))
	default:
		return fmt.Errorf("%w: %q", ErrUnknownCmd, action)
	}
}

// argInt reads an int-ish arg (JSON numbers arrive as float64); missing → 0.
func argInt(args map[string]any, key string) int64 {
	if args == nil {
		return 0
	}
	switch v := args[key].(type) {
	case float64:
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
	e.fireStart()
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
	e.fireStart()
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
	cues, err := e.deps.Cues()
	if err != nil {
		e.mu.Unlock()
		return err
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
		if next := autoStartDue(cues, e.rt.ActivePos, nowMS); next > 0 {
			if serr := e.startLocked(next); serr == nil {
				changed = true
			}
		}
	}
	if !changed {
		e.mu.Unlock()
		return nil
	}
	snap, err := e.commitLocked()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.notify(snap)
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
	cues, err := e.deps.Cues()
	if err != nil {
		return TimerFrame{}, err
	}
	e.mu.Lock()
	rt, now := e.rt, e.now()
	e.mu.Unlock()
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
	cues, err := e.deps.Cues()
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
	cues, err := e.deps.Cues()
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
	cues, err := e.deps.Cues()
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
	cues, err := e.deps.Cues()
	if err != nil {
		return err
	}
	c := cueAtPos(cues, pos)
	if c == nil {
		return ErrUnknownPos
	}
	e.rt.ActivePos = c.Pos
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
	cues, err := e.deps.Cues()
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

// ---------------------------------------------------------------------------
// Pure helpers shared with schedule.go / snapshot.go

// autoStartDue returns the pos of the first cue past activePos whose
// wall-clock startAt has come (0 when none). Cues at or before the playhead
// are never re-armed by the clock — firing moves ActivePos forward, which
// is also what stops refires.
func autoStartDue(cues []Cue, activePos, nowMS int64) int64 {
	for i := range cues {
		if cues[i].Pos <= activePos {
			continue
		}
		if cues[i].StartAt == "" {
			continue
		}
		if ts := DayStartTSFrom(cues[i].StartAt, nowMS); ts != 0 && ts <= nowMS {
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
