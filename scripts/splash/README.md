# splash-draw — TimerPi framebuffer splash

Pure-Go boot splash: draws the TimerPi logo centered on the fbdev
framebuffer, holds it until the appliance reports readiness, then fades
to black and exits. No X11/Wayland, no extra packages — PNG decode
(`image/png`) plus raw framebuffer ioctls/mmap (`golang.org/x/sys/unix`).

## Build

```sh
go build -o bin/splash-draw ./scripts/splash                       # amd64 (dev)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -o bin/splash-draw-arm64 ./scripts/splash               # Pi 4/5
```

## Usage

```
splash-draw [-image PATH] [-fb DEV] [-ready PATH] [-timeout DUR] [-fork] [-clear]
```

| flag     | default | meaning |
|---|---|---|
| `-image` | `/opt/timerpi/public/img/timerpi-512.png` | brand PNG, drawn centered; oversized art is nearest-neighbor scaled to fit |
| `-fb`    | `/dev/fb0` | framebuffer device; `FBDEV` env overrides the default (flags win over env) |
| `-ready` | `/run/timerpi/ready` | readiness file of the handshake |
| `-timeout` | `45s` | cap on the hold loop; `-1` waits forever |
| `-fork`  | — | used by the unit: re-exec self `-daemon` in a new session and return 0 immediately |
| `-clear` | — | black-fill + restore console blanking, then exit (ExecStop / debugging) |

Exit code is **0** in every operational outcome: no framebuffer (dev
containers, headless), ready file seen, timeout reached, or SIGTERM. Only
supervisor failures (e.g. detached spawn broke) are non-zero.

## fbdev ioctl contract

Numbers from `uapi/linux/fb.h` (arch-fixed constants; noted here because
`golang.org/x/sys/unix` v0.37 does not yet expose fbdev helpers — the
binaries call `unix.Syscall(unix.SYS_IOCTL, fd, req, arg)` with these):

| request | value | struct arg |
|---|---|---|
| `FBIOGET_VSCREENINFO` | `0x4600` | `struct fb_var_screeninfo` (160 B) |
| `FBIOPUT_VSCREENINFO` | `0x4601` | (optional; not used) |
| `FBIOGET_FSCREENINFO` | `0x4602` | `struct fb_fix_screeninfo` (80 B) |
| `FBIOPAN_DISPLAY`     | `0x4606` | `struct fb_var_screeninfo` |
| `FBIOBLANK`           | `0x4611` | blank level int (`FB_BLANK_UNBLANK` = 4 to restore) |

Struct mirrors live in `main.go` and are asserted by
`splash_test.go` (`TestStructMirrorsMatchKernel`): all-u32
`fb_var_screeninfo` = 160 bytes; `fb_fix_screeninfo` = 80 bytes with
`unsigned long smem_start/mmio_start` mirrored as uint64 (8-byte
alignment). If a future `x/sys` adds the real helpers, swap them in and
drop the mirrors — the tests tell you they matched.

Drawing rules:

- Pixel addressing: `row y` starts at `y * fix.line_length`
  (falls back to `xres_virtual * bpp/8`), pixel `x` at
  `y*stride + x*bpp/8`, little-endian bytes.
- Channel placement comes from the var-screeninfo `red/green/blue/transp`
  bitfields, so the driver's declared mode (typically 32bpp BGRX with
  alpha at offset 24, or 16bpp RGB565) is honored without hardcoding.
  Values are reduced 8-bit → channel length (`>> (8-len)`); the fb alpha
  plane, if present, is forced opaque.
- Source alpha is folded onto black first (`c' = c * a`), because the
  logo sits on the black-filled screen.
- Every (re)draw ends with `FBIOPAN_DISPLAY` — some drivers only show new
  bytes after an explicit pan.

## Ready-file handshake

```
boot ─▶ timerpi-splash.service (oneshot)
         ExecStart=/opt/timerpi/bin/splash-draw -fork
            └─ detached worker: logo on /dev/fb0, polls every 100 ms
              wait condition: /run/timerpi/ready exists  → fade + exit 0
              or SIGTERM (system halt/stop)              → fade + exit 0
              or 45 s cap                                 → fade + exit 0
             next: /run/timerpi/ready written by
             timerpi.service once it serves HTTP+WS
```

Contract points the other layers must keep:

1. **Producer**: the appliance (primary/`drm` renderer work, latter
   phase) creates the file `/run/timerpi/ready` when the web app is up
   (i.e. after `ListenAndServe` succeeds) and removes it on graceful
   shutdown. The splash only ever *reads* it (`os.Stat`) — there is no
   content protocol, existence is the message.
2. **Directory**: `/run/timerpi` is provided by `RuntimeDirectory=timerpi`
   in both units (and `scripts/install-pi.sh` writes an identical
   `tmpfiles.d/timerpi.conf`), so the worker can poll before the app has
   ever run.
3. **Mode change**: once the DRM/fbdev renderer takes over the screen the
   splash is gone — it holds at most one timeout, so a renderer arriving
   later just repaints over black.
4. **`-timeout` value**: 45 s comfortably covers SQLite first-boot
   initialization on a Pi 4 without letting a broken web app hide the
   console text behind a stuck logo forever.

On a Pi without `/dev/fb0` (VideoCore only driver, no `dtoverlay=vc4-*`
fb) the binary logs and exits 0, and the unit's `ConditionPathExists`
keeps it from even starting — boot continues normally.
