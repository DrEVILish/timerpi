# TimerPi — Wire Protocol (HTTP routes + WebSocket frames)

> The contract between server and clients. **It describes the code as it is
> today**: if you change a route or frame, change this file in the same commit.
> Background: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Last verified 2026-10-05 (`ccd415f`).

**Conventions**

- `:ident` and `:code` are always the 8-char share code (`K7QP-M3XB` or `K7QPM3XB`). Numeric IDs return **404**.
- "gated" means it needs the room password cookie (`tp_show_<CODE>`) when that room has a password.
- "auth" means it needs the device password (cookie `tp_auth` or Basic `operator:<pw>`) when one is set. A browser GET gets a 302 to `/login`; any other caller gets a 401 JSON.
- Everything goes through OriginGuard (strict mode only when `allowed_hosts` is set).
- Bodies are capped at 8 MiB, or 32 MiB on `/import` paths.

---

## 1. Pages

| Route | Who | Notes |
|---|---|---|
| `GET /` | operator | Home: create or join a room, recent rooms (browser-local), Open Display |
| `GET /c/:ident` | room operator | Dashboard: running order, transport, messages, Audience panel, share |
| `GET /screens/:ident` | room operator | Screens gallery: capture, preview, theme/layout, layout editor |
| `GET /super` | SuperOperator | Cross-room panel (polls `/api/super/rooms` every 2 s) |
| `GET /settings`, `GET /setup`, `GET /setup/sheet` | operator | Device identity, theme, OSC; first-run wizard; printable QR sheet |
| `GET /login`, `GET /logout` | — | Device-password login |
| `GET /d/` | screen | READY card; registers in the waiting room until captured |
| `GET /d/:ident` | screen | `?view=stage` (default) \| `next` \| `daysheet` \| `clock` \| `board`. Board view: `&board=<id>`. Also `?screen=<name>`, `?theme=`, `?accent=`/`?bg=` (hex). `?edit=1&preview=1` = editor (auth) |
| `GET /a/:code` | audience | Phone page (gated by the room password). **Not auth-exempt yet (STATUS B1)** |
| `GET /zone/:name` | screen | Event walk-in for a zone (server-rendered, 30 s refresh; to be replaced by a live event walk-in, STATUS N7) |
| `GET /health` | probe | `{ok, version:"2.0", uptime, device, title, sessions:{connected}}` |

Static: `/ftl/*` (ftl-themes tree), `/css/*`, `/src/*`, `/img/*` (from `public/`), `/assets/:id` (uploaded blobs, public).

## 2. REST API

### Rooms (shows)
| Method & path | Notes |
|---|---|
| `GET /api/shows` · `POST /api/shows {title}` | List (code and title only after auth) · create |
| `GET/DELETE /api/shows/:ident` | Snapshot (gated) · delete |
| `POST …/clone {title?}` | New code, same running order, no password |
| `POST …/blank {on}` | Room blackout (+ OSC panic/go) |
| `POST …/cmd/:action` | Remote control: `go start pause resume reset next prev jump rate daystart`. Body = WS args |
| `PUT …/cues` · `POST …/moveto {pos,to}` · `POST …/sync` | Replace running order · move · offline-master merge push |
| `POST …/import` (multipart `file`, `kind`, `mode=replace\|append`) · `GET …/import-example?fmt=` · `GET /api/import-example?fmt=` | Running-order import |
| `GET …/file` · `POST /api/shows/import-file` | v2 full show bundle export/import (import creates a new room) |
| `POST …/messages {text,color?,show?}` · `POST …/messages/:mid/show\|hide` · `DELETE …/messages/:mid` | Stage messages |
| `POST …/notes` · `POST …/daystart {hhmm}` · `POST …/zone {zone}` | Room settings |
| `POST …/passphrase {pw}` · `POST …/unlock {pw}` | Room password set/clear · unlock (issues cookie + `showToken`) |
| `GET …/actions?limit=` · `GET/POST …/client-log` | Action log · browser error reports |
| `GET …/qr?data=&size=` | QR PNG (auth-exempt for screens) |
| `DELETE …/sessions/:peer` | Kick a WS session |

### Screens, layouts, presets
| Method & path | Notes |
|---|---|
| `GET …/screens` · `GET …/screens/self?name=` | Registry ∪ live sessions (with preview data) · a screen's own config |
| `POST …/screens/config\|match\|rename\|forget` | Assign theme/board · match all · rename · forget |
| `GET/POST …/presets` · `POST …/presets/:pid/apply` · `DELETE …/presets/:pid` · `GET …/presets/:pid/export` · `POST …/presets/import` | Named screen assignment bundles |
| `GET /api/board-templates` | Built-in templates (single source: `boards.TemplateLayouts`) |
| `GET/POST …/boards` · `PUT/DELETE …/boards/:bid` | Layouts. `PUT {name?, layout?}` is validated (types, overlap, limits) |
| `POST /api/waiting/register {name,host}` · `GET /api/waiting/mine?name&host` | Screen side (auth-exempt) |
| `GET /api/waiting` · `POST /api/waiting/:id/capture {code,…}` · `DELETE /api/waiting/:id` | Operator side |

### Audience interactions
| Method & path | Notes |
|---|---|
| `GET/POST /api/shows/:ident/polls` | List · create `{kind, question, options[], correct?}`. Always created `hidden` |
| `POST …/polls/:pid/state {state}` | `hidden` \| `open` \| `results` (opening one top-level item hides the others) |
| `DELETE …/polls/:pid` | Delete (404 if unknown) |
| `GET /api/audience/:code` | Audience read: only on-air items |
| `POST /api/audience/:code/vote {pollId, choice, peer}` | 1 per 300 ms per peer; 600/s per room → 429 + Retry-After |
| `POST /api/audience/:code/ask {kind, text, parent, peer}` | Question/word/idea submission (moderated, lands hidden); 1 per 3 s per peer |
| `GET /api/audience/:code/qr` | Join QR for `/a/<code>` |

### SuperOperator, device, assets, OSC
| Method & path | Notes |
|---|---|
| `GET /api/super/rooms?zone=` | Per-room live state + session counts |
| `POST /api/super/verb {code, verb}` · `POST /api/super/bulk {verb, zone?}` | Verbs: `go next prev pause resume reset blank unblank` |
| `POST /api/login {pw}` · `POST /api/auth/password {pw}` | Device password login · set/clear |
| `GET/POST /api/theme` | Appliance default theme (+ installed list from `ftl-themes/dist`) |
| `GET /api/network` · `POST /api/network/hostname\|role` · `GET /frag/network` | Device identity / mesh |
| `POST /api/setup/identity\|create\|import` | First-run wizard |
| `GET/POST /api/assets` · `DELETE /api/assets/:id` · `POST /api/zone-map {zone, assetId}` | Uploads (sniffed MIME, 4 MiB) · zone map pointer |
| `GET/POST /api/osc` · `POST /api/osc/test` | OSC bridge settings · send test packet |

## 3. WebSocket `/ws`

### Join (first frame, within 10 s)
```json
{"v":1,"t":"join","role":"controls|display|audience","show":"<code>",
 "peerId":"<uuid>","joinedAt":<ms>,"authToken":"…","showToken":"…","screen":"<name>"}
```
- `controls` needs `authToken` when a device password is set.
- `showToken` (or the cookie) is needed when the room has a password.
- `screen` is for screens only; it upserts the screens registry.
- `audience` joins a separate bucket (cap 4000/room). Others share the room bucket (cap 512).

### Server → client
| `t` | Payload | To |
|---|---|---|
| `joined` | `snapshot`, `you`, `peers` (audience: poll state only) | joiner |
| `state` | `snapshot` (full; newer `updatedAt` wins) | room bucket |
| `schedule` | `rows[{pos,startMS,endMS,holdMS,isBreak}]`, `totalMS`, `dayStartTS` | room bucket |
| `oob` | `html`, `target` (`#cuelist #tp-daybar #messages-panel #tp-now #d-stage #share-panel`) | room bucket |
| `poll` | `{v:1, poll: PollView\|null, ts}`; PollView = `{id, kind, question, options, correct, state, counts, total, upvotes, children[]}` | audience + room buckets |
| `peers` | `[{peerId, role, joinedAt, screen}]` | room bucket |
| `display` | `{theme}` | targeted screen |
| `screen-board` | `{boardId}` (0 = back to stage) | targeted screen |
| `screen-rename` | `{name}` | targeted screen |
| `screens` | (refresh hint) | controls |
| `signal` | `{from, data}` (WebRTC relay) | target peer |
| `pong` | `{serverTime}` | sender |
| `err` | `{message}`. "session deleted …" is terminal | sender |

Digits are **never** sent per second. Clients render from `anchorTS`, `rate`, `pausedElapsedMS` and `serverTime`.

### Client → server
- `{"t":"ping"}`. Client pings every 20 s; the server pings every 30 s and drops after 75 s of silence.
- `{"t":"signal","to":"<peerId>","data":{…}}` (not allowed on the audience lane).
- `{"v":1,"t":"cmd","action":…,"args":{…}}`. The audience lane is refused always; other non-controls roles are refused when a password is set (STATUS B6):

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
 "poll":null}
```
`timerpi/snapshot.go` is authoritative for the exact field set.

## 5. OSC (UDP)
- **Inbound** (when enabled): `/timerpi/<code>/<verb>`, verbs as in `cmd/:action` plus `blank`/`unblank`. This is unauthenticated, so keep it on a trusted LAN.
- **Outbound** to `osc.out.host:port`: a cue start sends `/cue/<pos>/start`; blank on sends `/panic`; blank off sends `/go`.
