package timerpi

import (
	"testing"
)

// mergeCue builds a server/incoming cue with an explicit stamp (stamps ride
// Cue.UpdatedAt in memory, decoded from the sync body's per-cue updatedAt).
func mergeCue(id, pos int64, label string, stamp int64) Cue {
	c := Cue{ID: id, Pos: pos, Label: label, DurationMS: 60_000,
		Kind: KindSession, TimerKind: TimerCountdown, EndAction: EndHold}
	c.Normalize()
	c.UpdatedAt = stamp
	return c
}

func mergeLabels(cs []Cue) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Label
	}
	return out
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertDense(t *testing.T, cs []Cue) {
	t.Helper()
	for i, c := range cs {
		if c.Pos != int64(i+1) {
			t.Fatalf("pos not dense 1..N at index %d: pos=%d (%+v)", i, c.Pos, cs)
		}
	}
}

// Add-vs-add: disjoint offline + server rows union.
func TestMergeAddVsAdd(t *testing.T) {
	server := []Cue{mergeCue(1, 1, "A", 100), mergeCue(2, 2, "B", 100)}
	incoming := []Cue{mergeCue(1, 1, "A", 100), mergeCue(2, 2, "B", 100),
		mergeCue(0, 3, "Encore", 200)}
	merged, remote := MergeCues(server, incoming, nil)
	if !equalStr(mergeLabels(merged), []string{"A", "B", "Encore"}) {
		t.Fatalf("labels = %v", mergeLabels(merged))
	}
	assertDense(t, merged)
	if remote != 0 {
		t.Fatalf("remote = %d, want 0 (only the master's own add)", remote)
	}
}

// Edit-vs-edit: newer stamp wins, either direction.
func TestMergeEditNewerWins(t *testing.T) {
	mk := func(serverStamp, inStamp int64, serverLabel, inLabel string) (Cue, int) {
		m, r := MergeCues(
			[]Cue{mergeCue(1, 1, serverLabel, serverStamp)},
			[]Cue{mergeCue(1, 1, inLabel, inStamp)}, nil)
		return m[0], r
	}
	// Incoming newer → master's edit wins, nothing remote.
	if got, r := mk(100, 200, "S", "M"); got.Label != "M" || r != 0 {
		t.Fatalf("incoming-newer: %+v remote=%d", got, r)
	}
	// Server newer → server edit wins and counts as remote.
	if got, r := mk(300, 200, "S", "M"); got.Label != "S" || r != 1 {
		t.Fatalf("server-newer: %+v remote=%d", got, r)
	}
}

// Tie (equal stamps, e.g. pure positional shift vs idle server) → incoming.
func TestMergeTieIncomingWins(t *testing.T) {
	merged, remote := MergeCues(
		[]Cue{mergeCue(1, 1, "A", 100), mergeCue(2, 2, "B", 100)},
		[]Cue{mergeCue(2, 1, "B", 100), mergeCue(1, 2, "A", 100)}, nil)
	if !equalStr(mergeLabels(merged), []string{"B", "A"}) {
		t.Fatalf("tie reorder lost: %v", mergeLabels(merged))
	}
	assertDense(t, merged)
	if remote != 0 {
		t.Fatalf("remote = %d, want 0 (identical content, order is master's)", remote)
	}
}

// Delete-vs-edit, tombstone newer → the delete holds.
func TestMergeDeleteBeatsOlderEdit(t *testing.T) {
	server := []Cue{mergeCue(1, 1, "A", 100), mergeCue(2, 2, "B", 100)}
	incoming := []Cue{mergeCue(1, 1, "A", 100)} // master deleted B (pos 2)
	tombs := []CueTombstone{{ID: 2, Deleted: true, UpdatedAt: 200}}
	merged, remote := MergeCues(server, incoming, tombs)
	if !equalStr(mergeLabels(merged), []string{"A"}) {
		t.Fatalf("labels = %v", mergeLabels(merged))
	}
	assertDense(t, merged)
	if remote != 0 {
		t.Fatalf("remote = %d, want 0 (master's own delete)", remote)
	}
}

// Delete-vs-edit, server edit newer than the tombstone → resurrect.
func TestMergeNewerEditBeatsDelete(t *testing.T) {
	server := []Cue{mergeCue(1, 1, "A", 100), mergeCue(2, 2, "B-edited", 300)}
	incoming := []Cue{mergeCue(1, 1, "A", 100)}
	tombs := []CueTombstone{{ID: 2, Deleted: true, UpdatedAt: 200}}
	merged, remote := MergeCues(server, incoming, tombs)
	if !equalStr(mergeLabels(merged), []string{"A", "B-edited"}) {
		t.Fatalf("labels = %v (want the newer server edit resurrected)", mergeLabels(merged))
	}
	if remote != 1 {
		t.Fatalf("remote = %d, want 1 (resurrection is a remote change)", remote)
	}
}

// Server-only row the master never saw is kept and reported.
func TestMergeServerOnlyRowKept(t *testing.T) {
	server := []Cue{mergeCue(1, 1, "A", 100), mergeCue(9, 2, "Late-add", 150)}
	incoming := []Cue{mergeCue(1, 1, "A", 100)}
	merged, remote := MergeCues(server, incoming, nil)
	if !equalStr(mergeLabels(merged), []string{"A", "Late-add"}) {
		t.Fatalf("labels = %v", mergeLabels(merged))
	}
	if remote != 1 {
		t.Fatalf("remote = %d, want 1", remote)
	}
}

// Added-then-deleted offline collapses (no resurrection of a temp row).
func TestMergeAddThenDeleteCollapses(t *testing.T) {
	server := []Cue{mergeCue(1, 1, "A", 100)}
	incoming := []Cue{mergeCue(1, 1, "A", 100), mergeCue(-7, 2, "scratch", 200)}
	tombs := []CueTombstone{{ID: -7, Deleted: true, UpdatedAt: 201}}
	merged, _ := MergeCues(server, incoming, tombs)
	if !equalStr(mergeLabels(merged), []string{"A"}) {
		t.Fatalf("labels = %v", mergeLabels(merged))
	}
}

// Reorder interleave: both sides move DIFFERENT cues; each cue keeps its
// winner's position (accepted split-brain — documented, not solved).
func TestMergeReorderInterleave(t *testing.T) {
	// Base order A B C. Server moves C to top (C stamped by... server moves
	// don't restamp, but the test drives the rule directly: C carries the
	// server's new pos with its old stamp vs untouched incoming copies).
	server := []Cue{mergeCue(3, 1, "C", 100), mergeCue(1, 2, "A", 100), mergeCue(2, 3, "B", 100)}
	// Master offline moves B to top instead (B stamped new).
	incoming := []Cue{mergeCue(2, 1, "B", 200), mergeCue(1, 2, "A", 100), mergeCue(3, 3, "C", 100)}
	merged, _ := MergeCues(server, incoming, nil)
	assertDense(t, merged)
	if len(merged) != 3 {
		t.Fatalf("merged = %v", mergeLabels(merged))
	}
	// B took the master's (newer) pos 1; A/C tie → incoming wins ties, so
	// incoming's C@3 beats server's C@1 only where stamps tie: C stamps tie
	// (100==100) → incoming C@3. Final: B A C — the master's order, because
	// only B actually diverged by stamp.
	if !equalStr(mergeLabels(merged), []string{"B", "A", "C"}) {
		t.Fatalf("labels = %v", mergeLabels(merged))
	}
}

// True divergent-move interleave: both sides restamp the SAME cue's move
// differently — per-cue LWW picks one position; order reflects the winners.
func TestMergeDivergentSameCueMove(t *testing.T) {
	server := []Cue{mergeCue(1, 2, "A", 150), mergeCue(2, 1, "B", 100)}
	incoming := []Cue{mergeCue(1, 1, "A", 160), mergeCue(2, 2, "B", 100)}
	merged, _ := MergeCues(server, incoming, nil)
	assertDense(t, merged)
	// A: incoming newer → A@1. B ties → incoming B@2. Master's order wins.
	if !equalStr(mergeLabels(merged), []string{"A", "B"}) {
		t.Fatalf("labels = %v", mergeLabels(merged))
	}
}

// Tombstone cap: only the newest 200 survive.
func TestMergeTombstoneCap(t *testing.T) {
	var tombs []CueTombstone
	for i := int64(1); i <= 250; i++ {
		tombs = append(tombs, CueTombstone{ID: i, Deleted: true, UpdatedAt: i})
	}
	kept := CapTombstones(tombs, MaxSyncTombstones)
	if len(kept) != 200 {
		t.Fatalf("kept = %d, want 200", len(kept))
	}
	for _, k := range kept {
		if k.UpdatedAt <= 50 {
			t.Fatalf("stale tombstone survived: %+v", k)
		}
	}
	// And MergeCues honours the cap: a delete older than the cap window is
	// forgotten → the server row resurrects (documented limit).
	server := []Cue{mergeCue(1, 1, "Ancient", 1)}
	merged, _ := MergeCues(server, nil, tombs)
	if len(merged) != 1 || merged[0].Label != "Ancient" {
		t.Fatalf("over-cap delete should be forgotten, merged = %+v", merged)
	}
}

// Zero-stamp bodies (old clients) degrade to incoming-wins everywhere —
// the handler keeps those bodies on the legacy whole-snapshot path;
// at the merge level this documents the fallback shape.
func TestMergeZeroStampFallback(t *testing.T) {
	server := []Cue{mergeCue(1, 1, "S", 0)}
	incoming := []Cue{mergeCue(1, 1, "M", 0)}
	merged, _ := MergeCues(server, incoming, nil)
	if merged[0].Label != "M" {
		t.Fatalf("zero-stamp tie should go incoming: %+v", merged[0])
	}
}

// ReplaceCuesStamped preserves stamps for the next offline round.
func TestReplaceCuesStamped(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Stamped")
	a := sampleCue(0, "a")
	a.UpdatedAt = 111
	b := sampleCue(0, "b") // 0 → now
	if err := d.ReplaceCuesStamped(show.ID, []Cue{a, b}); err != nil {
		t.Fatalf("ReplaceCuesStamped: %v", err)
	}
	got, err := d.ListCues(show.ID)
	if err != nil || len(got) != 2 {
		t.Fatalf("list: %+v %v", got, err)
	}
	if got[0].UpdatedAt != 111 {
		t.Fatalf("stamp lost: %+v", got[0])
	}
	if got[1].UpdatedAt == 0 {
		t.Fatalf("zero stamp should default to now: %+v", got[1])
	}
	assertOrder(t, d, show.ID, []string{"a", "b"})
}

// ------------------------------------------------------------ merge edges --

// Tombstone expiry semantics: a cue deleted offline while the server ALSO
// deleted it later must stay deleted; resurrection attempts are suppressed
// by the tombstone regardless of incoming stamp.
func TestMergeTombstoneSuppressesResurrection(t *testing.T) {
	server := []Cue{mergeCue(1, 1, "S-kept", 0)}
	// Server deleted cue 2; incoming mesh still has cue 2 (older view).
	incoming := []Cue{
		mergeCue(1, 2, "M-stale", 0),
	}
	tombs := []CueTombstone{{ID: 2, Deleted: true, UpdatedAt: 9999}}
	merged, _ := MergeCues(server, incoming, tombs)
	for _, c := range merged {
		if c.ID == 2 {
			t.Fatalf("tombstoned cue resurrected: %+v", c)
		}
	}
	if len(merged) != 1 {
		t.Fatalf("merged = %+v, want only the server cue", merged)
	}
}

// Empty-everything sync: no cues, no tombstones — merge yields an empty
// order without panicking or inventing IDs (empty shows are legal).
func TestMergeEmptyEverything(t *testing.T) {
	merged, count := MergeCues(nil, nil, nil)
	if len(merged) != 0 || count != 0 {
		t.Fatalf("empty merge: %d cues, count %d", len(merged), count)
	}
	// Server-only cues with empty incoming: merge keeps them.
	merged, _ = MergeCues([]Cue{mergeCue(1, 1, "Keep", 10)}, nil, nil)
	if len(merged) != 1 || merged[0].Label != "Keep" {
		t.Fatalf("server-only merge: %+v", merged)
	}
}

// CapTombstones of exactly-max → identity; max-1 over → keep NEWEST max.
func TestMergeTombstoneCapBoundary(t *testing.T) {
	many := make([]CueTombstone, 200)
	for i := range many {
		many[i] = CueTombstone{ID: int64(i + 1), UpdatedAt: int64(i)}
	}
	if got := CapTombstones(many, 200); !tombIDEqual(got, many) {
		t.Fatalf("at-cap copy mismatch: %d", len(got))
	}
	many = append(many, CueTombstone{ID: 201, UpdatedAt: 500})
	got := CapTombstones(many, 200)
	if len(got) != 200 || got[0].ID != 201 || got[0].UpdatedAt != 500 {
		t.Fatalf("cap kept wrong slice: first = %+v, len %d", got[0], len(got))
	}
}

func tombIDEqual(a, b []CueTombstone) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].UpdatedAt != b[i].UpdatedAt {
			return false
		}
	}
	return true
}
