# drm — TimerPi display renderer (1080p50)

The custom framebuffer/KMS renderer that draws the timer HUD straight
onto the Pi's HDMI output. No X11, no Wayland, no font library: baked
Share Tech Mono atlases (drm/assets/, produced by scripts/fontgen) plus
hand-written DRM uapi ioctls (pure Go + golang.org/x/sys, no cgo).

```
face.go          pure layout engine: Render(View) → ARGB frame, Diff → damage rects
framebuffer.go   32bpp little-endian buffer, rect math, copy/blit helpers
font.go          go:embed atlases + metrics.json, glyph blitting
display.go       Backend interface + env selection (off | drm | fb)
kms.go           KMS dumb buffers + page flip backend (the real output)
fb.go            /dev/fb0 fallback backend (scripts/splash-style ioctls)
runclock.go      the 50 fps loop: Provider → Render → Diff → Present
```

## Integration contract (for main.go)

```go
back, err := drm.OpenDisplay()      // picks the backend per env; logs why
if err == nil {
    defer back.Close()
    clock := drm.NewClock(back, engineProvider)   // Provider: engine → View
    go clock.Run(ctx)                             // blocked Present paces to vblank
}
```

Interfaces:

```go
type Backend interface {
    Open() error
    Size() (w, h int)
    Present(buf []byte, dirty []Rect) error // full frame + damage rects
    Close() error
}

type Provider interface {
    View(nowMS int64) View   // the engine snapshot rendered to a View
}
```

`View` (all fields comparable — an unchanged View renders byte-identical
output and skips the frame before any work):

```go
type View struct {
    Label, Speaker, Next, NextSpeaker, Title string
    RemainingMS int64        // may be negative in overtime
    Overtime bool             // OVERTIME badge + '+' sign
    AlertState int            // 0 normal / 1 / 2 (engine alert thresholds)
    AlertColor1, AlertColor2 string   // digit tint per state
    Progress float64          // 0..1, bottom bar (purple #7C3AED)
    Message, MessageColor string      // overlay strip ("" = hidden)
    WallClock string          // "HH:MM:SS", refresh once per second
    Rate float64              // rate multiplier (reserved, not drawn in v1)
    Blank bool                // blank the timer: logo + wall clock only
}
```

A minimal provider adapter at integration:

```go
type drmProvider struct{ e *timerpi.Engine }
func (p drmProvider) View(nowMS int64) drm.View {
    s := p.e.Snapshot()
    // map PROTOCOL snapshot fields onto the View struct, including
    // nowMS → wall clock string (recompute only when the second changes)
    ...
}
```

## Layout (1920×1080 fixed)

| Element | Geometry |
|---|---|
| Logo mark (brand PNG, embedded) | top-left, 96×96 @ (40,36) |
| Wall clock (ui face, gray) | top-right, right edge x=1880, top y=56 |
| Message strip (msg face, bg=MessageColor) | centered, top y=170, 88px cells + padding |
| OVERTIME badge (msg face on red #DC2626) | centered, top y=308 |
| Giant clock (clock faces, white/alert-tinted) | centered, cell top y=400 |
| NEXT label + speaker (ui face) | left x=160, y=872 / y=944 |
| Progress bar (purple `#7C3AED`, never green) | bottom: y=1008, h=28, 24px margins |

Clock formatting (pure, `ClockLayout`): `M:SS` below an hour, `H:MM:SS`
from it, tenths never shown; overtime prefixes `+`; the `clock-410`
face is used for ≤ 6 cells, everything longer (whole-hour strings,
long overtime) drops to `clock-320`. Text outside the baked character
range (uppercase A-Z, 0-9, `%:.,-!?/` + clock's `0-9:-+`) is filtered,
never guessed — no missing-glyph squares.

## Damage / dirty-rect model

Every giant-clock glyph lives in its own monospaced **slot** (interned:
`Rect` + content key). `Diff(prev, next)` produces rects only where:

- a slot's rect or content key changed (interslot: static digits cost
  zero rects — a ticking second is typically one ~220×290 rect),
- a slot count changed (whole clock band),
- a named site (logo, wall clock, message strip, overtime badge, NEXT
  lines, progress rail/fill) changed pixel-wise.

An unchanged View therefore produces **zero** rects (verified by
tests). runclock skips the whole render in that case and the KMS
backend skips any flip — a static wall shows a static frame.

## Backends

### KMS (`kms.go`, default)

1. Opens `$DRM_CARD` (default `/dev/dri/card0`) `O_RDWR`.
2. `DRM_IOCTL_SET_MASTER` — **this service must become DRM master**
   first. Anything holding the card first (splash-draw holds fb0 only,
   fine; a stray `drm_info`/ kms debug run would break it with
   permission denied, per CuTePi's device wisdom).
3. GETRESOURCES → GETCONNECTOR (HDMI-A-1 first, honoring
   `$DRM_CONNECTOR`, e.g. `HDMI-A-1`; then HDMI-B, then any connected)
   → GETENCODER → possible_crtcs → CRTC chain.
4. Mode choice: `1920x1080 @ 50` progressive by exact match, then any
   1920×1080, then the EDID-preferred mode, then largest area.
5. Two `CREATE_DUMB` buffers (XRGB8888, depth 24) + ADDFB each,
   double-buffered; initial `SETCRTC` shows black.
6. Present: damage-copy into the **off-screen** buffer, then
   `DRM_IOCTL_MODE_PAGE_FLIP` with `DRM_MODE_PAGE_FLIP_EVENT` and a
   blocking read of the flip-complete event on the card fd.

**Pacing decision (documented per the plan):** the flip completes ON
the next vblank and the event read is the wait — there is deliberately
no separate `WAIT_VBLANK` anywhere. This keeps a hard 50 Hz cadence at
the display's refresh and never tears (damage memcpys never touch the
scanout buffer). A per-frame `SetPlane` commit would also block until
vblank (and would share the commit budget with other planes, per
CuTePi's measured wall limits); the double-buffer flip avoids that.

The rendered frame is **soft real time, not polled**: runclock's 20 ms
ticker triggers frames; `Present` itself paces to vblank (blocking flip
wait), so the effective rate is exactly the panel's 50 Hz and heavy
frames self-throttle.

### fbdev fallback (`fb.go`)

`TIMERPI_FBDEV` (or legacy `FBDEV`), default `/dev/fb0`: single
mmap'd buffer, damage writes only, no flip, no pacing (writes land as
the scan reaches them). Pixel packing follows the var-screeninfo
bitfields — the same verified math as scripts/splash/main.go, so
whatever layout the kernel advertises is what gets drawn.

### off

`TIMERPI_DISPLAY=off` gives a no-op backend for CI/containers.

## Environment knobs

| Var | Values | Meaning |
|---|---|---|
| `TIMERPI_DISPLAY` | `off` \| `drm` \| `fb` \| `auto` (default) | explicit backend choice |
| `DRM_CARD` | `/dev/dri/card0` (default) | KMS card |
| `DRM_CONNECTOR` | e.g. `HDMI-A-1` | pinned connector (alias `HDMI-A-<n>`) |
| `TIMERPI_FBDEV` | `/dev/fb0` (default) | fbdev fallback device (also legacy `FBDEV`) |

## Running on the Pi (acceptance checklist)

Kernel cmdline (persistent, e.g. via `raspi-config` → on Pi 5 it's
`/boot/firmware/cmdline.txt`):

```
video=HDMI-A-1:1920x1080@50e
```

Checks (`PI-DEPLOY.md` step 8 style):

1. Boot with the display attached; `cat
   /sys/class/drm/card0-HDMI-A-1/status` → `connected`.
2. `TIMERPI_HW_TEST=1 go test ./drm/ -run TestHW -v` drives ~200
   animated frames through the real card (and fb0), skipping cleanly
   elsewhere. Schedule AFTER the app's other card users are stopped —
   the service must be first on the card.
3. `drm_info` (if installed) shows the mode `1920x1080`, 50 Hz, our two
   dumb framebuffers alternating `FB_ID`s per presented frame.
4. Full loop: `systemctl start timerpi`; the wall clock in the top-right
   ticks once per second; start a countdown and watch the seconds
   digit.

## Tests

`go test ./drm/ -count=1` (all pure, fast, container-safe):

- atlas metrics parse + coverage assertions (digits 0-9, `: - +`,
  OVERTIME word coverage, per-glyph ink inside its cell, the grid vs
  the PNG),
- a2b color parse + little-endian buffer math (B,G,R,A lane order =
  KMS XRGB8888 wire bytes),
- image fill/copy/rect math incl. clipping honesty,
- backend contract with a fake cyclic double-buffer backend (flip
  accounting, empty-damage no-op, persistent-failure give-up, error
  baseline reset),
- full clock loop with a fake provider (skip-on-equal, damage inside
  the clock band only),
- KMS/fbdev hardware runs behind `TIMERPI_HW_TEST=1` + a real
  `/dev/dri` appearing.

`TestUapiStructSizes`/`TestUapiIoctlNumbers` lock the hand-written
uapi mirrors to the kernel layouts and libdrm ioctl numbers — a
toolchain/kernel pairing that changes struct packing fails loudly in
tests, not silently on the panel.

## Known risks / open items

1. **Regenerated atlases (2026-10-03).** The previously committed
   atlas assets were unusable: the fontgen paste loop missed the
   per-cell `i*cellW` offset, so every glyph in a row was drawn at the
   same x (the whole row overlapped into a blob; columns beyond cell 1
   were empty — rendering was unreadable). The generator (one-line fix
   in scripts/fontgen/main.go) and the assets were regenerated;
   metrics.json keeps cell-relative ink boxes as before — confirmed
   against the committed PNGs' actual content. Re-testing on hardware
   is cheap because the runtime re-validates the grid at load.
2. **Renderer size is fixed 1920×1080.** If the picked mode is not
   1080p (mode fallback path), runclock logs the mismatch and the
   output is clipped/framed as-is; a scaling rect is future work.
3. **Flip-event timeout.** If the kernel stops delivering
   flip-complete events (>100 ms = 5 vblanks), Present swaps
   double-buffer roles anyway and surfaces the error; runclock reset
   its diff baseline so the next success re-copies the full frame.
   Worst case seen as an occasional full-frame repair, not a stuck
   display.
4. **DPI/scale.** The layout constants assume the 1080p panel; no
   fractional scaling for non-1080p sinks in v1.
5. **Rate display.** `View.Rate` is currently not drawn (documented
   decision, reserved for a small rate chip in a later pass).
6. **Console blanking.** fbdev Present does not manage FBIOBLANK; the
   KMS path replaces the console fb on SETCRTC entirely (per CuTePi,
   kernel text stays off-screen while we are master, text mode returns
   when we drop master on Close).
