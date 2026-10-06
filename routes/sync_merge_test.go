// Sync merge handler tests (docs/OFFLINE-EDIT.md): per-cue merge against a
// diverged server, the merged response shape, legacy stale refusal, and the
// legacy whole-snapshot fallback. Runs on the full wired stack (real SQLite,
// engines, hub) via newAPITest in api_test.go.
package routes_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"timerpi/timerpi"
)

// syncPost POSTs a sync body and decodes the response envelope.
func (ts *apiTest) syncPost(t *testing.T, body any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal sync body: %v", err)
	}
	code, out := ts.call("POST", "/api/shows/"+ts.showCode+"/sync", raw, "application/json")
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("sync response json: %v (%s)", err, out)
	}
	return code, env
}

// snapCues fetches the live snapshot cues as typed rows (stamps included).
func (ts *apiTest) typedCues(t *testing.T) []timerpi.Cue {
	t.Helper()
	cues, err := ts.db.ListCues(ts.showID)
	if err != nil {
		t.Fatalf("ListCues: %v", err)
	}
	return cues
}

func cueLabels(cs []timerpi.Cue) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Label
	}
	return out
}

// Diverged edit on both sides: each side's newer row wins; the response
// reports the remote survivor so the master can toast.
func TestSyncMergeDivergedEdits(t *testing.T) {
	ts := newAPITest(t)
	base := int64(1_000_000)

	a, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "A", DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "B", DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	// Fix deterministic stamps (CreateCue stamps now; rewrite for control).
	stamp := func(id, ts2 int64) {
		if _, err := ts.db.Exec(`UPDATE cues SET updated_at = ? WHERE id = ?`, ts2, id); err != nil {
			t.Fatal(err)
		}
	}
	stamp(a.ID, base)
	stamp(b.ID, base)
	if _, err := ts.db.Exec(`UPDATE shows SET updated_at = ? WHERE id = ?`, base, ts.showID); err != nil {
		t.Fatal(err)
	}

	// Meanwhile the SERVER operator edits B (newer than the master's base).
	sb, _ := ts.db.GetCue(ts.showID, 2)
	sb.Label = "B-server"
	if _, err := ts.db.UpdateCue(ts.showID, sb); err != nil {
		t.Fatal(err)
	}
	serverCues := ts.typedCues(t)
	var serverBStamp int64
	for _, c := range serverCues {
		if c.Label == "B-server" {
			serverBStamp = c.UpdatedAt
		}
	}

	// The offline master: edited A (newer than everything), never saw
	// B-server, and adds C. Its snapshot stamp is stale (base) — the merge
	// path must NOT refuse it.
	incoming := []timerpi.Cue{
		{ID: a.ID, Pos: 1, Label: "A-master", DurationMS: 60_000, Kind: "session", TimerKind: "COUNTDOWN", EndAction: "HOLD", UpdatedAt: serverBStamp + 100},
		{ID: b.ID, Pos: 2, Label: "B", DurationMS: 60_000, Kind: "session", TimerKind: "COUNTDOWN", EndAction: "HOLD", UpdatedAt: base},
		{ID: 0, Pos: 3, Label: "C", DurationMS: 30_000, Kind: "session", TimerKind: "COUNTDOWN", EndAction: "HOLD", UpdatedAt: serverBStamp + 50},
	}
	code, env := ts.syncPost(t, map[string]any{
		"updatedAt": base, "cues": incoming,
		"runtime": map[string]any{"rate": 1.0}, "messages": []any{},
	})
	if code != http.StatusOK {
		t.Fatalf("sync: %d %v", code, env)
	}
	if env["ok"] != true || env["merged"] != true {
		t.Fatalf("envelope = %v (want ok+merged)", env)
	}
	if n, _ := env["mergedCues"].(float64); n != 1 {
		t.Fatalf("mergedCues = %v, want 1 (the unseen B-server row)", env["mergedCues"])
	}
	got := cueLabels(ts.typedCues(t))
	want := []string{"A-master", "B-server", "C"}
	if len(got) != len(want) {
		t.Fatalf("cues = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cues = %v, want %v", got, want)
		}
	}
	// Snapshot carries the merged truth with per-cue stamps for the next round.
	snap := env["snapshot"].(map[string]any)
	for _, c := range snap["cues"].([]any) {
		if _, ok := c.(map[string]any)["updatedAt"]; !ok {
			t.Fatalf("merged snapshot cue lacks updatedAt: %v", c)
		}
	}
}

// Offline delete travels as a tombstone and holds against an untouched row.
func TestSyncMergeTombstoneDelete(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "A", DurationMS: 60_000}); err != nil {
		t.Fatal(err)
	}
	del, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Gone", DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	server := ts.typedCues(t)
	var aCue timerpi.Cue
	for _, c := range server {
		if c.ID != del.ID {
			aCue = c
		}
	}
	incoming := []timerpi.Cue{aCue}
	incoming[0].UpdatedAt = maxStamp(server) + 10
	tomb := []timerpi.CueTombstone{{ID: del.ID, Deleted: true, UpdatedAt: maxStamp(server) + 20}}

	code, env := ts.syncPost(t, map[string]any{
		"updatedAt": maxStamp(server), "cues": incoming, "tombstones": tomb,
		"runtime": map[string]any{"rate": 1.0}, "messages": []any{},
	})
	if code != http.StatusOK || env["ok"] != true {
		t.Fatalf("sync: %d %v", code, env)
	}
	if env["merged"] != false {
		t.Fatalf("envelope = %v (want merged:false — only the master's own delete)", env)
	}
	got := cueLabels(ts.typedCues(t))
	if len(got) != 1 || got[0] != "A" {
		t.Fatalf("cues = %v, want [A]", got)
	}
}

// The master's own ops alone never report a merge (no toast on reconnect).
func TestSyncMergeOwnOpsOnly(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "A", DurationMS: 60_000}); err != nil {
		t.Fatal(err)
	}
	server := ts.typedCues(t)
	incoming := append([]timerpi.Cue{}, server...)
	incoming = append(incoming, timerpi.Cue{Pos: 2, Label: "Encore", DurationMS: 30_000,
		Kind: "session", TimerKind: "COUNTDOWN", EndAction: "HOLD", UpdatedAt: maxStamp(server) + 5})
	code, env := ts.syncPost(t, map[string]any{
		"updatedAt": maxStamp(server) + 5, "cues": incoming,
		"runtime": map[string]any{"rate": 1.0}, "messages": []any{},
	})
	if code != http.StatusOK || env["ok"] != true {
		t.Fatalf("sync: %d %v", code, env)
	}
	if env["merged"] != false {
		t.Fatalf("envelope = %v (want merged:false)", env)
	}
	if n, _ := env["mergedCues"].(float64); n != 0 {
		t.Fatalf("mergedCues = %v, want 0", env["mergedCues"])
	}
}

// Legacy bodies (no per-cue stamps) keep whole-snapshot LWW: stale refused,
// fresh applied.
func TestSyncLegacyStaleRefused(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Server", DurationMS: 60_000}); err != nil {
		t.Fatal(err)
	}
	cur := ts.db.UpdatedStamp(ts.showID)
	// Stale legacy push (no updatedAt on cues): refused with server truth.
	code, env := ts.syncPost(t, map[string]any{
		"updatedAt": cur - 1000,
		"cues":      []any{map[string]any{"label": "Old", "durationMS": 1000}},
		"runtime":   map[string]any{"rate": 1.0}, "messages": []any{},
	})
	if code != http.StatusOK || env["ok"] != false || env["stale"] != true {
		t.Fatalf("stale legacy: %d %v (want ok:false stale:true)", code, env)
	}
	if got := cueLabels(ts.typedCues(t)); len(got) != 1 || got[0] != "Server" {
		t.Fatalf("stale push touched cues: %v", got)
	}
	// Fresh legacy push: whole replace, merged:false shape uniformity.
	code, env = ts.syncPost(t, map[string]any{
		"updatedAt": cur + 5000,
		"show":      map[string]any{"title": "API Test Show"},
		"cues":      []any{map[string]any{"label": "New", "durationMS": 1000}},
		"runtime":   map[string]any{"rate": 1.0}, "messages": []any{},
	})
	if code != http.StatusOK || env["ok"] != true {
		t.Fatalf("fresh legacy: %d %v", code, env)
	}
	if env["merged"] != false {
		t.Fatalf("legacy success envelope = %v (want merged:false)", env)
	}
	if got := cueLabels(ts.typedCues(t)); len(got) != 1 || got[0] != "New" {
		t.Fatalf("fresh legacy replace: %v", got)
	}
}

func maxStamp(cs []timerpi.Cue) int64 {
	var m int64
	for _, c := range cs {
		if c.UpdatedAt > m {
			m = c.UpdatedAt
		}
	}
	return m
}

// ------------------------------------------------------------ sync edges --

// Empty body → the legacy whole-snapshot path with all-zero fields; must
// not 500 (a bug here bricks re-syncing clients, not just one op).
func TestSyncEdgeEmptyBodyLegacy(t *testing.T) {
	ts := newAPITest(t)
	code, env := ts.syncPost(t, map[string]any{})
	if code != 200 {
		t.Fatalf("empty body sync: %d %v", code, env)
	}
	accepted, _ := env["accepted"].(bool)
	stale, _ := env["stale"].(bool)
	if !accepted && !stale {
		t.Fatalf("empty body got neither accepted nor stale: %v", env)
	}
}

// Tombstone-only master (every cue deleted offline): the merge path must
// apply and leave the show legitimately empty, resurrecting nothing.
// Stamp discipline (real sequence): the server row was last written BEFORE
// the master went dark; the master's tombstone carries its (offline-run)
// wall clock, i.e. NEWER than the stale server row — so the delete wins.
// A tombstone OLDER than a live server row correctly loses (that cue was
// edited server-side after the master's view)) — covered separately below.
func TestSyncEdgeTombstoneOnlyMaster(t *testing.T) {
	ts := newAPITest(t)
	cue, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "kickoff", DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	// Backdate the row going dark, like the real world: the server has not
	// touched cues since before the master went offline.
	if _, err := ts.db.Exec(`UPDATE cues SET updated_at = ? WHERE id = ?`, 500, cue.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.db.Exec(`UPDATE shows SET updated_at = ? WHERE id = ?`, 5, ts.showID); err != nil {
		t.Fatal(err)
	}
	code, env := ts.syncPost(t, map[string]any{
		"updatedAt":  9999999,
		"tombstones": []timerpi.CueTombstone{{ID: cue.ID, Deleted: true, UpdatedAt: 1_000_000}},
	})
	if code != 200 {
		t.Fatalf("tombstone-only sync: %d %v (live cue stamp=%d)", code, env, cue.UpdatedAt)
	}
	cues := ts.typedCues(t)
	if len(cues) != 0 {
		t.Fatalf("tombstone-only master resurrected: %+v (cue=%d)", cues, cue.ID)
	}
}

// Corollary edge: a tombstone OLDER than a server-side edit loses politely
// (the server edited the cue after the master's delete); the cue survives
// and the master's snapshot is served back so it converges.
func TestSyncEdgeStaleTombstoneLosesToServerEdit(t *testing.T) {
	ts := newAPITest(t)
	cue, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "kickoff", DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	// Server edited the cue NOW (fresh row stamp beats the master's delete).
	code, env := ts.syncPost(t, map[string]any{
		"updatedAt":  9999999,
		"tombstones": []timerpi.CueTombstone{{ID: cue.ID, Deleted: true, UpdatedAt: 1_000_000}},
	})
	if code != 200 {
		t.Fatalf("stale tombstone sync: %d %v", code, env)
	}
	cues := ts.typedCues(t)
	if len(cues) != 1 || cues[0].Label != "kickoff" {
		t.Fatalf("server edit lost to stale tombstone: %+v", cues)
	}
}

// Adversarial share-code ident on the sync route: everything malformed gets
// a clean 404, never a panic or a template leak.
func TestSyncEdgeAdversarialIdents(t *testing.T) {
	ts := newAPITest(t)
	for _, ident := range []string{"%20%20", "K7QPM3XBXXL", "K7QP%00M", "........", "---"} {
		code, out := ts.call("POST", "/api/shows/"+ident+"/sync", []byte("{}"), "application/json")
		if code != 404 {
			t.Fatalf("adversarial ident %q: %d %s, want 404", ident, code, out)
		}
	}
}

// BUGLOG RC6: a sync push delivered twice (lost response, retry) must not
// duplicate the running order. Cue IDs survive the merge, so the second
// delivery is recognised; the ID-less offline add is recognised by its
// content and stamp.
func TestSyncMergeRetryIsIdempotent(t *testing.T) {
	ts := newAPITest(t)
	a, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "A", DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "B", DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	stamp := a.UpdatedAt + 1_000
	body := map[string]any{
		"updatedAt": stamp,
		"cues": []timerpi.Cue{
			{ID: a.ID, Pos: 1, Label: "A2", DurationMS: 60_000, Kind: "session", TimerKind: "COUNTDOWN", EndAction: "HOLD", UpdatedAt: stamp},
			{ID: b.ID, Pos: 2, Label: "B", DurationMS: 60_000, Kind: "session", TimerKind: "COUNTDOWN", EndAction: "HOLD", UpdatedAt: b.UpdatedAt},
			{ID: 0, Pos: 3, Label: "C", DurationMS: 30_000, Kind: "session", TimerKind: "COUNTDOWN", EndAction: "HOLD", UpdatedAt: stamp},
		},
		"runtime": map[string]any{"rate": 1.0}, "messages": []any{},
	}
	for i := 0; i < 2; i++ {
		if code, env := ts.syncPost(t, body); code != http.StatusOK || env["ok"] != true {
			t.Fatalf("sync #%d: %d %v", i+1, code, env)
		}
	}
	cues := ts.typedCues(t)
	got := cueLabels(cues)
	if len(got) != 3 || got[0] != "A2" || got[1] != "B" || got[2] != "C" {
		t.Fatalf("after two identical pushes cues = %v, want [A2 B C]", got)
	}
	if cues[0].ID != a.ID || cues[1].ID != b.ID {
		t.Fatalf("cue IDs changed across sync: %d,%d want %d,%d", cues[0].ID, cues[1].ID, a.ID, b.ID)
	}
}
