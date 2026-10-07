# TimerPi — Venue boxes, pairing and the cloud

> **Status: built 2026-10-07 (STATUS N12–N16, N17–N21), not yet tried on Pi
> hardware.** Owner-confirmed decisions and sync rules from 2026-10-06; how
> the build meets them: §7 (end) and §8a.
> §9–§14 (2026-10-07) are the **next major release** (cloud host, BATMAN-adv
> venue mesh, Trixie, boot-time updates). **Built 2026-10-07 (STATUS N17–N21),
> not yet tried on Pi hardware** (D1). Owner answers are in §13; the kiosk
> question is still open.
> Work items are STATUS N12–N16. This replaces the "isolated venue LAN only"
> assumption (PRODUCT H2, before 2026-10-06) and closes BUGLOG RW15
> (unauthenticated device mesh) by design.

## 1. The pieces

| Piece | What it is | Holds |
|---|---|---|
| **Cloud server** | The public TimerPi (today `https://timer.drevilish.com`, to change later). One install, **many events at once**. | Events while they are prepared; the audience pages; a copy of each event after the show |
| **Box** | A Raspberry Pi with one HDMI output: **one box = one display**. Runs TimerPi. **Attached to one event at a time.** | Its pairing (event, key, its screen settings) |
| **Primary box** | The box at the venue elected primary (mDNS). Answers **`http://timerpi.local`**. | **The event during the show** (the home copy): rooms, sessions, runtime, interactions |
| **Screen (browser)** | Any browser showing `/d/…` (TV, PC, a box's kiosk). | A screen key (BUGLOG RW9) |

## 2. Owner decisions (2026-10-06)

1. The cloud server supports several events at the same time.
2. **Without internet, audience features are off**: phones reach TimerPi only through the public cloud address.
3. Each box is attached to **one event at a time**. Think of a box as a single display.
4. Moderators and the SuperOperator can reach the control panel at **`timerpi.local`** (the primary box).
5. **The venue box is home during the show; the cloud relays** audience traffic and receives a copy.
6. A box **releases its pairing 4 hours after the event's end** date/time, or at once when the event is deleted.
7. An unpaired box **shows a pairing code** on its display.
8. Pairing also works **without internet** (on `timerpi.local`); it reaches the cloud when the internet returns.

## 3. Event lifecycle

```
 prepare (cloud)  →  pair boxes  →  show (venue home, cloud relays)  →  end + 4 h: release
       ↑                                                               ↘ copy back to the cloud
       └──────── event deleted at any time: every box releases ────────┘
```

- **Event end:** every event gets an **end date and time** (venue local time). Multi-day events end on their last day. The SuperOperator can extend it.
- **Release at end + 4 h**, but never while a room's timer is running (release waits for it to stop). Released boxes forget the event key and their screen settings and show a pairing code again.
- **Delete:** deleting the event releases every box at once. Offline, a box learns this the next time it reaches whoever deleted it (the cloud or the primary box).

## 4. Pairing

1. An unpaired box shows its **name and a 6-digit pairing code** on HDMI (the DRM splash or its kiosk ready card). The code changes every 10 minutes.
2. The SuperOperator types the code on the event dashboard, either on the cloud or on `timerpi.local`. Typing a code proves someone is standing at that display. Attempts are rate limited.
3. The box receives: the event, the **event mesh key**, its display settings (room, display type, layout, theme, rotation) and its screen key.
4. The first box paired at a venue pulls the event from the cloud (the existing full-fidelity bundle) and becomes its home. More boxes join it as displays.
5. Paired offline (no cloud reachable)? The event must already be on the primary box (created there, or pulled earlier). The pairing is reported to the cloud when it is back.

## 5. Box mesh (closes BUGLOG RW15)

- Boxes paired to an event **sign** their mDNS announcements with the event mesh key.
- Only signed announcements count for the primary election and takeover. Unpaired boxes and any other device on the network are ignored, so a phone on the venue Wi-Fi can't pretend to be the primary.
- The primary also answers the alias `timerpi.local`.
- **Clock:** a Pi has no battery-backed clock. Boxes take their time from the primary; the primary from the internet (NTP) when online, else from the SuperOperator's browser when they sign in. The end + 4 h release uses this clock.

## 6. Cloud relay (audience)

- The primary box keeps one outbound WebSocket to the cloud (works through venue NAT; nothing to open on the venue router).
- Phones scan the QR → the **cloud** audience page → votes and questions travel cloud → primary box, which owns the interaction state, and the on-air item and results travel primary → cloud → phones.
- Internet down: the cloud shows "audience paused" on phones; screens and timers at the venue carry on.
- The primary streams a read-only copy of the event to the cloud during the show; at release the final copy is stored on the cloud.

## 7. Sync rules (owner, 2026-10-06)

1. **Seamless.** Moderators and the SuperOperator work the same way whether they are on the venue network offline, on it with internet, or remote through the cloud. Nobody chooses a mode.
2. **One event per venue** (one primary box holds it).
3. **Venue changes win.** A change made at the venue overwrites earlier cloud changes to the same thing.
4. **Flapping link: upload only.** While the cloud link keeps dropping and returning, the venue keeps pushing its changes to the cloud and takes nothing back.
5. **Stable link: both ways.** Cloud changes (for example a remote SuperOperator editing) reach the venue only while the link has been up without a drop for a while (proposed: 2 minutes; tunable).
6. **Created offline:** an event made at the venue with no internet is uploaded to the cloud when the link first becomes stable (follows from rule 1; confirm when N15 starts).

**How the build meets these rules (2026-10-07).** Cue ids are local to each
database, so the cloud and a venue can't merge running orders cue by cue.
Instead the venue's copy is the only writer during the show:

- The primary uploads its whole event copy (`routes/eventbundle.go`) on
  connect and whenever it changes; the cloud replaces its copy with it. Venue
  changes always win (rule 3) and the venue never takes a copy back (rule 4).
- A remote Event Technician or moderator on the cloud works **on the venue**:
  once the link has been up for 2 minutes without a drop (`StableAfter`),
  their pages, API calls and live socket are tunnelled to the venue (rule 5).
  Before that, and while the link is down, the cloud shows its last copy
  read-only and refuses changes with "changes are paused".
- An event made offline at the venue is registered with the cloud once the
  cloud has answered steadily for 2 minutes (rule 6).
- Phones' votes and questions go to the venue whenever the link is up (they
  are audience actions, not cloud edits); with it down phones see "audience
  paused".

## 8. Build order (STATUS)

| Item | Work |
|---|---|
| Item | Work | Built (2026-10-07) |
|---|---|---|
| N12 | Event end date/time; release at end + 4 h (never mid-timer); release on delete. | ✅ `routes/release.go`; end time in Event settings |
| N13 | Pairing codes; pair from the event page; event mesh key; signed mDNS (RW15). | ✅ code on the box's own screen (`/d/box`); DRM splash code waits for the kiosk decision |
| N14 | `timerpi.local` alias on the primary; clock from the primary/NTP/Event Technician's browser. | ✅ |
| N15 | Cloud ↔ primary link: pull the event to the venue; stream the copy back. | ✅ `routes/link.go`, `venue/` |
| N16 | Audience relay through the cloud; "audience paused" when the link is down. | ✅ |

### 8a. How it fits together

- **Box agent** (`venue/agent.go`, boxes only): keeps the pairing (a box
  setting), offers the 6-digit code to the cloud, to primaries on the LAN and
  to itself once it holds an event, pulls the event from the cloud (first
  box) or mirrors the primary every 30 s (members), asks every 30 s whether
  the event ended or was deleted, publishes `timerpi.local` while it leads,
  and sets an unset clock from the primary.
- **Box screen:** the kiosk opens `http://localhost/d/box`. It shows the box's
  name and code, or its screen served by the primary in a full-screen frame
  that follows takeovers, re-pairing and release. `/api/pairing/self`
  answers only the box itself, so the code never leaks to the LAN.
- **Event made on a box:** if a box holds exactly one event made on it, it
  attaches to it (offline pairing at `timerpi.local`); it then refuses new
  events. Servers that host many events (the cloud, dev and test servers)
  run with `TIMERPI_ROLE=cloud`.
- **Release on the primary:** the final copy goes to the cloud; the box only
  deletes its copy once the cloud confirmed it (else it keeps it: no data
  loss). Members delete their mirror.
- **Signed mDNS:** TXT `event` + `sig` (HMAC with the event mesh key over
  host, role, epoch, protocol, event, boot id). Only same-event signed peers
  count; an unpaired box announces `unpaired` and never leads.
- **Link auth:** `X-TimerPi-Auth: <unix s>.<HMAC(mesh key, purpose|event|s)>`
  on `/api/link`, `/api/link/bundle`, `/api/link/register` (5 min skew).
- **Tunnelled identity:** the cloud checks the browser's own session and
  vouches "Event Technician" or "moderator of rooms …"; the venue issues its
  own sessions for that and serves the request in-process. Phones carry their
  cloud device id the same way.

---

## 9. One binary, two roles (2026-10-07)

The cloud and the boxes run **the same TimerPi binary**, built twice
(`amd64` for the cloud, `arm64` for the Pis). A role setting picks what runs:
`TIMERPI_ROLE=cloud|box` (default `box`).

| Feature | Cloud | Box (primary) | Box (member) |
|---|---|---|---|
| Events: prepare, store, copy after the show | ✅ many events | the one event, during the show (home) | — (follows the primary) |
| Audience pages `/a/<room>` (phones) | ✅ the only public address | via the uplink (§6) | — |
| Control panel | ✅ remote; changes reach the venue on a stable link (§7) | ✅ `timerpi.local` | redirects to the primary |
| mDNS discovery, primary election | off | ✅ | ✅ |
| BATMAN-adv mesh, `/settings` network page | off | ✅ | ✅ |
| HDMI display (kiosk or DRM countdown), pairing code | off | ✅ | ✅ |
| OSC bridge (CuTePi) | off | ✅ | off |
| Box password (`/box`) | replaced by a cloud admin login | ✅ | ✅ |
| Updates | publishes signed builds | fetches from the cloud; serves members offline | fetches from the cloud or the primary |

Rules:

- **Versions match.** The mDNS TXT record and the cloud uplink carry a
  protocol major (`proto=3`). A box never joins a primary, and a primary never
  links to the cloud, with a different major, instead of half-working. Nobody
  is asked to do anything: the older box updates itself at its next boot (§14).
- **Outbound only.** Boxes always dial the cloud (one WebSocket from the
  primary, §6). Nothing is opened on the venue router; the cloud never needs to
  reach into a venue.
- **No multicast in the cloud.** Unprivileged LXC containers usually have no
  multicast and no Wi-Fi; `role=cloud` turns those parts off explicitly instead
  of relying on mDNS failing quietly.

## 10. Cloud host

- **Container:** unprivileged LXC, Debian Trixie, x86-64. TimerPi as a systemd
  service (`timerpi.service`, data in `/var/lib/timerpi`), the same unit as on a
  Pi minus the display and splash units.
- **Build:** `GOARCH=amd64`, CGO for SQLite, built on Trixie (glibc 2.41) or in
  a Trixie container so the binary links against the host's libc.
- **TLS in front:** a reverse proxy (Caddy or nginx) terminates HTTPS with a
  real certificate. Phones need it: camera QR scans open `https://`, and some
  browsers refuse WebSockets from secure pages to insecure ones. The proxy must
  pass WebSocket upgrades (`/ws`, and the box uplink). Set `allowed_hosts` to the
  public domain.
- **Scale:** the cloud carries every phone of every live event (PRODUCT A10:
  1,000 per room). Run the load harness (`TP_LOAD=1`) on the container before
  the first show; the boxes only see one relayed stream per event.
- **Backups:** the existing `timerpi-backup.timer` works unchanged; ship the
  archives off the container host.

## 11. Venue network: BATMAN-adv mesh

### 11.1 Shape

```
          internet (optional)
               │
        venue router (DHCP)  ← maybe none (offline show)
          │            │
        eth0         eth0          ← any box may be wired, or none
   ┌── box A ── )))  box B  ((( ── box C ──┐
   │  br0 = eth0 + bat0         bat0 over wlan0 (IBSS, one channel)
   └─ every box, operator laptops on the router's LAN: one Layer-2 segment
```

- **Radio (owner, 2026-10-07):** the Pi's **built-in Wi-Fi**, IBSS, ESSID
  hard-coded to **`timerpi`**, 20 MHz wide. Defaults: **2.4 GHz channel 13**,
  **5 GHz channel 36**. Wi-Fi country defaults to **GB** (channel 13 and
  36–48 are allowed there). The channel numbers are box settings; which radio is on which band is automatic (radio rule below).
- **Radio rule (owner, 2026-10-07).** The built-in Wi-Fi is one radio on one
  channel at a time; a USB Wi-Fi adapter adds a second, and batman-adv then
  meshes over both (it alternates traffic between them and fails over when one
  drops). **Invariant: every box always has a 2.4 GHz leg on channel 13**, so
  boxes with and without adapters always reach each other.

  | USB radio (must support IBSS) | Built-in Wi-Fi | USB radio |
  |---|---|---|
  | none, or no IBSS support | 2.4 GHz ch 13 | — |
  | supports 5 GHz (with or without 2.4) | 2.4 GHz ch 13 | 5 GHz ch 36 |
  | 2.4 GHz only | 5 GHz ch 36 | 2.4 GHz ch 13 |

  - **Every boot starts from the first row**: the built-in Wi-Fi comes up on
    2.4 GHz, then `timerpi-mesh.service` checks for a USB radio (`iw phy`:
    bands and IBSS in the supported interface modes) and applies the table.
  - **Hotplug:** a USB radio plugged in while running is applied the same way
    (udev rule → the same unit). Unplugged: the built-in Wi-Fi returns to
    2.4 GHz ch 13 if it had moved to 5 GHz.
  - Both legs are added to `bat0` (`batctl if add`); the box network page
    shows which radio is on which band.
  - 5 GHz relies on the D1 drill (ad-hoc on channel 36 on these chips). If a
    radio refuses to start IBSS on 5 GHz, the box stays on its 2.4 GHz leg
    and the network page says why.
- **Every box is configured the same.** `wlan0` (plus a USB radio, if any) joins the mesh (IBSS), `bat0`
  runs on them, and `br0` bridges `bat0` with `eth0`. A box with no cable just
  has an idle `eth0` port. Nobody configures "the gateway box": any box that
  is plugged in becomes one.
- **One Layer-2 segment.** batman-adv makes the mesh look like one switch, and
  the bridge joins it to the router's LAN. That keeps today's design working
  unchanged: mDNS discovery and the primary election (`mesh/`, `mdns/`), the
  `timerpi.local` alias, and operator laptops on the venue LAN finding the
  primary. A routed mesh would need an mDNS reflector and its own DHCP.
- **Several wired boxes:** batman-adv's **bridge loop avoidance** (`batctl bl`,
  on by default) stops loops when two or more boxes bridge onto the *same* LAN.
  Two boxes wired to two *different* routers would merge those LANs into one
  segment with two DHCP servers; we don't support that (document it, and
  later, detect it: two DHCP offers from different servers on `br0` → warning
  on the box's network page; not built yet).
- **Addresses:** `br0` takes DHCP from the router. With no router (offline
  show), it falls back to **IPv4 link-local** (169.254/16) plus IPv6
  link-local; mDNS still works, so the boxes still find each other and elect a
  primary.
- **Clock:** the system clock matters before the app does: a box whose clock is
  far off fails TLS to the cloud. Keep `fake-hwclock` (time saved at shutdown)
  and `systemd-timesyncd`; offline, boxes take app time from the primary (§5).
  A Pi 5 with an RTC battery skips the problem.

### 11.2 What changes from the guide (it predates Trixie)

| Guide step | On Raspberry Pi OS Lite (Trixie) |
|---|---|
| `dhcpcd.conf` `denyinterfaces` | dhcpcd is gone since Bookworm; **NetworkManager** owns the network. The installer disables NetworkManager (and lists `wlan*`, `eth0`, `bat0`, `br0` as unmanaged in case it comes back) and enables **systemd-networkd**, which creates `bat0` (`Kind=batadv`, systemd ≥ 248) and `br0` (`deploy/box/network/`), plus systemd-resolved for DNS. |
| `/etc/network/interfaces.d/*` | ifupdown isn't used with NetworkManager; don't mix them. Use networkd `.netdev`/`.network` files. |
| `iwconfig wlan0 mode ad-hoc …` | wireless-tools are deprecated; use `iw dev wlan0 set type ibss` and `iw dev wlan0 ibss join timerpi 2472 HT20 fixed-freq` (channel 13) in a oneshot unit before networkd brings up `bat0`. |
| `/etc/rc.local` | Don't rely on it: `timerpi-mesh.service` (oneshot, after systemd-networkd has created `bat0`, before `timerpi.service`) runs `timerpi mesh`, which applies the radio rule; `timerpi-mesh-status.timer` refreshes the network page every 10 s; a udev rule re-runs it when a Wi-Fi radio is plugged in or out. |
| `brctl` / `dhclient` | Replaced by a networkd bridge netdev and networkd's DHCP client (`DHCP=ipv4`, `LinkLocalAddressing=yes` for the offline fallback). |
| `ip link set mtu 1468 dev bat0` | In a bridge, the smallest member MTU wins, so the whole venue LAN path would drop to 1468. Keep `bat0` at 1500 and let batman-adv fragment (on by default), or raise `wlan0` to 1532 if the chip allows it (HW drill). |
| `gw_mode client` | Only useful with batman's DHCP gateway steering. Wired boxes set `server`, others `client`, decided at runtime from `eth0` carrier. Optional. |
| Wi-Fi country | Trixie keeps Wi-Fi blocked (rfkill) until a country is set. The installer sets **GB** by default (`raspi-config nonint do_wifi_country GB`), changeable in box settings. |

### 11.3 Risks to settle with hardware drills (HW-DRILLS)

1. **Built-in Wi-Fi (chosen).** The Pi's Broadcom chip (brcmfmac) supports
   IBSS but not 802.11s, and IBSS on it is known to be fragile. Drill: 6
   boxes, 1 h, measure link quality (`batctl o`), packet loss and reconnects,
   so we know the limits before a show.
2. **Channels.** 2.4 GHz channel 13 still shares air with the venue's phones;
   drill it next to a busy venue Wi-Fi. Drill 5 GHz IBSS on the built-in chip
   too: some regulatory rules forbid *starting* an ad-hoc network on 5 GHz
   (no-IR), which would make the 5 GHz option unusable on this chip.
3. **Venue traffic over the mesh.** Bridging a busy venue LAN floods its
   broadcast and multicast (ARP, SSDP, mDNS from every guest device) over the
   slow mesh. Filter on `br0` (nftables bridge family, `deploy/box/mesh-filter.nft`):
   from `eth0` into `bat0` only unicast, ARP, DHCP, mDNS and IPv6 neighbour
   discovery pass; other broadcast and multicast is dropped. Advise wiring boxes to a production/AV LAN or a travel router,
   not the guest Wi-Fi. Phones never need the mesh: audience traffic goes
   through the cloud.
4. **The mesh is open.** IBSS with batman-adv has no authentication or
   encryption, the ESSID is the fixed `timerpi`, and the boxes serve plain HTTP
   on `.local`. Anyone in range can join and read operator passwords off the
   air. The signed mDNS announcements (§5) stop a stranger becoming primary,
   and keep two events in one building apart on the shared mesh, but don't
   stop eavesdropping. Accepted for this release (owner, 2026-10-07).
5. **Display traffic.** Each box's kiosk loads pages from **its own** TimerPi
   (same embedded files, same version) and takes only live data (snapshots,
   ~KB) from the primary, so a box boot never pulls ftl-themes across the mesh.

## 12. Major release work (STATUS N17–N21, D1)

| Item | Work |
|---|---|
| N17 | `TIMERPI_ROLE=cloud\|box`; cloud turns off mDNS, mesh, DRM, OSC, `/settings` network; `proto` major in mDNS TXT and the uplink, refusing mismatches. |
| N18 | Trixie Lite box installer: packages (`batctl`, `iw`), Wi-Fi country GB, NetworkManager unmanaged list, networkd `bat0` + `br0` files, `timerpi-mesh.service` (ESSID `timerpi`, 20 MHz, the radio rule of §11.1 at boot and on USB hotplug), link-local fallback, `br0` multicast filter. Replaces the bookworm assumptions in `scripts/install-pi.sh` and PI-DEPLOY. Kiosk part waits for the owner (§13). |
| N19 | Box network page: mesh neighbours and link quality (`batctl n`/`o`), which boxes are wired, gateway, the two-DHCP-server warning. |
| N20 | Cloud deploy: LXC/Trixie runbook in OPS (proxy, TLS, WebSocket upgrade, `allowed_hosts`, backups) and the load test on the container. |
| N21 | In-place updates at boot (§14). |
| D1 | HW drills §11.3 (built-in radio limits, channels 13 / 5 GHz IBSS, venue LAN, MTU). |

## 13. Owner answers (2026-10-07)

| # | Question | Answer |
|---|---|---|
| 1 | Operator access at an offline venue | The owner handles it; not TimerPi's job. |
| 2 | Mesh security | ESSID hard-coded to `timerpi`; open mesh accepted (risk in §11.3). |
| 3 | Radio | Built-in Wi-Fi. |
| 4 | Channels | 2.4 GHz channel 13 by default, 5 GHz channel 36, both 20 MHz. Built-in Wi-Fi on 2.4 GHz at every boot; a USB radio adds the second band per the radio rule in §11.1. |
| 5 | Kiosk on Lite | **Open:** owner to follow up. |
| 6 | v2 boxes | Upgrade in place (§14). |
| — | Wi-Fi country | Default GB (United Kingdom). |

## 14. Updates at boot (owner, 2026-10-07)

- A box looks for an update **only after it boots**, from **the cloud** or from
  **other boxes on the mesh**, whichever it reaches.
- The window is **5 minutes from boot**. If no newer build is found in that
  time, the box **does not try again until the next reboot**. No background
  polling, so nothing ever updates mid-show on its own.
- How a box finds builds: every box already announces its version on mDNS
  (TXT `ver`); a box serves its own signed build at a fixed path to peers; the
  cloud lists the latest build per architecture. The box takes the newest
  build it finds, never an older one.
- Every build is **signed**; a box verifies the signature before installing,
  whoever served it, so a stranger on the open mesh can't push a binary.
- Install is the existing staged swap with health check and rollback
  (`scripts/update.sh`): the box restarts into the new build, and returns to
  the old one if it doesn't come up healthy.
- **No user action, no prompt (owner, 2026-10-07).** A box that missed the
  window and is older than the others just stays out of their election (§9)
  and updates itself at its next boot. Its display shows nothing about it; the
  network page only notes it for whoever looks after the boxes.
- **UPDATE button (owner, 2026-10-07).** If the boot-time update didn't take
  place, the box's network page (`/settings`, box password) checks the same
  sources and, when a newer signed build exists, offers **UPDATE to x.y.z**.
  It installs right away, outside the boot window, with the same rules
  (signed, never older, refused while a timer runs) and restarts the box.
