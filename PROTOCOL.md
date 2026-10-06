# TimerPi — Wire Protocol (HTTP routes + WebSocket frames)

> The contract between server and clients. **It describes the code as it is
> today**: if you change a route or frame, change this file in the same commit.
> Background: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Last verified 2026-10-05 (`ccd415f`).

**Conventions**

- `:ident`, `:code` and `:room` are 8-char codes (`K7QP-M3XB` or `K7QPM3XB`). Under `/api/shows` and `/c/`, `/d/`, `/a/` they are **room** codes; under `/e/` and `/api/events` `:code` is the **event** code. Numeric IDs return **404**.
- **mod** = needs a moderator session for that room (`tp_rm_<ROOM>`) or the event's supervisor session (`tp_ev_<EVENT>`); otherwise 401. **super** = needs the supervisor session. **box** = needs any protected event's supervisor session (open while none is protected). See ARCHITECTURE §8.
- Everything goes through OriginGuard (strict mode only when `allowed_hosts` is set).
- Bodies are capped at 8 MiB, or 32 MiB on `/import` paths.

---

## 1. Pages

| Route | Who | Notes |
|---|---|---|
| `GET /` | anyone | Home: join an event by code, create an event, open a screen, recent events (browser-local) |
| `GET /e/:code` | event-code holder | Event lobby: pick a room (moderator sign-in), SuperOperator sign-in |
| `GET /e/:code/admin` | super | SuperOperator dashboard (live rooms, rooms admin, event settings). No session → 302 to the lobby |
| `GET /c/:ident` | mod | Room dashboard: running order, transport, messages, Audience panel, audience join QR |
| `GET /screens/:ident` | mod | Screens gallery: capture, preview, theme/layout, layout editor |
| `GET /settings` | box | Box settings: device identity, default theme, OSC. No box session → 303 to `/box?next=/settings` |
| `GET /box` | anyone | Box password: first-time setup, sign-in, or (signed in) change password / sign out |
| `GET /logout` | — | Drops every TimerPi session cookie on this browser |
| `GET /super`, `GET /setup` | — | Retired; 302 to `/` |
| `GET /d/` | screen | READY card; registers in the waiting room until captured |
| `GET /d/:ident` | screen | `?view=stage` (default) \| `next` \| `daysheet` \| `clock` \| `board`. Board view: `&board=<id>`. Also `?screen=<name>`, `?theme=`, `?accent=`/`?bg=` (hex). `?edit=1&preview=1` = editor (mod) |
| `GET /a/:code` | audience | Phone page (open) |
| `GET /zone/:name` | screen | Event walk-in for a zone (server-rendered, 30 s refresh; to be replaced by a live event walk-in, STATUS N7) |
| `GET /favicon.ico` | browser | 301 to `/img/timerpi.svg` |
| `GET /health` | probe | `{ok, version:"2.0", uptime, device, title, sessions:{connected}}` |

Static: `/ftl/*` (ftl-themes tree), `/css/*`, `/src/*`, `/img/*` (from `public/`), `/assets/:id` (uploaded blobs, public).

## 2. REST API

### Box password (`routes/box.go`)
| Method & path | Notes |
|---|---|
| `POST /api/box/setup` | `{password}` (≥ 8 chars). Only while no box password exists (409 after); signs this browser in (`tp_box`) |
| `POST /api/box/login` | `{password}` → `tp_box` session. 401 wrong password, 409 none set |
| `POST /api/box/password` | `{current, password}`; needs a box session. Signs every other box session out |
| `POST /api/box/logout` | Drops `tp_box` |

### Events (`routes/events.go`)
| Method & path | Notes |
|---|---|
| `POST /api/events {name, password, rooms[]}` | Create (password ≥ 4 chars). Sets the supervisor session. → `{code, codeFmt, admin}` |
| `GET /api/events/:code` | Lobby data: `{event:{code, name, theme, mapAsset, hasSuperPassword, isSuper, rooms[{code, name, pos, hasPassword, canModerate}]}}` |
| `POST /api/events/:code/login {pw}` | Supervisor sign-in |
| `POST /api/events/:code/rooms/:room/login {pw}` | Moderator sign-in (`pw` empty when the room has none) → `{room:"/c/<code>"}` |
| `PATCH /api/events/:code {name?, theme?, password?}` · `DELETE /api/events/:code` | super |
| `GET /api/events/:code/live` | super. Per room: running, paused, overtime, remainingMS, active/next label, blanked, sessions, screens |
| `POST /api/events/:code/verb {verb, room?}` | super. `go next prev pause resume reset blank unblank`; no `room` = every room |
| `POST /api/events/:code/rooms {name}` · `PATCH …/rooms/:room {name?, password?, clearPassword?, pos?}` · `DELETE …/rooms/:room` | super. Room admin |
| `POST /api/events/:code/room-password {password}` | super. Same moderator password for every room ("" clears all) |
| `POST /api/events/:code/map {assetId}` | super. Event map (0 clears) |
| `POST /api/events/:code/rooms/import` (multipart `file`) | super. Room from a `.timerpi.json` bundle |

### Rooms (shows)
| Method & path | Notes |
|---|---|
| `GET/DELETE /api/shows/:ident` | mod. Snapshot · delete |
| `POST …/clone {title?}` | New code, same running order, no password |
| `POST …/blank {on}` | Room blackout (+ OSC panic/go) |
| `POST …/cmd/:action` | Remote control: `go start pause resume reset next prev jump rate daystart`. Body = WS args |
| `PUT …/cues` · `POST …/moveto {pos,to}` · `POST …/sync` | Replace running order · move · offline-master merge push |
| `POST …/import` (multipart `file`, `kind`, `mode=replace\|append`) · `GET …/import-example?fmt=` · `GET /api/import-example?fmt=` | Running-order import |
| `GET …/file` | mod. v2 full room bundle export (import: `POST /api/events/:code/rooms/import`) |
| `POST …/messages {text,color?,show?}` · `POST …/messages/:mid/show\|hide` · `DELETE …/messages/:mid` | Stage messages |
| `POST …/notes` · `POST …/daystart {hhmm}` · `POST …/zone {zone}` | Room settings |
| `GET …/actions?limit=` · `GET/POST …/client-log` | Action log · browser error reports |
| `GET …/qr?data=&size=` | QR PNG (open; screens use it) |
| `DELETE …/sessions/:peer` | Kick a WS session |

### Screens, layouts, presets
| Method & path | Notes |
|---|---|
| `GET …/screens` · `GET …/screens/self?name=` | Registry ∪ live sessions (with preview data) · a screen's own config |
| `POST …/screens/config {name, theme?, boardId?, room?, kind?, rotation?}` | mod. Theme, layout, display type (`audience\|walkin\|presenter`), rotation (0/90/180/270) |
| `POST …/screens/template {name, template}` | mod. Give the screen its own copy of a built-in template |
| `POST …/screens/match\|rename\|forget` | mod. Copy one screen's look to all · rename `{from,to}` · forget |
| `GET/POST …/presets` · `POST …/presets/:pid/apply` · `DELETE …/presets/:pid` · `GET …/presets/:pid/export` · `POST …/presets/import` | Named screen assignment bundles |
| `GET /api/board-templates` | `{catalog:[{key,name,kind,desc,layout}], templates:{key: layout}}` |
| `GET …/walkin` | open. Event walk-in feed: `{event:{name,map}, rooms:[{name, here, running, now, next, schedule[{label, speaker, startTS, endTS, state}]}]}` |
| `GET/POST …/boards` · `PUT/DELETE …/boards/:bid` | Layouts `{v, rows, orientation, widgets[]}`. `PUT {name?, layout?}` is validated (types, overlap, limits) |
| `POST /api/waiting/register {name,host}` · `GET /api/waiting/mine?name&host` | Screen side (open) |
| `GET /api/waiting` · `POST /api/waiting/:id/capture {code,…}` · `DELETE /api/waiting/:id` | Any operator session |

### Audience interactions (`routes/audience.go`, model in `timerpi/polls.go`)
Items (`poll quiz qa wordcloud ideas`) are created **off air**. Two push targets: **audience** (phones + audience screens) and **presenter** (DSM). One item per target per room. Submissions (questions, words, ideas) are entries under their item.

| Method & path | Notes |
|---|---|
| `GET /api/shows/:ident/polls` | mod. `{items:[PollView + pending + all entries]}` |
| `POST /api/shows/:ident/polls {kind, question, options[], correct?, autoApprove?}` | mod. Quiz needs `correct`; poll/quiz need ≥2 options |
| `PATCH …/polls/:pid {question, options?, correct?, autoApprove}` | mod. Edit text/options/answer |
| `POST …/polls/:pid/show {target: audience\|presenter, on}` | mod. Show to / take off a target |
| `POST …/polls/:pid/results {on}` | mod. Reveal results (closes voting) wherever it is shown |
| `POST …/polls/:pid/hide` | mod. Off every target |
| `POST …/polls/:pid/spotlight {entry}` | mod. Q&A spotlight (0 clears; spotlighting approves) |
| `POST …/polls/:entry/moderate {status: pending\|approved\|answered\|dismissed}` | mod. Word-cloud approval covers every identical word |
| `DELETE …/polls/:pid` | mod. Item (with entries) or one entry |
| `POST …/polls/:pid/state {state}` | Legacy single verb (hidden / open / results on the audience target) |
| `GET /api/audience/:code` | open. `{data:{poll}}` — the audience-target item only |
| `POST /api/audience/:code/vote {pollId, choice, peer}` | open. Poll/quiz choice, or upvote (`pollId` = entry). 1 per 300 ms per peer; 600/s per room → 429 + Retry-After |
| `POST /api/audience/:code/ask {item, text, peer}` | open. Submission to the item shown to the audience (pending unless auto-approve). 1 per 3 s per peer |
| `GET /api/audience/:code/qr` | open. Join QR for `/a/<code>` |

**PollView** = `{id, kind, question, options, correct (-1 until results, quiz only), state (hidden|open|results), toAudience, toPresenter, autoApprove, counts[], total, children[{id, question, state (open|answered; moderator view adds hidden|dismissed), upvotes}], spotlight, pending}`. Word-cloud children aggregate identical words; `upvotes` = senders.

### Box, assets, OSC
| Method & path | Notes |
|---|---|
| `GET /api/theme` · `POST /api/theme` (box) | Box default theme (+ installed list from `ftl-themes/dist`) |
| `GET /api/network` · `POST /api/network/hostname\|role` · `GET /frag/network` | box. Device identity / mesh |
| `GET/POST /api/assets` · `DELETE /api/assets/:id` · `POST /api/zone-map {zone, assetId}` | Any operator session. Uploads (sniffed MIME, 4 MiB) · legacy zone map pointer |
| `GET/POST /api/osc` · `POST /api/osc/test` | box. OSC bridge settings · send test packet |

## 3. WebSocket `/ws`

### Join (first frame, within 10 s)
```json
{"v":1,"t":"join","role":"controls|display|screen|audience","show":"<room code>",
 "peerId":"<uuid>","joinedAt":<ms>,"screen":"<name>"}
```
- `controls` needs a moderator (or supervisor) session cookie on the upgrade request; otherwise `err` "moderator access required".
- `screen` is for screens only; it upserts the screens registry.
- `audience` joins a separate bucket (cap 4000/room). Others share the room bucket (cap 512).

### Server → client
| `t` | Payload | To |
|---|---|---|
| `joined` | `snapshot`, `you`, `peers` (audience: poll state only) | joiner |
| `state` | `snapshot` (full; newer `updatedAt` wins) | room bucket |
| `schedule` | `rows[{pos,startMS,endMS,holdMS,isBreak}]`, `totalMS`, `dayStartTS` | room bucket |
| `oob` | `html`, `target` (`#cuelist #tp-daybar #messages-panel #tp-now #d-stage #share-panel`) | room bucket |
| `poll` | `{v:1, poll: PollView\|null, presenter: PollView\|null, ts}`. Phones get `poll` (audience target) only | audience + room buckets |
| `polls` | (refresh hint after any interaction change) | controls |
| `peers` | `[{peerId, role, joinedAt, screen}]` | room bucket |
| `display` | `{theme}` | targeted screen |
| `screen-board` | `{boardId}` (0 = back to stage) | targeted screen |
| `screen-rename` | `{name}` | targeted screen |
| `screen-look` | `{kind, rotation}` | targeted screen (also on join) |
| `screens` | (refresh hint) | controls |
| `signal` | `{from, data}` (WebRTC relay) | target peer |
| `pong` | `{serverTime}` | sender |
| `err` | `{message}`. "session deleted …" is terminal | sender |

Digits are **never** sent per second. Clients render from `anchorTS`, `rate`, `pausedElapsedMS` and `serverTime`.

### Client → server
- `{"t":"ping"}`. Client pings every 20 s; the server pings every 30 s and drops after 75 s of silence.
- `{"t":"signal","to":"<peerId>","data":{…}}` (not allowed on the audience lane).
- `{"v":1,"t":"cmd","action":…,"args":{…}}`. Only `controls` sessions may send commands; every other role is read-only. Unknown roles are refused at join:

| action | args |
|---|---|
| `go` | `{pos?}` (re-runs a held cue at 0) |
| `start` · `jump` | `{pos}` · `{pos, start?}` |
| `pause` (toggle) · `reset` · `next` · `prev` | — |
| `rate` | `{rate}` ×0.5–×2.0 (clamped) |
| `blank` · `unblank` | — |
| `settings` | `{title?}` · `{ts}` (day starts now) · `{dayStart:"HH:MM"}` |
| `cueAdd` | `{label, durationMS, …}` |
| `cueEdit` | `{pos, …any cue field…, startAt:"HH:MM"\|""}` |
| `cueDel` · `cueDup` | `{pos}` |
| `cueMove` | `{pos, dir:"up"\|"down"}` or `{pos, to}` |
| `addMsg` | `{text, color?, show?}` |
| `showMsg` · `hideMsg` | `{id}` |
| `clearMsgs` | — |

## 4. Snapshot shape (abridged)
```json
{"updatedAt":0,"serverTime":0,
 "show":{"id":1,"code":"K7QPM3XB","title":"Room A","zone":"Main venue","blanked":false},
 "runtime":{"running":false,"paused":false,"activePos":0,"prevPos":0,"nextPos":0,
            "anchorTS":0,"pausedElapsedMS":0,"rate":1.0,"dayStartTS":0},
 "cues":[{"id":1,"pos":1,"label":"Welcome","durationMS":600000,"kind":"session",
          "tags":"VT GFX","speaker":"Leslie","holdMS":0,"timerKind":"COUNTDOWN",
          "alert1MS":90000,"alert2MS":0,"alertColor1":"#ffaa00","alertColor2":"#ff4444",
          "endAction":"HOLD","autoContinue":false,"startAt":"","notes":"","color":"",
          "updatedAt":0}],
 "messages":[{"id":1,"text":"WRAP UP","color":"","shownAt":1}],
 "poll":null, "presenter":null}
```
`timerpi/snapshot.go` is authoritative for the exact field set.

## 5. OSC (UDP)
- **Inbound** (when enabled): `/timerpi/<code>/<verb>`, verbs as in `cmd/:action` plus `blank`/`unblank`. This is unauthenticated, so keep it on a trusted LAN.
- **Outbound** to `osc.out.host:port`: a cue start sends `/cue/<pos>/start`; blank on sends `/panic`; blank off sends `/go`.
