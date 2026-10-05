# TimerPi — full offline editing (mesh master rewrites the running order while the server is dark)

Product decision: operators may rewrite the running order while the server
is unreachable. The mesh master executes cue CRUD locally and the server
MERGES the diverged list on reconnect (per-cue last-writer-wins +
tombstones), instead of the old whole-snapshot LWW which would silently drop
one side's work.

## 1. Rules

- While the server link is down, the elected master executes `cueAdd /
  cueEdit / cueDel / cueMove / cueDup` locally in `public/src/engine.js`
  (same semantics as the server's WS `cue.*` commands in `ws/commands.go` +
  `timerpi/db.go`; see §5 for the deliberate deltas).
- Every content-touched cue gets a fresh `updatedAt` on the server-anchored
  clock (`mesh.now()` = `Date.now() + clockOffset`, monotonic
  `max(stamp+1, now)`); the snapshot `updatedAt` bumps as before and the
  master sets `_advancedOffline`, so the existing reconnect push fires.
- Deletes travel as tombstones `{id, deleted:true, updatedAt}` on
  `snap.tombstones` (mesh-propagated, capped at the last 200).
- On reconnect the master POSTs the diverged list; the server runs
  `timerpi.MergeCues` (per-cue LWW, tombstone suppression, dense Pos
  renumber) and fans the result out through the EXISTING oob/state fanout —
  no new protocol frames.
- After a merge that kept anything beyond the master's own ops, the sync
  response reports `merged:true, mergedCues:N` and the master toasts
  "Merged N remote change(s)" (existing toast helper, no new UI surface).

## 2. Wire shapes

Sync request (additive on top of the PROTOCOL snapshot body):

```json
{
  "updatedAt": 1720000000000,
  "show": { "title": "…" },
  "runtime": { "rate": 1.0, … },
  "cues": [
    { "id": 12, "pos": 1, "label": "Welcome", "durationMS": 600000, …,
      "updatedAt": 1720000000100 }
  ],
  "messages": [ … ],
  "tombstones": [ { "id": 9, "deleted": true, "updatedAt": 1720000000200 } ]
}
```

- Per-cue `updatedAt` rides every cue object (server snapshots now emit it;
  pre-change readers ignore unknown fields). A body with ANY per-cue stamp
  or any tombstone takes the merge path; a body with neither is an old
  client and keeps the legacy whole-snapshot LWW (including stale refusal).
- Sync response, merge path: `{ok:true, merged:true|false,
  mergedCues:N, snapshot:{…}}`. Legacy success also carries
  `merged:false, mergedCues:0` (shape uniformity). Legacy stale refusal is
  unchanged: `{ok:false, stale:true, snapshot:{…}}`.

## 3. Merge rule (`timerpi.MergeCues`)

Per cue ID: newer `UpdatedAt` wins; **ties go to INCOMING** (the master's
offline intent survives a server-idle race — pure positional shifts from an
insert/delete keep their old stamp, so a tie is exactly "nobody touched the
content"). Server-only rows are kept unless a tombstone at least as new
covers them (delete wins); a newer server edit resurrects (edit wins).
Incoming-only rows (`id` unknown or non-positive temp) are offline adds,
unless a newer same-ID tombstone collapses an add-then-delete. Survivors
sort by `(Pos, ID)` and renumber densely `1..N`.

`remoteCount` (reported as `mergedCues`) = survivors sourced from the
SERVER side that the incoming list did not already match (kept server-only
rows, server-won conflicts, resurrections). The master's own ops (its adds,
its honoured deletes, its won conflicts) never count — no toast when there
was nothing remote.

Runtime / messages / title have no per-field stamps: they stay
snapshot-LWW and are applied only when the body's snapshot stamp is at
least as new as the server's; otherwise the server's newer transport state
stands while cues still merge per cue.

## 4. Conflict examples

1. Master edits A's label offline; server operator edits B's label.
   → Both win (disjoint rows). `merged:true, mergedCues:1` (B was remote).
2. Both edit A; master newer. → A's master version wins, `merged:false`.
3. Master deletes B offline (tombstone T200); server untouched (B@T100).
   → B stays deleted, `merged:false`.
4. Master deletes B (T200); server operator edited B (T300).
   → B resurrects with the server edit, `merged:true, mergedCues:1`.
5. Master reorders B→top offline; server idle. → B wins by newer stamp, the
   rest tie → incoming wins ties → the master's order survives whole.
6. Master appends "Encore"; server added "Late" meanwhile.
   → Union: […, Late?, Encore?] ordered by winner positions, dense.
7. TRUE SPLIT-BRAIN (accepted, §6): both sides move DIFFERENT cues, or a
   remote content edit lands on a locally-shifted row — each cue keeps its
   winner's position and the merged order may INTERLEAVE.

## 5. Client semantics parity (`engine.js` vs server)

| op | server (online) | offline master mirror |
|---|---|---|
| cueAdd | needs label; dur from `durationMS` else `mss`; only label/duration/kind read; pos insert or append | same (the dashboard form converts `mss`→`durationMS` before `sendCommand`, so both paths see it); temp negative id; new row stamped |
| cueEdit | partial allowlist; bad kind/color skipped, bad timerKind/endAction fails the op | same; touched row stamped |
| cueDel | needs pos; renumbers 1..N | same + tombstone (server ids only; temp rows collapse silently) |
| cueMove | pos + dir up\|down; edge = no-op success | same swap; edge = `ignored` (avoids a pointless sync); BOTH swapped rows stamped |
| cueDup | copy inserted directly after | same; fresh temp id; copy stamped |

Insert/delete positional shifts do NOT restamp (§3 tie rule). Every applied
op bumps `snap.updatedAt` and sets `_advancedOffline`.

## 6. Operator-visible behavior

- Offline cue edits apply instantly on the master's pages and propagate to
  mesh peers over the existing `mesh-state` broadcast (tombstones included,
  so a mastership handoff keeps the deletes).
- On reconnect the pages repaint via the normal `state`/oob fanout; if the
  server held concurrent changes, the master toasts
  "Merged N remote change(s)".
- Accepted split-brain caveat: under a TRUE concurrent reorder on both
  sides the merged running order may interleave (per-cue positions win
  independently) — the operator re-drags if the order matters.

## 7. Limits

- **200 tombstones**: client caps before sending, server caps defensively
  (newest kept). A delete older than the cap window is forgotten and the
  row may resurrect on a later merge.
- **Dense renumber**: merged positions are always `1..N`; no gaps.
- **Clock skew**: stamps use the server-anchored clock (`serverTime` offset
  re-anchored on every snapshot/pong). Skew >2s between master and server
  degrades comparisons toward the incoming-wins tiebreak — documented, not
  solved (same class as the existing whole-snapshot LWW).
- **Server-side moves don't restamp** (`MoveCue` only renumbers): a
  concurrent offline CONTENT edit to the same cue overrides the server's
  move of that cue. Edits to untouched rows always merge cleanly.
- **cueAdd carries label/duration/kind only** (server parity) — full-field
  offline creation is a later step; use cueEdit after add for the rest.
- **Runtime/messages/title stay snapshot-LWW** (§3); cue list is the only
  per-field merged domain.
- **E5 `startAt` is merged cue content** like notes/color (equality +
  stamps cover it): an offline timer edit merges per the usual LWW rules;
  the engine evaluates it on the next tick after adoption.
- **Server-side full replace during a partition duplicates**: PUT /cues,
  dashboard import (replace) and show-file import re-create every row with
  fresh IDs. A master holding pre-replace IDs then merges its rows as
  unknown-ID adds alongside the replaced list — the running order doubles
  and the operator deletes the surplus. WS-command edits (the normal
  multi-operator path) keep IDs stable and merge cleanly; a whole-list
  rewrite racing an offline editor is split-brain on the LIST, reconciled
  loudly rather than silently.

## 8. Verification

- `go test ./timerpi/ ./routes/ -count=1` (merge unit tests + sync handler
  tests: diverged edits, tombstone delete, own-ops-only, legacy stale +
  legacy fallback).
- `/tmp/offline-harness/offline_engine.test.mjs` — `node --test` parity
  spot-checks for `engine.js` (repo has no JS runner; the harness copies
  `public/src/engine.js` verbatim beside a `{"type":"module"}` package.json;
  re-copy after editing).
- Throwaway-port live probe: two divergent cue sets → merged snapshot
  (never against `:80` data), then `systemctl restart timerpi` + `:80`
  smoke curls. (The original evidence log was not kept in the repo.)
