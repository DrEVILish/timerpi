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
| `GET /e/:code/leave` | — | "Leave event": drops this browser's sessions for that event (SuperOperator + its rooms), then home |
| `GET /super`, `GET /setup` | — | Retired; 302 to `/` |
| `GET /d/` | screen | READY card; registers in the waiting room until captured |
| `GET /d/:ident` | screen | `?view=stage` (default) \| `next` \| `daysheet` \| `clock` \| `board`. Board view: `&board=<id>`. Also `?screen=<name>`, `?theme=`, `?accent=`/`?bg=` (hex). `?edit=1&preview=1` = editor (mod) |
| `GET /a/:code` | audience | Phone page (open) |
| `GET /zone/:name` | — | Retired (STATUS C10); 302 to `/`. The event walk-in replaces it |
| `GET /favicon.ico` | browser | 301 to `/img/timerpi.svg` |
| `GET /health` | probe | `{ok, version, proto, role:"box"\|"cloud", uptime, device, title, sessions:{connected}}` |
| `GET /api/update/manifest?arch=` · `GET /api/update/binary?arch=` | open. Signed build for the boot-time updater: a published release in `<data>/releases/<arch>/`, else this binary (when its manifest is beside it). Manifest `{version, arch, sha256, size, sig}`, ed25519 over `timerpi-update/1\n<version>\n<arch>\n<sha256>\n<size>` |

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
| `POST /api/events/:code/login {pw, now?}` | Event Technician sign-in. `now` (browser ms) sets a box's clock when it was never set |
| `POST /api/events/:code/rooms/:room/login {pw}` | Moderator sign-in (`pw` empty when the room has none) → `{room:"/c/<code>"}` |
| `PATCH /api/events/:code {name?, theme?, password?, endsAt?}` · `DELETE /api/events/:code` | super. `endsAt` epoch ms (0 clears); screens release at end + 4 h |
| `GET /api/events/:code/live` | super. Per room: running, paused, overtime, remainingMS, active/next label, blanked, sessions, screens |
| `POST /api/events/:code/verb {verb, room?}` | super. `go next prev pause resume reset blank unblank`; no `room` = every room |
| `POST /api/events/:code/rooms {name}` · `PATCH …/rooms/:room {name?, password?, clearPassword?, pos?}` · `DELETE …/rooms/:room` | super. Room admin |
| `POST /api/events/:code/room-password {password}` | super. Same moderator password for every room ("" clears all) |
| `POST /api/events/:code/map {assetId}` | super. Event map (0 clears) |
| `POST /api/events/:code/pair {pairCode, code, kind, template, rotation, name?, theme?, room?}` | super. Pair the box showing `pairCode` (6 digits, registered in the last minute) as a screen of room `code`; 10 tries/min per event |
| `POST /api/events/:code/phone-link {base}` | super (needs an Event Technician Password). `{url, qr, expiresAt}`: a QR (PNG data URL) for `<base>/e/<code>/phone?t=<token>`. The token is `<expiry>.<nonce>.<HMAC>` keyed with the event's password hash (valid on every copy of the event), single-use, 2 minutes; a password change voids it |
| `GET /e/:code/phone?t=` | open (sign-in limiter). A good token sets this browser's Event Technician session and 303s to `/e/<code>/admin`; otherwise the "code expired" page. Never tunnelled: it signs in on the server the phone reached |
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
| `POST …/notes` · `POST …/daystart {hhmm}` | Room settings |
| `GET …/actions?limit=` · `GET/POST …/client-log` | Action log · browser error reports |
| `GET …/qr?data=&size=` | QR PNG (open; screens use it) |
| `DELETE …/sessions/:peer` | Kick a WS session |

### Screens, layouts, presets
| Method & path | Notes |
|---|---|
| `GET …/screens` · `GET …/screens/self?name=` | Registry ∪ live sessions (with preview data) · a screen's own config |
| `POST …/screens/config {name, theme?, boardId?, room?, kind?, rotation?}` | mod. Theme, layout, display type (`audience\|walkin\|presenter`), rotation (0/90/180/270) |
| `POST …/screens/match\|rename\|forget` | mod. Copy one screen's look to all · rename `{from,to}` · forget (drops the screen's key: it is released) |
| `POST …/screens/template {name, template}` | mod. The screen shows a built-in as is (`""` = plain timer); no copy is made. It never changes the screen's rotation. Retired keys (`<key>-portrait`, `countdown`) map to their layout |
| `GET …/layout-targets?kind=` | mod. Screens of that type in the event's rooms you moderate (SuperOperator: all), by room |
| `POST …/layouts {name, template\|fromBoard, screens:[{room,name}]}` | mod. New event layout from a built-in (or a layout); the listed screens switch to it → `{boardId}` |
| `POST …/screens/link {name}` | mod. `{link}`: the screen's own URL `/d/<room>?screen=<name>&key=<key>` (key created on first use) for opening a screen by hand |
| `GET/POST …/presets` · `POST …/presets/:pid/apply` · `DELETE …/presets/:pid` · `GET …/presets/:pid/export` · `POST …/presets/import` | Named screen assignment bundles |
| `GET /api/board-templates` | `{catalog:[{key,name,kind,desc,layout}], templates:{key: layout}}`. Every layout carries both versions: the landscape one, and the portrait one in `layout.alt` (event layouts too; saving a layout saves both) |
| `GET …/walkin` | open. Event walk-in feed: `{event:{name,map}, rooms:[{name, here, running, now, next, schedule[{label, speaker, startTS, endTS, state}]}]}` |
| `GET/POST …/boards` · `PUT/DELETE …/boards/:bid` | The event's own layouts `{v, rows, orientation, widgets[], alt}`. `PUT {name?, layout?}` is validated (types, overlap, limits). Nothing is seeded: an event has only the layouts someone made, and a board page with none shows the factory layout without saving it. `DELETE` sends its screens back to the plain timer |
| `POST /api/waiting/register {name,host,token,code?,handheld?}` · `GET /api/waiting/mine?name&host&token` | Screen side (open; per-IP budget). The token is the tab's random secret: only it claims the capture. `mine` → `{assigned, screen, key}`; the screen hops to `/d/<assigned>?screen=<screen>&key=<key>`. A box registers with its pairing `code`; once paired, its `mine` also carries `pairing:{event, eventName, meshKey, endsAt, room}` (boxes only) |
| `GET /api/waiting` · `POST /api/waiting/:id/capture {code,…}` · `DELETE /api/waiting/:id` | Any operator session; capture needs moderator access to the room. A screen already captured into another room → 409 |

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
| `POST …/polls/:pid/reset` | mod. Clear an item's votes, entries and spotlight; results go back to voting; targets unchanged |
| `GET /api/shows/:ident/polls/export` | mod. CSV download (UTF-8 BOM): Item, Type, Question, Response, Count, Status, Time — one row per option or entry |
| `POST …/polls/:pid/state {state}` | Legacy single verb (hidden / open / results on the audience target) |
| `GET /api/audience/:code` | open. `{data:{poll}, paused?}` — the audience-target item only. On the cloud, a venue event's item is the venue's; `paused:true` while its link is down (votes/asks then answer 503 `{paused:true}`) |
| `GET /api/pairing/status?event=CODE` | open. `{exists, released, endsAt, now}` — boxes poll it to learn their event ended or was deleted |
| `GET /api/pairing/self` · `GET /d/box` | box only, loopback only. The box's own screen: `{name, code?, paired, eventName, target}`; `/d/box` shows the code or frames the target |
| `GET /api/link?event=CODE` (WebSocket) · `POST /api/link/register {meshKey, bundle}` · `GET /api/link/bundle?event=CODE` | Signed `X-TimerPi-Auth: <unix s>.<hex HMAC-SHA256(mesh key, "timerpi-link/1\|purpose\|event\|s")>`. Link and register on the cloud only; the bundle (the whole event copy) from the cloud or a primary. Link frames: venue→cloud `bundle`, `air {room, poll}`, `res`, `ws`, `ws-close`, `final`; cloud→venue `req {method, path, header, body, as, peer}`, `ws-open {as, text: join}`, `ws`, `ws-close`, `final-ok`, see `routes/link.go` |
| `POST /api/audience/:code/vote {pollId, choice, peer}` | open. Poll/quiz choice, or upvote (`pollId` = entry). 1 per 300 ms per peer; 600/s per room → 429 + Retry-After |
| `POST /api/audience/:code/ask {item, text, peer}` | open. Submission to the item shown to the audience (pending unless auto-approve). 1 per 3 s per peer |
| `GET /api/audience/:code/qr` | open. Join QR for `/a/<code>` |

**PollView** = `{id, kind, question, options, correct (-1 until results, quiz only), state (hidden|open|results), toAudience, toPresenter, autoApprove, counts[], total, children[{id, question, state (open|answered; moderator view adds hidden|dismissed), upvotes}], spotlight, pending, waiting[]}`. Word-cloud children aggregate identical words; `upvotes` = senders. `waiting` (public views only) lists the ids still pending review — ids, never text — so a phone keeps only its own still-pending entries under "Waiting for review".

### Box, assets, OSC
| Method & path | Notes |
|---|---|
| `GET /api/theme` · `POST /api/theme` (box) | Box default theme (+ installed list from `ftl-themes/dist`) |
| `GET /api/network` · `POST /api/network/hostname\|role` · `GET /frag/network` | box. Device identity / mesh (identity carries `proto`, `updateNeeded`; peers carry `proto`, `foreign`) |
| `GET /api/network/update` · `POST /api/network/update` | box. The UPDATE button: `{current, available, source, updating}`; POST installs the newest signed build now (409 while a timer runs or an install is under way, 404 when nothing is newer) and restarts |
| `POST /api/network/mesh {country, ch24, ch5}` | box. Venue mesh radio: two-letter country, 2.4 GHz 1–13, 5 GHz 36/40/44/48; applied at the next boot |

mDNS (`_timerpi._tcp`) TXT: `host role ver epoch boot proto event sig`. A paired box sends `event` and `sig` (HMAC-SHA256 with the event mesh key over host, role, epoch, proto, event, boot; first 32 hex); only same-event signed peers count; an unpaired box announces role `unpaired`. The leading box also answers `timerpi.local`. A box ignores peers whose `proto` differs from its own (v2 boxes send none) for the election and takeover; a peer with a higher `proto` sets `updateNeeded`.
| `GET/POST /api/assets?event=CODE` (SuperOperator) or `?room=CODE` (moderator) · `DELETE /api/assets/:id` | Images belong to an event: list = that event's plus legacy unowned ones; upload (sniffed image type, 4 MiB) is owned by the named event; delete needs the owning event's SuperOperator (legacy: box admin) |
| `GET/POST /api/osc` · `POST /api/osc/test` | box. OSC bridge settings · send test packet |

## 3. WebSocket `/ws`

### Join (first frame, within 10 s)
```json
{"v":1,"t":"join","role":"controls|display|screen|audience","show":"<room code>",
 "peerId":"<uuid>","joinedAt":<ms>,"screen":"<name>","key":"<screen key>","handheld":true}
```
- **Trusted vs public** (BUGLOG RW9): operators, a screen whose `key` matches its `screen` name, and a display opened by a browser with moderator access are *trusted*. Everyone else gets the **public snapshot**: no show or cue notes, no cue tags, no stage messages, no `presenter` item, and no `oob` operator fragments. `joined.you.trusted` says which. The same rule applies to the first paint of `/d/` pages.
- `peerId` must be ≤ 64 plain characters (else one is generated). The same id in another role is refused; in the same role it replaces the stale session. `joinedAt` is clamped: never in the future; only operators keep it (up to a day back), screens and phones join "now".
- `controls` needs a moderator (or supervisor) session cookie on the upgrade request; otherwise `err` "moderator access required".
- `screen` is for screens only; it upserts the screens registry. `handheld` (optional) marks a phone/tablet display: it follows its own orientation, and the screens list reports `handheld:true` while it is live (the Screens page hides Mounted).
- `audience` joins a separate bucket (cap 4000/room). Others share the room bucket (cap 512).

### Server → client
| `t` | Payload | To |
|---|---|---|
| `joined` | `snapshot`, `you`, `peers` (audience: poll state only) | joiner |
| `state` | `snapshot` (full; newer `updatedAt` wins) | room bucket |
| `schedule` | `rows[{pos,startMS,endMS,holdMS,isBreak}]`, `totalMS`, `dayStartTS` | room bucket |
| `oob` | `html`, `target` (`#cuelist #tp-daybar #messages-panel #tp-now #d-stage #share-panel`) | room bucket |
| `poll` | `{v:1, poll: PollView\|null, presenter: PollView\|null, paused?, ts}`. Phones get `poll` (audience target) only; `paused` on the cloud while a venue event's link is down | audience + room buckets |
| `polls` | (refresh hint after any interaction change) | controls |
| `peers` | `[{peerId, role, joinedAt, screen}]` | room bucket |
| `display` | `{theme}` | targeted screen |
| `screen-board` | `{boardId}` (0 = back to stage) | targeted screen |
| `screen-rename` | `{name}` | targeted screen |
| `screen-look` | `{kind, rotation}` | targeted screen (also on join) |
| `screens` | (refresh hint) | controls |
| `signal` | `{from, data}` (WebRTC relay) | target peer |
| `flash` | `{ms}` — presenter screens blink the timer for `ms` | room bucket |
| `pong` | `{serverTime}` | sender (the footer's ping is this round trip) |
| `err` | `{message}`. "session deleted …" is terminal | sender |

Digits are **never** sent per second. Clients render from `anchorTS`, `rate`, `pausedElapsedMS` and `serverTime`.

### Client → server
- `{"t":"ping"}`. Screens ping every 20 s, operator pages every 5 s (and on join); the server pings every 30 s and drops after 75 s of silence.
- `{"t":"signal","to":"<peerId>","data":{…}}` (not allowed on the audience lane).
- `{"v":1,"t":"cmd","action":…,"args":{…}}`. Only `controls` sessions may send commands; every other role is read-only. Unknown roles are refused at join:

| action | args |
|---|---|
| `go` | `{pos?}` (re-runs a held cue at 0) |
| `start` · `jump` | `{pos}` · `{pos, start?}` |
| `pause` (toggle) · `reset` · `next` · `prev` | — |
| `rate` | `{rate}` ×0.5–×2.0 (clamped) |
| `blank` · `unblank` | — |
| `flash` | — (transient: fans out a `flash` frame, nothing stored) |
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
          "alert1MS":90000,"alert2MS":0,"alertColor1":"#ffaa00","alertColor2":"#ff4444","alertFlash1":false,"alertFlash2":false,
          "endAction":"HOLD","autoContinue":false,"startAt":"","notes":"","color":"",
          "updatedAt":0}],
 "messages":[{"id":1,"text":"WRAP UP","color":"","shownAt":1}],
 "poll":null, "presenter":null}
```
`timerpi/snapshot.go` is authoritative for the exact field set.

## 5. OSC (UDP)
- **Inbound** (when enabled): `/timerpi/<code>/<verb>`, verbs as in `cmd/:action` plus `blank`/`unblank`. This is unauthenticated, so keep it on a trusted LAN.
- **Outbound** to `osc.out.host:port`: a cue start sends `/cue/<pos>/start`; blank on sends `/panic`; blank off sends `/go`.
