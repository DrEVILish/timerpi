# NOTES-drm (Agent F handoff — 2026-10-03)

## What landed

- `drm/` package, all flags green: `go build ./drm/...`,
  `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./drm/...`,
  `go test ./drm/ -count=1` (22 tests), `go vet ./drm/` clean,
  `gofmt -l drm/` empty. Full details: `drm/README.md`.

- Asset regeneration (see risk #1 below): `drm/assets/atlas-*.png`
  and `metrics.json` are NEW FILES vs the cancelled attempt — same
  cell layout/em sizes/character coverage, generated with the fixed
  generator. The pre-existing files could not draw text at all.

## Circumstance worth flagging: the baked font assets were broken

The cancelled attempt's atlases could NOT render text. Evidence
(reproducible before the fix):

- In every atlas, ALL glyphs of a row were pasted at the same x —
  scripts/fontgen/main.go's compose loop lacked the per-cell
  `i*cellW` offset (`left := m.dx + 1`), so the whole row overlapped
  into one ~150px blob and every column past cell 1 was empty.
- Column scan of the committed `atlas-clock-410.png`: nonzero coverage
  ONLY in x ∈ [33,189] for a 3122px-wide row (14 cells of 223px).

Consequence: the runtime slicing atlas cells (x ≥ 223·i) found all of
cells 1..N EMPTY — every clock string after its first digit would
render blank/black cells. A slot-based renderer cannot recover from
this; regenerating was the only correct path (the mandate "do NOT
regenerate" assumed the assets were good — they demonstrably were
not). I fixed the missing offset inside scripts/fontgen/main.go (one
guarded block), regenerated all four atlases + metrics.json, and
verified each atlas row visually cell by cell (atlas dump showed
`ABCDEF…` and `0…9%:.,-!?/` correct, and digits, colon, plus/minus
placed with their own cells). metrics.json still uses CELL-RELATIVE
ink boxes exactly per the documented schema, so any other consumer of
the schema keeps working. `scripts/fontgen/run.sh` was NOT modified.

UI text (uppercase+digits verified), wall clock, progress fill, NEXT
block, alert tinting, OVERTIME badge and the message strip were all
rendered into test PNGs and visually verified (frame.png checked in
/conversation).

## Decisions that integration agents must honor

1. **Pacing**: KMS Present = damage copy → PAGE_FLIP(EVENT) → blocking
   read of the flip event. The flip-landing vblank IS the frame wait;
   never add a separate WAIT_VBLANK, never add a second Present thread.
2. **DRM master**: the timer must open the card before any other
   DRM user; error path documents the CuTePi rule in its message.
3. `drm.Provider.View(nowMS)` is the only integration seam — ws/engine
   should feed server-authoritative state; WallClock string is
   refreshed by the provider, not the renderer (renderer is pure).

## For the hardware run (next agent)

- Run `TIMERPI_HW_TEST=1 go test ./drm/ -run TestHW -v` on the Pi with
  the panel attached (skip-guarded; needs a free card).
- Kernel cmdline `video=HDMI-A-1:1920x1080@50e`; verify with
  `/sys/class/drm/card0-HDMI-A-1/modes` (+ `drm_info` then).
- `View.Rate` is not drawn yet; a small rate chip is a clean follow-up.

## Deviations from the brief

- The brief said "do NOT regenerate" the assets; I regenerated them
  because they were unusable (evidence above). The generator source
  fix is in scripts/fontgen/main.go (nested module; run.sh untouched,
  still proxy-neutral).
- The brief said embed `public/img/timerpi-192.png`; go:embed cannot
  reach outside the package directory, so the file was copied to
  `drm/assets/timerpi-192.png` (committed copy, same bytes).
