# TimerPi — Venue boxes, pairing and the cloud

> **Status: design, owner-confirmed decisions and sync rules (2026-10-06), not built yet.**
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

Merging uses the per-session last-writer-wins merge that already exists for offline browser edits (`timerpi/merge.go`), with the venue's copy as the writer that wins ties.

## 8. Build order (STATUS)

| Item | Work |
|---|---|
| N12 | Event end date/time; release at end + 4 h (never mid-timer); release on delete. Works on today's single server. |
| N13 | Pairing codes on unpaired displays (DRM splash + ready card); pair from the event dashboard; event mesh key; signed mDNS announcements (RW15). |
| N14 | `timerpi.local` alias on the primary; clock from the primary/NTP/SuperOperator browser. |
| N15 | Cloud ↔ primary link: pull the event to the venue; stream the copy back. |
| N16 | Audience relay through the cloud; "audience paused" when the link is down. |
