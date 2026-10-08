# TimerPi — Product Specification

> **What we are building.** This is the single source of truth for *what* TimerPi
> must do and *who* it serves. If code, another doc, or an old note disagrees
> with this file, this file wins. If this file is wrong, fix it here first,
> with the owner's agreement, and then change the code.
>
> Companions: [ARCHITECTURE.md](ARCHITECTURE.md) explains *how* it is built
> **today**. [../STATUS.md](../STATUS.md) tracks the distance between the two.

---

## 1. One-line pitch

**TimerPi is a self-hosted cue-timer and audience-interaction system for
multi-room conferences.** One appliance runs a whole event:

- walk-in poster screens;
- audience screens in each room;
- presenter (DSM) screens facing the speaker;
- a moderator per room;
- a SuperOperator who owns the event;
- a Slido-style audience layer (polls, Q&A, word clouds) that phones join by scanning a QR code.

Every screen is styled by **ftl-themes** and animates items in and out.

## 2. Vocabulary (use these words everywhere: code, UI, docs)

| Term | Meaning |
|---|---|
| **Event** | The top-level thing a SuperOperator creates: a conference with one or more rooms. Addressed by an **event code** (8 chars, `K7QP-M3XB`). Protected by the **supervisor password**. |
| **Day** | One day of an event. **v2 ships single-day events.** The model must allow adding days later (§4.7). |
| **Room** | A physical room inside an event, with its own running order and its own screens. Has a name ("Room A") and one moderator. |
| **Session** | One timed item in a room's running order (a talk, a break, a changeover). Stored as a *cue*. |
| **Running order** | The ordered list of sessions for one room for the day. |
| **Screen** | One physical display (a browser, TV or kiosk) showing a **layout**. It has a name, a **display type**, a theme, a layout and a rotation. |
| **Display type** | **Audience**, **Walk-in** or **Presenter** (§3.2). |
| **Layout** | Tiles (widgets) placed on a grid. Stored as a *board*. |
| **Template** | A built-in starting layout per display type. The operator picks one and can customise it. |
| **Tile / widget** | One block on a layout: clock, countdown, current session, next session, schedule, map, poll, Q&A wall, spotlight, word cloud, join QR, notice… |
| **Interaction** | Any audience item: **poll**, **quiz**, **Q&A**, **word cloud**, **ideas**. |
| **Push target** | Where an interaction is showing: **Audience** (phones and audience screens) and/or **Presenter** (presenter screens). |
| **Spotlight** | One Q&A question pushed full-size, on top of the approved-questions wall. |

## 3. People, devices and screens

### 3.1 Roles

| Role | Device | Gets in by | Can do |
|---|---|---|---|
| **SuperOperator** | Laptop | Creates the event and sets the **supervisor password**. Later: event code + supervisor password | **Full admin of the event.** Create/edit/delete rooms, sessions and interactions in every room. Run any room. Manage every screen. Set moderator passwords. Set the event map and theme. Blackout all. Export/import the event |
| **Moderator** | Tablet or laptop | Home page → type the **event code** → **pick their room** from the list → type the room's **moderator password** *if one is set* | **Run and Audience** for **their room only**: run the timer, edit and import sessions, stage messages, create/push/moderate interactions. Screens, layouts and themes are the SuperOperator's (2026-10-06). Cannot see or change other rooms |
| **Audience** | Own phone or tablet (iPhone, Android) | Scan the room's QR code (`/a/<code>`). No login, no app | Vote, answer, ask, upvote, submit words/ideas, but only on what is pushed to Audience |
| **Screen** | TV, projector, kiosk | Open `/d/` and get captured by the SuperOperator | Nothing. Shows its layout |

**Rules**

- Each room has exactly **one moderator role**. Several devices may log in as it, for example a tablet plus a backup laptop.
- The moderator password is **optional, per room**, and set by the SuperOperator. With no password, the event code plus the room pick is enough.
- The supervisor password is **required** at event creation and needs at least 6 characters. Room (moderator) passwords can be anything.
- No personal accounts and no personal data. Codes and passwords are the credentials.
- Box-level settings (hostname, network, OSC, default theme) need the **box password**, which is set the first time someone opens box settings. Event passwords never unlock them, because anyone on the network can create an event.

### 3.2 Display types

| Type | Faces | Typical content | Special needs |
|---|---|---|---|
| **Audience display** | The audience, in the room (projector, main LED) | Interactions pushed to *Audience*: question, live results, Q&A wall and spotlight, word cloud. Join QR. Session title. Idle "holding" layout between items | Big, legible from the back row |
| **Walk-in display** | People outside rooms, foyer (poster screens) | Wall clock, current/next sessions, full-day schedule, event map, notices | **Portrait support**: each screen has a rotation setting of 0°, 90°, 180° or 270°, and templates exist in portrait form |
| **Presenter display** (DSM) | The presenter only, away from the audience | Remaining time (countdown, alerts, overtime), next session, stage messages, plus interactions pushed to *Presenter* (questions to answer, results) | Maximum legibility of the countdown |

The `/d/` capture flow sets each screen's **room, name, display type, template, theme and rotation**.

## 4. Requirements

Each requirement has an ID so STATUS.md and tests can point at it.

### 4.1 Events and rooms

- **E1** A SuperOperator creates an event from the home page: event name, supervisor password, and the first room(s). The result is an **event code**.
- **E2** Home page join flow: type the event code → see the list of rooms → pick one → enter the moderator password if that room has one → land on the room's moderator view. The SuperOperator path is: event code → "SuperOperator" → supervisor password.
- **E3** The SuperOperator can add, rename, reorder and delete rooms, and can set or clear each room's moderator password. A "same password for all rooms" option is available.
- **E4** An event exports and imports as one file containing everything: rooms, sessions, interactions with their moderation state, screens, layouts, map and theme. The file carries no passwords and no screen keys; importing it makes a new event (new codes) whose password the importer sets.

### 4.2 Rooms and timing (the cue timer)

- **T1** Each room has one running order per day. A session has:
  - type (session or break), title, speaker (a break has a place instead), duration;
  - a timer (countdown, count up, clock) and what happens at zero (hold, overtime, blank);
  - two alert thresholds with colours, tags, notes, a row colour.
  - Every cue starts by hand: there is no auto-continue, wall-clock auto-start or hold after (owner, 2026-10-06).
- **T2** Transport: GO, next, previous, pause/resume, jump (cue up), ±30 s/±1 min, rate ×0.5–×2.0. There is no reset: GO on a row restarts that cue.
- **T3** The schedule (planned start, end, over/under) is computed live from the day start and durations. With no day start set, the first GO starts the day so that cue is on plan.
- **T4** Countdown digits render from the local clock, so they are smooth on every screen. The server never ticks digits.
- **T5** Stage messages ("PLEASE WRAP UP") go to that room's presenter displays.
- **T6** Running orders import from XLSX, CSV or JSON.
- **T7** Blackout: one tap blanks every screen of a room. The SuperOperator can blank the whole event.

### 4.3 Screens and layouts

- **S1** Any browser becomes a screen by opening `/d/`. It shows a "ready" card with its name and waits to be **captured** by a moderator (for their room) or the SuperOperator (any room, or event-wide for walk-ins). No typing on the TV.
- **S2** Every layout is **customisable** in a visual editor: add, remove, move and resize tiles. The editor previews the screen's theme, aspect ratio and **rotation**.
- **S3** Built-in templates exist per display type, covering at least the reference event (§5). Choosing a template gives a correct screen with no further editing.
- **S4** Moderators see their room's screens, and the SuperOperator sees all screens. Both see online/offline status, type, layout and a live preview, and can change layout, theme or rotation remotely. The screen follows within about 2 s.
- **S5** Screens survive reboot and network loss. They reconnect by themselves and keep countdowns running locally.
- **S6** Screens never show operator chrome. Editing happens only in the operator's preview.
- **S7** **Rotation**: a per-screen setting of 0/90/180/270°. The page rotates its whole layout in software, so a landscape TV hung portrait works without OS configuration.
- **S8** Event-level walk-in displays aggregate **all rooms** of the event (current session per room, full-day schedule per room, event map). They are live, not page reloads.
- **S9** Each screen has **its own theme and its own layout**, chosen independently. A screen's name can be edited **inline by double-click / double-tap** wherever operators see it (screens list, previews).

### 4.4 Audience interaction (Slido-style)

- **A1** Kinds:
  - **Poll**: single choice.
  - **Quiz**: a poll with a correct answer, revealed with the results.
  - **Q&A**: audience asks, others upvote, moderator moderates.
  - **Word cloud**: submitted words, sized by frequency.
  - **Ideas**: moderated free text.
- **A2** Interactions belong to **one room**. They are created ahead of time or live, by that room's moderator or the SuperOperator.
- **A3** **Hidden until pushed.** Phones and screens receive *nothing* about an interaction until it is pushed. Hidden items are absent from the data sent, not just hidden visually.
- **A4** **Two independent push buttons per interaction:**
  - **Show to Audience** puts it on audience devices (phones) *and* the room's audience displays at the same moment.
  - **Show to Presenter** puts it on the room's presenter displays (DSM).
  - Either, both or neither may be on.
- **A5** **Show results** reveals results on every target the item is shown on. For polls this means the total votes plus each option's count and % of total, as bar graphs. Voting closes when results are shown.
- **A6** **Hide** removes it from all targets, with its exit animation.
- **A7** **Q&A flow:**
  - Submitted questions enter a moderation queue.
  - *Approved* questions join the **wall**, a list ordered by upvotes, on the targets the Q&A is shown on.
  - The moderator can **spotlight** one question, which shows it full-size and replaces the previous spotlight.
  - Each question can be **dismissed** (removed from the wall) or **marked answered** (greyed or moved to an "answered" section).
  - The audience can upvote approved questions, once per device per question.
- **A8** Word-cloud words and ideas are moderated (approve/hide/delete) per item. Only approved items reach the screens.
- **A9** Joining: a QR code and short URL (`/a/<code>`) per room, shown on that room's audience and walk-in screens. One vote per device per item; a device can change its vote until results are shown.
- **A10** **Scale:** at least **1,000 audience devices per room** can join within about 30 s and vote on one question at the same time, while the room's timer and screens stay responsive. A floor of **200** concurrent devices must work comfortably on a Raspberry Pi 4/5.

### 4.5 Look and motion

- **L1** All UI is styled by **ftl-themes**. TimerPi uses theme classes and tokens and adds layout CSS only. Each screen can have its own theme. **Default theme: `blue-future`.** A custom `timerpi` theme will be designed later. Until then, `blue-future` is the TimerPi default everywhere.
- **L2** Items appear and disappear with animation: tiles, interactions, results, the spotlight and stage messages. Options are **fade, slide, pop or none**, plus a duration.
- **L3** Animation is **operator-customisable** from the UI: a default per layout, an override per tile, and no JSON editing.
- **L4** Screens always play their animations, whatever the OS motion setting. Only audience phones honour `prefers-reduced-motion`.
- **L5** Displays size in viewport units, so 720p, 1080p, 4K and portrait all look right.
- **L6** Moderator views are **touch-first for tablets** and work equally well on laptops. The SuperOperator view is laptop-first. The audience page is phone-first.

### 4.6 Appliance

- **H1** Single Go binary, with HTTP and WebSocket on one port and SQLite storage. Runs on a Raspberry Pi 4/5 or any Linux box, behind a reverse proxy or custom domain.
- **H2** Timers, screens and the control panel work on the venue LAN with no internet; no CDN, all JS/CSS vendored. **Audience features need the internet**: phones reach TimerPi through the public cloud address (2026-10-06, [VENUE-CLOUD.md](VENUE-CLOUD.md)).
- **H3** Boxes paired to an event discover each other (mDNS, signed with the event key). If the primary dies, another takes over. The primary answers `timerpi.local`.
- **H6** A **cloud server** hosts many events at once. During a show the **venue's primary box is the event's home** and the cloud relays audience traffic and keeps a copy ([VENUE-CLOUD.md](VENUE-CLOUD.md)).
- **H7** A **box is one display, attached to one event at a time**. An unpaired box shows a pairing code; the SuperOperator types it on the event dashboard (cloud or `timerpi.local`, with or without internet).
- **H8** Every event has an end date/time. Boxes release their pairing 4 hours after it (never mid-timer), or at once when the event is deleted.
- **H4** Optional OSC bridge to a CuTePi media player: GO fires the paired media cue, and blackout fires panic.
- **H5** Screens can be browsers on anything: kiosk Pis, smart TVs, PCs. The Pi's native renderer is an optional extra for presenter countdowns.

### 4.7 Multi-day (designed for, not shipped in v2)

- **M1** v2 is single-day.
- **M2** The data model must not block days. A session belongs to a room *and* a day, and the day defaults to the event's only day.
- **M3** Later: add days to an event, use a day picker for moderators and walk-ins, and have walk-ins show "today".

## 5. The reference event (acceptance scenario)

TimerPi v2 is done when an operator team can run this event from the WebUI alone.

**Event "Conf 2026", two rooms (A and B).**

| # | Screen / person | Display type / role | Must show / do |
|---|---|---|---|
| 1 | Event walk-in (foyer) | Walk-in, event-level | Wall clock · every room's current session · event map · full-day schedule for all rooms |
| 2 | Room A walk-in (door) | Walk-in, Room A | Wall clock · Room A current session · next session start time · Room A full-day schedule · join QR |
| 3 | Room B walk-in | Walk-in, Room B | Same for Room B |
| 4 | Room A main screen | Audience, Room A | Interactions *Shown to Audience* in Room A: question, results bars, Q&A wall + spotlight, word cloud. Join QR on demand |
| 5 | Room B main screen | Audience, Room B | Same for Room B |
| 6 | Room A DSM | Presenter, Room A | Countdown with alerts/overtime, next session, stage messages, interactions *Shown to Presenter* |
| 7 | Room B DSM | Presenter, Room B | Same for Room B |
| 8 | Room A moderator | Moderator (tablet/laptop) | Runs Room A only |
| 9 | Room B moderator | Moderator | Runs Room B only. Cannot see Room A |
| 10 | SuperOperator | SuperOperator (laptop) | Sees and edits everything. Event-wide actions |
| 11 | Audience phones | Audience | Only what is *Shown to Audience* in the room whose QR they scanned |

This set is a default, not a limit: any number of rooms, and any number of screens of any type per room.

## 6. Out of scope (v2)

- Personal user accounts and SSO.
- Multi-venue events (one event per venue, 2026-10-06).
- Audio alerts. Alerts are visual only.
- Multi-day *UI*. The data model is still designed for days (§4.7).

## 7. Decisions log (owner-confirmed)

| Date | Decision |
|---|---|
| 2026-10-03 | Audible alerts dropped. |
| 2026-10-03 | Home page never lists events or rooms (codes are credentials). |
| 2026-10-05 | BLANK is instant (no confirm). |
| 2026-10-05 | Reduced motion is honoured on audience phones only. |
| 2026-10-05 | Screens carry no operator chrome. |
| 2026-10-05 | Audience lane = WebSocket with REST fallback. Word cloud = themed DOM tiles. |
| 2026-10-05 | Screens may be kiosk Pis **or** any other browser (TVs, PCs). The layout system must not assume either. |
| 2026-10-05 | **`blue-future` is the TimerPi default theme.** A custom theme comes later; the local `timerpi` ftl theme is shelved. |
| 2026-10-05 | Q&A = approved wall + moderator spotlight. Questions can be dismissed or marked answered. |
| 2026-10-05 | **Event model:** the SuperOperator creates the event (supervisor password) and is full admin. One moderator per room, joining via event code → room pick → optional moderator password set by the SuperOperator. |
| 2026-10-05 | Two push buttons: **Show to Audience** (phones + audience displays) and **Show to Presenter** (DSM). |
| 2026-10-05 | Moderator (room) password is optional. |
| 2026-10-05 | Single day first. The model must allow multiple days. |
| 2026-10-05 | **htmx 4**, vendored locally. |
| 2026-10-05 | Three display types: Audience, Walk-in (portrait, rotation setting), Presenter. Device classes: audience = phones/tablets, moderator = tablet/laptop, SuperOperator = laptop. |

| 2026-10-05 | Passwords: **one supervisor password per event** (SuperOperator/admin) and **one optional password per room** (moderator). |
| 2026-10-05 | ~~No separate appliance password: box settings need any event's supervisor password.~~ Superseded 2026-10-06. |
| 2026-10-06 | **Box password** (review BUGLOG RC5): anyone on the network can create an event, so event passwords no longer unlock box settings. A separate box password, set the first time someone opens box settings (`/box`), guards hostname, network, OSC and the default theme. |
| 2026-10-06 | Supervisor passwords need at least 6 characters; room passwords can be anything (minimum security). Sign-in attempts are rate limited. |
| 2026-10-06 | OSC input stays as is (room code in the address, off by default). Accepted risk when switched on. |
| 2026-10-06 | Captured screens get a key: only keyed screens receive presenter items and the running order. |
| 2026-10-07 | **Next major release, venue mesh** ([VENUE-CLOUD.md](VENUE-CLOUD.md) §9–§14): the cloud runs on x86-64 (LXC, Debian Trixie); boxes run Raspberry Pi OS Lite (Trixie) with a BATMAN-adv mesh on the **built-in Wi-Fi**, ESSID fixed to `timerpi`, 20 MHz, built-in Wi-Fi on 2.4 GHz channel 13 at every boot; a USB radio adds a second leg (USB on 5 GHz channel 36 if it can, otherwise USB on 2.4 GHz and the built-in moves to 5 GHz), Wi-Fi country GB; any box may be wired to a DHCP router. Operator access at offline venues is handled outside TimerPi. Boxes upgrade in place: at boot they look for a newer build from the cloud or other boxes for 5 minutes, then not again until the next reboot. |
| 2026-10-07 | The SuperOperator role is called **Event Technician** in the UI and its password the **Event Technician Password** (code and docs keep the old internal name for now). The footer always shows the Event ID and the ping to the server, never the session code. Presenter screens can be flashed from the transport, and each alert can blink the timer. |
| 2026-10-07 | **You pick a layout, the orientation picks its version.** Every layout (built-in or the event's own) has a landscape and a portrait version, edited together (a Landscape/Portrait switch in the editor; saving saves both). Built-ins: four per display type (Audience: Audience main, Q&A wall, Holding slide, Break; Walk-in: Event, Room, Room lobby, Clock; Presenter: DSM, Full timer, Countdown only, Speaker support), 24 versions in all. A phone or tablet display follows the way it is held and swaps version instantly when turned; its Mounted setting is hidden. Every other screen takes its version from Mounted (0°/180° landscape, 90°/270° portrait). Changing a screen's type moves it to that type's layouts. UI components come from ftl-themes; dialogs are always ftl modals. |
| 2026-10-07 | The Event Technician can **sign in on their phone** by scanning a QR code on the Event Technician page (single-use, 2 minutes, voided by a password change) to check rooms and screens while walking the venue. The event's own layouts are listed on the Screens page and can be deleted; no layout is created automatically. |
| 2026-10-06 | **Cloud + venue boxes** ([VENUE-CLOUD.md](VENUE-CLOUD.md)): the cloud server holds many events; a box is one display attached to one event at a time; the venue box is home during the show and the cloud relays audience traffic; without internet audience features are off; moderators reach the control panel at `timerpi.local`. |
| 2026-10-06 | Pairing: unpaired boxes show a pairing code; pairing works on the cloud or on `timerpi.local` (offline too). Boxes release 4 h after the event's end date/time, or when the event is deleted. |
| 2026-10-06 | UI round (STATUS U1–U29): displays never show the event code; room names on displays read "Room: <name>" when the event has several rooms; walk-in schedule lines are `hh:mm  Title - Speaker` (24 h); no tap-for-full-screen on displays; built-in layouts are templates, edited as named copies; moderators work only in Run and Audience and can import running orders and interaction lists; only the SuperOperator changes themes; the Setup tab goes. |
| 2026-10-06 | Layout editor (STATUS U8, U10, U11): an almost-full-screen modal; handles follow common editor practice. Built-in templates appear in the same Layout list labelled **[built-in]** and are never edited: editing one makes a **named copy that belongs to the event** (usable by every room). After naming it, a list offers every screen **of the same display type across the event**, grouped by room, with the starting screen ticked. The built-in templates themselves get reworked later. |
| 2026-10-06 | Displays on devices that report their orientation (phones, tablets) follow it; the screen's "Mounted" rotation setting is ignored there (STATUS U1). Breaks can carry a location (e.g. "Great Hall") that shows on the schedule (U14). Import column matching (U26, U27) is deferred. Setup tab removed: Import moves to Run, Duplicate room to the SuperOperator dashboard, day start and room notes to Run; moderators get no theme picker (U25, U28, U29). |
| 2026-10-06 | Cloud/venue sync ([VENUE-CLOUD.md](VENUE-CLOUD.md) §7): seamless whether offline, local or remote; one event per venue; venue changes overwrite earlier cloud changes; while the link flaps the venue only uploads; cloud changes reach the venue only over a stable link. |
| 2026-10-05 | Zones are replaced by the event. The event walk-in shows every room in the event. |
| 2026-10-05 | "Show results" reveals results wherever the item is currently shown. |
| 2026-10-05 | Every screen has its own theme and layout. Screen names are renamed inline by double-click (or double-tap). |
