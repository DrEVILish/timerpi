// package ws — commands.go: inbound frame handling (cmd dispatch per
// CONTRACT-UI §4, mapped onto timerpi per NOTES-timerpi), WebRTC signal
// relay, and the oob signature functions deciding which fragments
// re-render.
package ws

import (
	"encoding/json"
	"fmt"
	"strings"

	"timerpi/oscbridge"
	"timerpi/timerpi"
	"timerpi/views"
)

// handle is the app-level frame entry from a session's readLoop.
// Unrecognized frames answer err to that session only; a recover keeps one
// malformed handler from killing the hub.
func (h *Hub) handle(s *session, raw []byte) {
	frame := struct {
		T      string          `json:"t"`
		Action string          `json:"action"`
		Args   json.RawMessage `json:"args"`
		To     string          `json:"to"`
		Data   json.RawMessage `json:"data"`
	}{}
	if err := json.Unmarshal(raw, &frame); err != nil {
		s.sendErr("unparseable frame")
		return
	}
	errOf := func(err error) {
		if err != nil {
			s.sendErr(err.Error())
		}
	}

	defer func() {
		if r := recover(); r != nil {
			h.logf("ws: handler panic for %s: %v", s.id, r)
			s.sendErr("internal error handling frame")
		}
	}()

	switch frame.T {
	case "ping":
		s.sendFrame("t", "pong", "serverTime", h.nowFn())
	case "cmd":
		h.command(s, frame.Action, frame.Args, errOf)
	case "signal":
		if s.role == "audience" {
			s.sendErr("audience is read-only")
			return
		}
		h.signal(s, frame.To, frame.Data)
	default:
		s.sendErr(fmt.Sprintf("unknown frame type %q", frame.T))
	}
}

// command applies one operator command row (CONTRACT-UI §4 mapped onto the
// engine per NOTES-timerpi): transport via eng.ApplyCmd; cue/message CRUD
// via the DB + eng.Notify(). No success ack frame — the state fanout IS
// the ack; errors go to the sending client only (panels never replace on
// error) and transport errors like ErrNoNextCue are operator conditions,
// not faults.
// logAction records one operator mutation (E4 action log). Actor is
// role:peerId — multi-operator accountability + post-show review.
func (h *Hub) logAction(s *session, action, detail string) {
	if h.store == nil {
		return
	}
	h.store.LogAction(s.showID, s.role+":"+s.id, action, detail)
}

// mutDone finishes a STORE-op branch (cue/message/flag rows bypass the
// engine, so the fanout must be explicit): op failure speaks (and is NOT
// logged); success is logged, then fanned out.
func (h *Hub) mutDone(s *session, eng *timerpi.Engine, action, detail string, errOf func(error), opErr error) {
	if opErr != nil {
		errOf(opErr)
		return
	}
	h.logAction(s, action, detail)
	errOf(eng.Notify())
}

// engDone finishes an ENGINE-verb branch: runMutation already fanned out
// (a second Notify would double-broadcast and shift clients reading one
// state per command), so success only logs.
func (h *Hub) engDone(s *session, action, detail string, errOf func(error), opErr error) {
	if opErr != nil {
		errOf(opErr)
		return
	}
	h.logAction(s, action, detail)
}

func (h *Hub) command(s *session, action string, rawArgs json.RawMessage, errOf func(error)) {
	// A1: with the operator password set, all commands belong to a live
	// operator login — the possibly-unauthenticated display/mesh roles are
	// strictly read-side (fans out state; signals P2P) and must not mutate
	// even by spoofing their join role.
	// PLAN §11.5: the audience lane is read-only by design — votes/asks go
	// over REST with their own guards, never over this socket.
	if s.role == "audience" {
		s.sendErr("audience is read-only (vote on the room's web page)")
		return
	}
	// Screens (display role) are strictly read-side: they fan state in and
	// relay P2P signals, but never mutate (STATUS B6).
	if s.role != "controls" {
		s.sendErr("role " + s.role + " is read-only")
		return
	}
	args := map[string]any{}
	if len(rawArgs) > 0 {
		_ = json.Unmarshal(rawArgs, &args)
	}
	eng := h.engFor(s)
	if eng == nil {
		s.sendErr("show engine unavailable")
		return
	}
	switch action {
	// --- transport (engine-owned) ---
	case "go":
		if argInt(args, "pos") > 0 {
			h.engDone(s, "start", fmt.Sprintf("pos %d", argInt(args, "pos")), errOf, eng.ApplyCmd("start", args))
		} else {
			h.engDone(s, "go", "", errOf, eng.ApplyCmd("go", args))
		}
	case "start":
		h.engDone(s, "start", fmt.Sprintf("pos %d", argInt(args, "pos")), errOf, eng.ApplyCmd("start", args))
	case "pause":
		// The keyboard sends `pause` to TOGGLE: map a paused runtime onto
		// the engine's explicit "resume" (NOTES-timerpi deviation 2).
		rt := eng.Runtime()
		if rt.Paused {
			h.engDone(s, "resume", "", errOf, eng.ApplyCmd("resume", args))
		} else {
			h.engDone(s, "pause", "", errOf, eng.ApplyCmd("pause", args))
		}
	case "reset", "next", "prev":
		h.engDone(s, action, "", errOf, eng.ApplyCmd(action, args))
	case "blank", "unblank":
		// E3: global display blackout. DB flag + fanout (the fanned
		// snapshot carries show.blanked to every screen). Role gating
		// rides the A1 check at the top of command().
		if err := h.store.SetShowBlanked(s.showID, action == "blank"); err != nil {
			errOf(err)
			return
		}
		h.logAction(s, action, "")
		// Outbound media bridge: BLANK cuts CuTePi to its panic holding
		// image; unblank resumes the playout peer.
		if action == "blank" {
			oscbridge.FireOut("panic", 0)
		} else {
			oscbridge.FireOut("go", 0)
		}
		errOf(eng.Notify())
	case "jump":
		// jump default = arm without starting; start:true begins now.
		if argBool(args, "start") {
			h.engDone(s, "start", fmt.Sprintf("pos %d", argInt(args, "pos")), errOf, eng.ApplyCmd("start", args))
		} else {
			h.engDone(s, "jump", fmt.Sprintf("pos %d", argInt(args, "pos")), errOf, eng.ApplyCmd("jump", args))
		}
	case "rate":
		rate, _ := args["rate"].(float64)
		h.engDone(s, "rate", fmt.Sprintf("x%g", rate), errOf, eng.ApplyCmd("rate", args))
	case "settings":
		// C2 drill fix: every day-anchor change must FAN OUT. Only the
		// clear-automation branch used to call Notify(), so "Day starts now"
		// (settings{ts}) re-anchored the engine but the connected pages kept
		// the stale START/END columns and day-bar scale until the next
		// unrelated structural change (parity with the REST twin, which both
		// re-anchors and notifies).
		if ts, ok := int64Arg(args, "ts"); ok {
			// NOTE: the trailing Notify is pre-existing (C2 fanout fix):
			// runMutation already fanned out once, this re-fans so the
			// day-bar/START-END columns repaint together with the anchor.
			if derr := eng.ApplyCmd("daystart", map[string]any{"ts": ts}); derr != nil {
				errOf(derr)
			} else {
				h.logAction(s, "daystart", fmt.Sprintf("ts %d", ts))
				errOf(eng.Notify())
			}
		} else if hhmm, ok := stringArg(args, "dayStart"); ok {
			// B4: persist the scheduled start ("09:00") and anchor today to
			// it now (scheduling and anchor move together; an empty string
			// clears the automation but leaves today's anchor alone).
			if err := h.store.SetShowDayStart(s.showID, hhmm); err != nil {
				errOf(err)
			} else if ts := timerpi.DayStartTSFrom(hhmm, h.nowFn()); ts != 0 {
				if derr := eng.ApplyCmd("daystart", map[string]any{"ts": ts}); derr != nil {
					errOf(derr)
				} else {
					h.logAction(s, "daystart", hhmm)
					errOf(eng.Notify())
				}
			} else {
				h.logAction(s, "daystart", "cleared")
				errOf(eng.Notify()) // structural change (setting) → re-fanout
			}
		} else if title, ok := stringArg(args, "title"); ok && strings.TrimSpace(title) != "" {
			if rerr := h.store.RenameShow(s.showID, strings.TrimSpace(title)); rerr != nil {
				errOf(rerr)
				return
			}
			h.logAction(s, "rename", strings.TrimSpace(title))
			errOf(eng.Notify())
		} else {
			s.sendErr("settings needs ts or title")
		}

	// --- cue CRUD (DB rows; the engine re-broadcasts via Notify) ---
	case "cueAdd":
		label, _ := stringArg(args, "label")
		if strings.TrimSpace(label) == "" {
			s.sendErr("cueAdd needs label")
			return
		}
		dur := argInt(args, "durationMS")
		if dur <= 0 { // dashboard quick-add sends m:ss text as `mss`
			dur = views.ParseDuration(str(args, "mss"))
		}
		cue := timerpi.Cue{Label: label, DurationMS: dur, Kind: str(args, "kind")}
		cue.Normalize()
		if _, cerr := h.store.CreateCue(s.showID, cue); cerr != nil {
			errOf(cerr)
			return
		}
		h.logAction(s, "cueAdd", label)
		errOf(eng.Notify())
	case "cueEdit":
		h.commandCueEdit(s, eng, args, errOf)
	case "cueDel":
		pos := argInt(args, "pos")
		if pos <= 0 {
			s.sendErr("cueDel needs pos")
			return
		}
		if derr := h.store.DeleteCue(s.showID, pos); derr != nil {
			errOf(derr)
			return
		}
		h.logAction(s, "cueDel", fmt.Sprintf("pos %d", pos))
		errOf(eng.Notify())
	case "cueMove":
		pos := argInt(args, "pos")
		// B2: two forms — {pos, dir up|down} (keyboard/steppers) and
		// {pos, to} (target-slot drag/automation). Empty `to` keeps the
		// legacy path; a present `to` goes straight to the full-slot move.
		if to, ok := int64Arg(args, "to"); ok {
			if pos <= 0 {
				s.sendErr("cueMove needs pos")
				return
			}
			h.mutDone(s, eng, "cueMove", fmt.Sprintf("pos %d to %d", pos, to), errOf,
				h.store.MoveCue(s.showID, pos, to))
			return
		}
		dir := str(args, "dir")
		if pos <= 0 || (dir != "up" && dir != "down") {
			s.sendErr("cueMove needs pos and dir up|down")
			return
		}
		target := pos - 1
		if dir == "down" {
			target = pos + 1
		}
		// Out-of-range targets are a no-op at the DB layer.
		h.mutDone(s, eng, "cueMove", fmt.Sprintf("pos %d %s", pos, dir), errOf,
			h.store.MoveCue(s.showID, pos, target))
	case "cueDup":
		pos := argInt(args, "pos")
		if pos <= 0 {
			s.sendErr("cueDup needs pos")
			return
		}
		if _, err := h.store.DuplicateCue(s.showID, pos); err != nil {
			errOf(err)
			return
		}
		h.logAction(s, "cueDup", fmt.Sprintf("pos %d", pos))
		errOf(eng.Notify())

	// --- messages ---
	case "addMsg":
		text, _ := stringArg(args, "text")
		if strings.TrimSpace(text) == "" {
			s.sendErr("addMsg needs text")
			return
		}
		color := str(args, "color")
		if color != "" && !timerpi.ValidColor(color) {
			s.sendErr("invalid color")
			return
		}
		msg, err := h.store.CreateMessage(s.showID, text, color)
		if err == nil && argBool(args, "show") {
			err = h.store.ShowMessage(s.showID, msg.ID, h.nowFn())
		}
		if err != nil {
			errOf(err)
			return
		}
		h.logAction(s, "addMsg", text)
		errOf(eng.Notify())
	case "showMsg":
		id := argInt(args, "id")
		if id <= 0 {
			s.sendErr("showMsg needs id")
			return
		}
		h.mutDone(s, eng, "showMsg", fmt.Sprintf("id %d", id), errOf,
			h.store.ShowMessage(s.showID, id, h.nowFn()))
	case "hideMsg":
		id := argInt(args, "id")
		if id <= 0 {
			s.sendErr("hideMsg needs id")
			return
		}
		h.mutDone(s, eng, "hideMsg", fmt.Sprintf("id %d", id), errOf,
			h.store.ClearMessage(s.showID, id))
	case "clearMsgs":
		// Delete everything for the show (ListMessages → per-row delete).
		msgs, lerr := h.store.ListMessages(s.showID)
		if lerr != nil {
			errOf(lerr)
			return
		}
		n := 0
		var derr error
		for _, m := range msgs {
			if derr = h.store.DeleteMessage(s.showID, m.ID); derr != nil {
				break
			}
			n++
		}
		// Partial delete still changed the panel state — fan out before the
		// error return, or every display keeps rendering removed overlays.
		if n > 0 {
			h.logAction(s, "clearMsgs", fmt.Sprintf("%d msgs", n))
			_ = eng.Notify()
		}
		errOf(derr)

	default:
		if action == "import" || action == "sync" || action == "display_cfg" {
			s.sendErr(fmt.Sprintf("%q is an HTTP route (/api/shows/...), not a WS command", action))
			return
		}
		s.sendErr(fmt.Sprintf("unknown command %q", action))
	}
}

// commandCueEdit patches the cue at pos with provided fields (partial
// update; untouched fields keep their values).
func (h *Hub) commandCueEdit(s *session, eng *timerpi.Engine, args map[string]any, errOf func(error)) {
	pos := argInt(args, "pos")
	if pos <= 0 {
		s.sendErr("cueEdit needs pos")
		return
	}
	cue, err := h.store.GetCue(s.showID, pos)
	if err != nil {
		errOf(err)
		return
	}
	if v, ok := stringArg(args, "label"); ok {
		cue.Label = v
	}
	if v, ok := int64Arg(args, "durationMS"); ok {
		cue.DurationMS = v
	}
	if v, ok := stringArg(args, "kind"); ok && (v == timerpi.KindSession || v == timerpi.KindBreak) {
		cue.Kind = v
	}
	if v, ok := stringArg(args, "tags"); ok {
		cue.Tags = v
	}
	if v, ok := stringArg(args, "speaker"); ok {
		cue.Speaker = v
	}
	if v, ok := int64Arg(args, "holdMS"); ok {
		cue.HoldMS = v
	}
	if v, ok := stringArg(args, "timerKind"); ok {
		cue.TimerKind = v
	}
	if v, ok := int64Arg(args, "alert1MS"); ok {
		cue.Alert1MS = v
	}
	if v, ok := int64Arg(args, "alert2MS"); ok {
		cue.Alert2MS = v
	}
	if v, ok := stringArg(args, "alertColor1"); ok && (v == "" || timerpi.ValidColor(v)) {
		cue.AlertColor1 = v
	}
	if v, ok := stringArg(args, "alertColor2"); ok && (v == "" || timerpi.ValidColor(v)) {
		cue.AlertColor2 = v
	}
	if v, ok := stringArg(args, "endAction"); ok {
		cue.EndAction = v
	}
	if v, ok := boolArg(args, "autoContinue"); ok {
		cue.AutoContinue = v
	}
	if v, ok := stringArg(args, "notes"); ok {
		cue.Notes = v
	}
	if v, ok := stringArg(args, "color"); ok && (v == "" || timerpi.ValidColor(v)) {
		cue.Color = v
	}
	if v, ok := stringArg(args, "startAt"); ok {
		// E5 wall-clock auto-start: "" clears, else a strict HH:MM.
		v = strings.TrimSpace(v)
		if v != "" && timerpi.DayStartTSFrom(v, h.nowFn()) == 0 {
			s.sendErr("startAt must be HH:MM 00:00-23:59")
			return
		}
		cue.StartAt = v
	}
	cue.Normalize()
	if verr := cue.Validate(); verr != nil {
		errOf(verr)
		return
	}
	if _, uerr := h.store.UpdateCue(s.showID, cue); uerr != nil {
		errOf(uerr)
		return
	}
	h.logAction(s, "cueEdit", fmt.Sprintf("pos %d %s", pos, cue.Label))
	errOf(eng.Notify())
}

// engFor resolves the session show's registry-cached engine.
func (h *Hub) engFor(s *session) *timerpi.Engine {
	if s.showID <= 0 {
		return nil
	}
	e, err := h.engines.Get(s.showID)
	if err != nil {
		return nil
	}
	return e
}

// signal relays one peer's WebRTC signaling payload to the target peer of
// the SAME show ("from" = the sender's peerId; targets resolve within the
// show only — no cross-show leakage).
func (h *Hub) signal(s *session, to string, data json.RawMessage) {
	if to == "" || len(data) == 0 {
		s.sendErr("signal needs to + data")
		return
	}
	h.mu.Lock()
	var target *session
	if sh, ok := h.byShow[s.showID]; ok {
		for ses := range sh.sessions {
			if ses.id == to {
				target = ses
				break
			}
		}
	}
	h.mu.Unlock()
	if target == nil {
		s.sendErr(fmt.Sprintf("unknown peer %q", to))
		return
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		s.sendErr("signal data must be an object")
		return
	}
	target.sendFrame("t", "signal", "from", s.id, "data", payload)
}

// ---------------------------------------------------------------------------
// Signature functions — cheap structure fingerprints deciding which oob
// fragments re-render (CONTRACT-UI §5 target table; digits never travel).

// cuesSignature fingerprints the running-order STRUCTURE (order, labels,
// durations, kinds, holds, speakers). Tick-time state (alerts, overtime)
// does not change it.
func cuesSignature(cues []timerpi.Cue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "n%d;", len(cues))
	for _, c := range cues {
		fmt.Fprintf(&b, "%d|%d|%s|%d|%s|%s|%s|%t;",
			c.Pos, c.ID, c.Label, c.DurationMS, c.Kind, c.TimerKind,
			c.Speaker, c.AutoContinue)
		if c.HoldMS > 0 {
			fmt.Fprintf(&b, "h%d;", c.HoldMS)
		}
	}
	return b.String()
}

// messagesSignature fingerprints the panel listing: which rows exist and
// their shown state (0 = queued).
func messagesSignature(msgs []views.MessageVM) string {
	var b strings.Builder
	fmt.Fprintf(&b, "n%d;", len(msgs))
	for _, m := range msgs {
		fmt.Fprintf(&b, "%d|%d|%s;", m.ID, bool01(m.IsShown), m.Text)
	}
	return b.String()
}

// scheduleSignature fingerprints everything the pure schedule depends on:
// the cue structure (via cuesSignature) PLUS the day-start anchor and rate.
// Rate never changes rows/totalMS (schedule math is rate-free per PROTOCOL
// §Engine) but is included so a `rate` jump also ships the frame as the
// client's needle/next-start refresh (REVIEW-3 D3 fix-checklist #6).
func scheduleSignature(snap timerpi.Snapshot) string {
	rt := snap.Runtime
	return fmt.Sprintf("%s|d%d;r%g", cuesSignature(snap.Cues), rt.DayStartTS, rt.Rate)
}

// currentSignature fingerprints the now/next text labels — the only part
// of frag-current a snapshot could drift (rate/digits are client UI).
func currentSignature(snap timerpi.Snapshot) string {
	var b strings.Builder
	rt := snap.Runtime
	fmt.Fprintf(&b, "a%d;p%d;", rt.ActivePos, rt.NextPos)
	for _, c := range snap.Cues {
		if c.Pos == rt.ActivePos {
			fmt.Fprintf(&b, "%s|%s|", c.Label, c.Speaker)
		}
		if c.Pos == rt.NextPos {
			fmt.Fprintf(&b, "%s|%s|", c.Label, c.Speaker)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Args helpers — JSON numbers arrive as float64; missing → ok=false.

func argInt(args map[string]any, key string) int64 {
	v, _ := int64Arg(args, key)
	return v
}

func int64Arg(args map[string]any, key string) (int64, bool) {
	switch v := args[key].(type) {
	case float64:
		return int64(v), true
	case nil:
		return 0, false
	default:
		return 0, false
	}
}

func argBool(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	case float64:
		return v != 0
	default:
		return false
	}
}

func boolArg(args map[string]any, key string) (bool, bool) {
	_, present := args[key]
	return argBool(args, key), present
}

func stringArg(args map[string]any, key string) (string, bool) {
	v, ok := args[key].(string)
	return v, ok
}

func str(args map[string]any, key string) string {
	v, _ := stringArg(args, key)
	return v
}

func bool01(b bool) int {
	if b {
		return 1
	}
	return 0
}
