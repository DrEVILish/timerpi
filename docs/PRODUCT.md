# TimerPi — Product Specification

> **What we are building.** This is the single source of truth for *what* TimerPi
> must do and *who* it serves. If code, another doc, or an old note disagrees
> with this file, this file wins. If this file is wrong, fix it here first,
> with the owner's agreement, and then change the code.
>
> Companion docs: [ARCHITECTURE.md](ARCHITECTURE.md) explains *how* it is
> built. [../STATUS.md](../STATUS.md) tracks *what is done and what is open*.

---

## 1. One-line pitch

**TimerPi is a self-hosted cue-timer and audience-interaction system for
multi-room conferences.** One appliance runs the whole event: walk-in signage,
in-room presentation screens, speaker confidence timers (DSM), room operators,
a SuperOperator who can see and control every room, and a Slido-style audience
layer (polls, Q&A, word clouds) that phones join by scanning a QR code.

Every screen is styled by **ftl-themes** and animates items in and out.

## 2. Vocabulary (use these words everywhere: code, UI, docs)

| Term | Meaning |
|---|---|
| **Event** | The whole conference day: all rooms, all screens, one appliance. |
| **Zone** | A group of rooms that share an event walk-in display. An event usually has one zone. |
| **Room** | A physical room with its own running order. **One room = one show** in the data model. A room is addressed by its 8-char share code (`K7QP-M3XB`). |
| **Session** | One timed item in a room's running order (a talk, a break, a changeover). Stored as a *cue*. |
| **Running order** | The ordered list of sessions for one room for the day. |
| **Screen** | One physical display: a browser window or TV that shows a **layout**. It has a name ("Room A DSM"), a theme, and a layout. |
| **Layout** | A set of tiles (widgets) placed on a 16:9 grid. Stored as a *board*. |
| **Template** | A built-in starting layout, such as *Event walk-in*, *Room walk-in*, *Main*, or *DSM*. The operator picks one and can then customise it. |
| **Tile / widget** | One block on a layout: clock, countdown, current session, next session, schedule, map, poll, Q&A, word cloud, join QR, notice, and so on. |
| **Interaction** | Any audience item: **poll**, **quiz**, **Q&A**, **word cloud**, **ideas**. |
| **Push state** | Where an interaction sits in its lifecycle: `Hidden` → `Shown` → `Results` → `Hidden`. |
| **Operator** | A person running one room. |
| **SuperOperator** | A person who can see and control every room. |
| **Audience device** | A phone or laptop that joined a room by QR code. |

## 3. The reference event (acceptance scenario)

This setup is the yardstick. TimerPi is "done" for v2 when an operator team can
run this event from the WebUI alone, without help.

**Two rooms (A and B) in one zone.** Eleven surfaces:

| # | Surface | Who / where | Must show | Must allow |
|---|---|---|---|---|
| 1 | **Event walk-in display** | Foyer screen | Wall clock · each room's current session · event space map · full-day schedule for all rooms | Nothing (read-only) |
| 2 | **Room A walk-in display** | Outside Room A | Wall clock · Room A current session · start time of Room A's next session · Room A full-day schedule | Nothing |
| 3 | **Room B walk-in display** | Outside Room B | Same as #2 for Room B | Nothing |
| 4 | **Room A main display** | Projector in Room A | The presentation surface. Shows polls, questions and results **on demand, for Room A only**. Shows the join QR when the operator chooses | Nothing |
| 5 | **Room B main display** | Projector in Room B | Same as #4 for Room B | Nothing |
| 6 | **Room A DSM / timer display** | Speaker monitor in Room A | Session progress timer (countdown, overtime, alerts, stage messages). Can also show Room A polls, questions and results | Nothing |
| 7 | **Room B DSM / timer display** | Speaker monitor in Room B | Same as #6 for Room B | Nothing |
| 8 | **Room A operator** | Laptop/tablet in Room A | Room A running order, timer, audience items, Room A screens | Run the timer, edit sessions, send messages, create/push/moderate interactions, change Room A screen layouts |
| 9 | **Room B operator** | Same for Room B | Same for Room B | Same, Room B only. **Cannot see or control Room A** |
| 10 | **SuperOperator** | Control desk | Every room at once: state, current session, time left, screens online | Everything any room operator can do, in any room, plus event-wide actions (blackout all, settings, zones, map) |
| 11 | **Audience devices** | Attendees' phones | Only the interactions the operator has pushed, and nothing else | Vote, answer, ask a question, submit words, upvote questions |

The eleven are a **default set, not a limit**. An event may have one room or
ten, and any number of screens per room.

## 4. Requirements by area

Each requirement has an ID so STATUS.md and tests can point at it.

### 4.1 Rooms and timing (the cue timer)

- **T1** Each room has one running order for the day. Sessions have a title,
  speaker, duration, optional wall-clock start time, break flag, tags, notes,
  colour, two alert thresholds with colours, and an end action (hold,
  overtime, blank). They can also auto-continue to the next session.
- **T2** Transport: GO, next, previous, pause/resume, reset, jump, ±30 s/±1 min,
  rate ×0.5–×2.0.
- **T3** The schedule (start, end, over/under) is computed live from the day start, durations and holds.
- **T4** Countdown digits render from the local clock, so they are smooth on every screen. The server never ticks digits.
- **T5** Stage messages ("PLEASE WRAP UP") are pushed to that room's DSM.
- **T6** Running orders import from XLSX, CSV or JSON. A room exports and imports as a single file containing everything, including interactions, screens and layouts.
- **T7** Blackout: one tap blanks every screen in the room. The SuperOperator can blank every room.

### 4.2 Screens and layouts

- **S1** Any browser can become a screen by opening `/d/`. It shows a "ready" card with its name and waits to be **captured** by an operator, who then assigns its room, name, theme and layout. No typing on the TV.
- **S2** Every screen's layout is **customisable**: tiles can be added, removed, moved and resized in a visual editor that previews the screen's theme at 16:9.
- **S3** Built-in templates cover surfaces #1–#7 above. Choosing a template gives a correct screen with no further editing.
- **S4** The operator sees every screen in their room: online/offline status, name, layout and a live preview. They can change the layout or theme remotely, and the screen follows within about 2 seconds.
- **S5** Screens survive reboot and network loss. They reconnect by themselves and keep the countdown running locally while offline.
- **S6** Screens never show operator chrome (buttons, editors, toolbars). The editor exists only in the operator's preview.
- **S7** The event walk-in screen (#1) aggregates every room in its zone and shows the zone's uploaded venue map.

### 4.3 Audience interaction (Slido-style)

- **A1** Interaction kinds: **poll** (single choice), **quiz** (poll with a correct answer that is revealed on results), **Q&A** (audience asks; others upvote; operator moderates), **word cloud** (audience submits words; sized by frequency), **ideas** (free-text submissions, moderated).
- **A2** Interactions are created ahead of time or live, and belong to **one room**. They never appear in another room.
- **A3** **Hidden until pushed.** Audience devices see *nothing* of an interaction until the operator presses **Show**. Hidden items are absent from what the phone receives, not just visually hidden.
- **A4** **Show** pushes the question to audience devices **and** to the room's displays (main and, if its layout has a poll tile, DSM) at the same moment.
- **A5** **Results** reveals results on the displays and phones: for polls, the total vote count and each option's count and % of total, drawn as bar graphs. Voting closes when results are shown.
- **A6** **Hide** removes the item everywhere, with its exit animation.
- **A7** Moderation: Q&A questions, words and ideas land in a queue. The operator approves, hides or deletes each one individually. Only approved items reach the screens.
- **A8** Joining: a QR code and short URL (`/a/<room code>`) on the room's screens. No login, no app, no personal data. One vote per device per item; a voter can change their vote until results are shown.
- **A9** **Scale:** at least **1,000 audience devices per room** can join within about 30 seconds and vote on one question at the same time, while the room's timer and screens stay responsive. A floor of 200 concurrent devices must work comfortably on a Raspberry Pi 4/5.

### 4.4 Look and motion

- **L1** All UI is styled by **ftl-themes**. TimerPi uses the theme's component classes and tokens and adds layout CSS only. Each screen can have its own theme. The appliance has a default theme (currently `blue-future`).
- **L2** Items appear and disappear with animation: tiles, interactions, results and messages. Options are **fade, slide, pop or none**, plus a duration.
- **L3** Animation is **operator-customisable**, per tile in the layout editor, and must be reachable from the operator UI without editing JSON.
- **L4** Display screens always play their animations, whatever the TV's OS motion setting. Only audience phones honour `prefers-reduced-motion`.
- **L5** Text is legible from the back of a room. Display surfaces size in viewport units, so 720p, 1080p and 4K look identical.

### 4.5 People and access

- **P1** Role ladder: **screen** (no login) → **audience** (QR, no login) → **room operator** (room code plus an optional room password) → **SuperOperator** (device password).
- **P2** A room operator sees and controls only their room.
- **P3** The SuperOperator has a cross-room panel with per-room status and transport, and event-wide actions.
- **P4** No accounts, no personal data. Show codes and passwords are the credentials.

### 4.6 Appliance

- **H1** Single Go binary. Serves HTTP and WebSocket on one port. SQLite storage. Runs on a Raspberry Pi 4/5 or any Linux box. Works behind a reverse proxy or custom domain.
- **H2** Works on an isolated venue LAN. No internet, CDN or cloud is required at show time.
- **H3** Several TimerPi devices on one LAN discover each other (mDNS). If the primary dies, another takes over.
- **H4** Optional OSC bridge to a CuTePi media player: GO fires the paired media cue, and blackout fires panic.

## 5. Out of scope (v2)

- Per-person user accounts and SSO.
- Cloud hosting, multi-venue, internet relay.
- Audio alerts. Alerts are visual only (owner decision 2026-10-03).
- Native (non-browser) rendering of anything except the DSM countdown. Walk-in and main screens need a browser. See open question Q1 in STATUS.md.

## 6. Decisions log (owner-confirmed)

| Date | Decision |
|---|---|
| 2026-10-03 | Default appliance theme = `blue-future`. |
| 2026-10-03 | Audible alerts dropped. |
| 2026-10-03 | Homepage never lists shows (codes are credentials). |
| 2026-10-05 | BLANK is instant (no confirm dialog). |
| 2026-10-05 | Reduced motion honoured on audience phones only (L4). |
| 2026-10-05 | Display screens carry no buttons. Editing happens only in the operator's preview (S6). |
| 2026-10-05 | Audience lane = WebSocket with REST fallback. Word cloud = themed DOM tiles, not canvas. |
| 2026-10-05 | One password per room (optional). SuperOperator = device password. |
