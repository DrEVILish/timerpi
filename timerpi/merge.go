// Package timerpi — merge.go: offline-edit cue merge (docs/OFFLINE-EDIT.md).
//
// A mesh master that kept operating while the server was unreachable pushes
// its diverged cue list back via POST /api/shows/:code/sync. The body
// carries per-cue `updatedAt` stamps plus tombstones for offline deletes, so
// the server can merge per cue instead of the legacy whole-snapshot
// last-writer-wins. This file is ADDITIVE: no existing behaviour changes.
//
// Merge rule (per cue ID):
//   - both sides have the row: newer UpdatedAt wins; ties go to INCOMING
//     (the master's offline intent survives a server-idle race; pure
//     positional shifts from an insert/delete keep their old stamp, so a
//     tie is exactly "nobody touched the content").
//   - server-only row: kept, UNLESS an incoming tombstone covers it and the
//     tombstone is at least as new as the row (delete wins) — otherwise the
//     row is resurrected (a newer server edit beats an older delete).
//   - incoming-only row (unknown or non-positive ID): the master's offline
//     add — kept as a new row (ID cleared for insert), unless a tombstone
//     for the same ID is newer (added-then-deleted offline collapses).
//   - survivors are sorted by (Pos, ID) and renumbered densely 1..N.
//
// Accepted split-brain caveat: concurrent reorders on both sides resolve
// per cue, so the merged order may INTERLEAVE (each cue keeps its winner's
// position). Documented in docs/OFFLINE-EDIT.md, not solved.
//
// Tombstones are capped at MaxSyncTombstones (200, highest UpdatedAt kept);
// anything older is forgotten, so a cue deleted more than 200 deletes ago
// may resurrect on a later merge (documented limit).
package timerpi

import (
	"sort"
)

// MaxSyncTombstones caps the delete tombstones travelling in one sync body
// (and held client-side). Oldest-by-stamp entries drop first.
const MaxSyncTombstones = 200

// CueTombstone is an offline delete travelling in the sync body:
// {id, deleted:true, updatedAt}.
type CueTombstone struct {
	ID        int64 `json:"id"`
	Deleted   bool  `json:"deleted"`
	UpdatedAt int64 `json:"updatedAt"`
}

// CapTombstones keeps the highest-stamped max entries (defensive; the
// client caps at 200 before sending — docs/OFFLINE-EDIT.md §limits).
func CapTombstones(ts []CueTombstone, max int) []CueTombstone {
	if max <= 0 || len(ts) <= max {
		return ts
	}
	cp := make([]CueTombstone, len(ts))
	copy(cp, ts)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].UpdatedAt != cp[j].UpdatedAt {
			return cp[i].UpdatedAt > cp[j].UpdatedAt
		}
		return cp[i].ID > cp[j].ID
	})
	return cp[:max]
}

// MergeCues merges an offline master's diverged cue list into the server's.
// Stamps ride in memory on Cue.UpdatedAt (populated from the sync body's
// per-cue `updatedAt`; the column already exists — no schema change).
//
// Returns the merged list (dense Pos 1..N) and remoteCount: the number of
// survivors sourced from the SERVER side that the incoming list did not
// already match — i.e. "changed anything beyond the master's own ops".
// The sync handler reports merged = remoteCount > 0 so the master can toast.
func MergeCues(serverCues, incomingCues []Cue, tombstones []CueTombstone) (merged []Cue, remoteCount int) {
	tombs := CapTombstones(tombstones, MaxSyncTombstones)
	byTomb := make(map[int64]CueTombstone, len(tombs))
	for _, t := range tombs {
		if !t.Deleted {
			continue
		}
		if cur, ok := byTomb[t.ID]; !ok || t.UpdatedAt > cur.UpdatedAt {
			byTomb[t.ID] = t
		}
	}

	byServer := make(map[int64]Cue, len(serverCues))
	for _, c := range serverCues {
		if c.ID != 0 {
			byServer[c.ID] = c
		}
	}
	byIncoming := make(map[int64]Cue, len(incomingCues))
	var firstSeen []int64            // incoming-ID order: map iteration is random, and
	for _, c := range incomingCues { // same-Pos adds must stay deterministic
		if c.ID != 0 {
			if cur, dup := byIncoming[c.ID]; !dup {
				byIncoming[c.ID] = c
				firstSeen = append(firstSeen, c.ID)
			} else if c.UpdatedAt >= cur.UpdatedAt {
				byIncoming[c.ID] = c
			}
		}
	}
	var adds []Cue // incoming rows unknown to the server (offline adds)
	for _, id := range firstSeen {
		c := byIncoming[id]
		// Determine fate for this incoming ID
		if _, ok := byServer[c.ID]; ok {
			continue // goes to the update branch against server
		}
		// Positive but unknown ID: treat as an add (re-created on insert);
		// a same-ID tombstone still collapses add-then-delete.
		if t, ok := byTomb[c.ID]; ok && t.UpdatedAt >= c.UpdatedAt {
			continue
		}
		cc := c
		cc.ID = 0
		adds = append(adds, cc)
	}
	for _, c := range incomingCues {
		if c.ID != 0 {
			continue // already processed
		}
		adds = append(adds, c) // non-positive ID (offline add)
	}

	// A retried push resends its offline adds with no ID. One the server
	// already holds (same content, same non-zero stamp) is not added again
	// (BUGLOG RC6); that server row is the master's own op, not remote.
	applied := map[int64]bool{}
	kept := adds[:0]
	for _, a := range adds {
		dup := false
		if a.UpdatedAt != 0 {
			for id, s := range byServer {
				if !applied[id] && s.UpdatedAt == a.UpdatedAt && cuesSameContent(s, a) {
					applied[id], dup = true, true
					break
				}
			}
		}
		if !dup {
			kept = append(kept, a)
		}
	}
	adds = kept

	out := make([]Cue, 0, len(serverCues)+len(adds))
	for id, s := range byServer {
		if applied[id] {
			out = append(out, s)
			continue
		}
		in, ok := byIncoming[id]
		if ok {
			// Both sides have the row: newer stamp wins, ties → incoming.
			if in.UpdatedAt >= s.UpdatedAt {
				out = append(out, in)
			} else {
				out = append(out, s)
				if !cuesEqualContent(s, in) {
					remoteCount++
				}
			}
			continue
		}
		// Server-only row: tombstone decides delete-vs-keep.
		if t, ok := byTomb[id]; ok && t.UpdatedAt >= s.UpdatedAt {
			continue // master's delete honoured (its own op: not remote)
		}
		out = append(out, s)
		if _, ok := byTomb[id]; ok {
			remoteCount++ // resurrected: server edit beat the delete
		} else {
			remoteCount++ // server-side row the master never saw
		}
	}
	out = append(out, adds...)

	// Dense renumber preserving the winners' relative order. Ties on Pos
	// (two winners claiming one slot after divergent reorders, or an add
	// landing on an occupied slot) break by ID for determinism — this is
	// the accepted interleave point.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pos != out[j].Pos {
			return out[i].Pos < out[j].Pos
		}
		return out[i].ID < out[j].ID
	})
	for i := range out {
		out[i].Pos = int64(i + 1)
	}
	if out == nil {
		out = []Cue{}
	}
	return out, remoteCount
}

// ReplaceCuesStamped makes the show's cue list exactly cues, in order, but
// each row keeps its in-memory UpdatedAt stamp (0 → now). The sync merge
// path uses it so per-cue stamps survive for the NEXT offline round.
//
// Rows are upserted by ID (BUGLOG RC6): a cue whose ID already belongs to
// this show is updated in place, ID 0 (or a foreign ID) is inserted, and
// only rows missing from cues are deleted. Cue IDs therefore stay stable
// across syncs, so a retried or second push is recognised instead of being
// re-added as "offline adds". All other writers keep ReplaceCues.
func (d *DB) ReplaceCuesStamped(showID int64, cues []Cue) error {
	defer d.cuesChanged(showID)
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing []int64
	if err := tx.Select(&existing, `SELECT id FROM cues WHERE show_id = ?`, showID); err != nil {
		return err
	}
	have := make(map[int64]bool, len(existing))
	for _, id := range existing {
		have[id] = true
	}
	keep := make(map[int64]bool, len(cues))
	for _, c := range cues {
		if c.ID > 0 && have[c.ID] {
			keep[c.ID] = true
		}
	}
	for _, id := range existing {
		if !keep[id] {
			if _, err := tx.Exec(`DELETE FROM cues WHERE id = ? AND show_id = ?`, id, showID); err != nil {
				return err
			}
		}
	}
	now := nowMS()
	ids := make([]int64, 0, len(cues))
	for i, c := range cues {
		c.ShowID = showID
		c.Normalize()
		if err := c.Validate(); err != nil {
			return err
		}
		if c.Day < 1 {
			c.Day = 1
		}
		stamp := c.UpdatedAt
		if stamp == 0 {
			stamp = now
		}
		if keep[c.ID] {
			if _, err := tx.Exec(`UPDATE cues SET
				label = ?, duration_ms = ?, kind = ?, tags = ?, speaker = ?, hold_ms = ?,
				timer_kind = ?, alert1_ms = ?, alert2_ms = ?, alert_color1 = ?, alert_color2 = ?,
				end_action = ?, autocontinue = ?, notes = ?, color = ?, start_at = ?, day = ?,
				location = ?, updated_at = ?
				WHERE id = ? AND show_id = ?`,
				c.Label, c.DurationMS, c.Kind, c.Tags, c.Speaker, c.HoldMS,
				c.TimerKind, c.Alert1MS, c.Alert2MS, c.AlertColor1, c.AlertColor2,
				c.EndAction, b2i(c.AutoContinue), c.Notes, c.Color, c.StartAt, c.Day,
				c.Location, stamp, c.ID, showID); err != nil {
				return err
			}
			delete(keep, c.ID) // a duplicate ID later in cues is inserted fresh
			ids = append(ids, c.ID)
			continue
		}
		res, err := insertCue(tx, showID, int64(-(i + 1)), c, stamp)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := applyOrder(tx, showID, ids); err != nil {
		return err
	}
	return tx.Commit()
}

// cuesSameContent is cuesEqualContent ignoring position.
// Both sides are normalised first: the stored row was, the pushed one may
// not be.
func cuesSameContent(a, b Cue) bool {
	a.Normalize()
	b.Normalize()
	b.Pos = a.Pos
	return cuesEqualContent(a, b)
}

// cuesEqualContent compares the operator-visible fields (identity excluded
// — the caller already knows the IDs match; Pos included so a pure
// server-side reorder of an otherwise untouched row still counts as a
// remote change worth reporting).
func cuesEqualContent(a, b Cue) bool {
	return a.Label == b.Label &&
		a.DurationMS == b.DurationMS &&
		a.Kind == b.Kind &&
		a.Tags == b.Tags &&
		a.Speaker == b.Speaker &&
		a.HoldMS == b.HoldMS &&
		a.TimerKind == b.TimerKind &&
		a.Alert1MS == b.Alert1MS &&
		a.Alert2MS == b.Alert2MS &&
		a.AlertColor1 == b.AlertColor1 &&
		a.AlertColor2 == b.AlertColor2 &&
		a.EndAction == b.EndAction &&
		a.AutoContinue == b.AutoContinue &&
		a.Notes == b.Notes &&
		a.Color == b.Color &&
		a.StartAt == b.StartAt &&
		a.Location == b.Location &&
		a.Pos == b.Pos
}
