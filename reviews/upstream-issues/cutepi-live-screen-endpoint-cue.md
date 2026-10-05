# CuTePi — request: become a live TimerPi screen endpoint via a special cue item

**Filed as:** [CuTePi issue #4](https://github.com/DrEVILish/CuTePi/issues/4),
owner direction, 2026-10-05, from TimerPi (sibling appliance,
DrEVILish/timerpi). Per the contributors' house rule we file requests and
never push to the CuTePi repository. This issue is the full request.
**Type:** feature request. No repro needed.

## Why

TimerPi v2 "Rooms" runs events that pair one CuTePi box per room as the
media destination (walk-in video loops, interstitials, panic holding image).
On the TimerPi side the OSC bridge in (BLANK ↔ panic image, cue start ↔ GO)
is prototyped. What CuTePi cannot currently show is **TimerPi's live
displays** — the room timer, per-room polls/questions/results, or the
walk-in day-schedule page — as cue content on the same HDMI wall. Today
those live on different screens, so a room's main wall cannot cut between
"video" and "the live timer/poll wall" from one cue list.

## The proposal: a "live endpoint" cue item

A second cue media class, alongside video/image/audio:

- **In the media pool**: an "endpoint" tile — base URL pointing at a
  TimerPi display page (e.g. `https://timerpi-a.local/d/AQ2D-7WKP/timer`
  or that room's poll/results page) plus a **pairing token**. TimerPi can
  issue room-scoped, revocable device tokens (capability records in our
  device mesh, PLAN §11.6); the token travels as a URL parameter at fire
  time and never needs to live in the cue text.
- **When fired**: the wall shows the live endpoint instead of local media —
  crisp, live-updating timer digits or poll results, whatever the page does.
- **When it ends**: CuTePi returns to the normal media cue stream exactly
  as after any other cue (fades per the cue's own settings).
- **Failure posture**: if the endpoint stops responding mid-cue, CuTePi
  cuts to its panic holding image (never a black wall, never a browser
  error page) and reconnects with a short backoff; page restored, image
  restored.

## Constraints checked against CuTePi's own DESIGN before filing

- CuTePi is headless-KMS on Pi 4/5, deliberately no browser and no
  windowing system, holding DRM master with the KMS plane wall or the GPU
  compositor wall (`CUTEPI_WALL=gl`). Whatever renders the page must feed
  the wall's layer path like any other source — it must never fight the
  service for the display.
- We are browser-stack-agnostic about the inside; both a WPE-based
  renderer texture and a headless-render-to-texture route land in the
  same wall path. The choice (and cost measurement on the Pi) is entirely
  CuTePi's call — TimerPi only needs: accepts `http(s)` + token, obeys
  cue fades, hands back at cue end.
- TimerPi pages are plain HTTPS web pages; no websocket beyond what the
  page itself opens from the appliance's LAN (that is the same trust
  surface CuTePi's own control UI already assumes).

## Acceptance criteria

1. An operator adds an endpoint cue to the media pool; it fires through
   the normal transport (GO, pre/post wait, fades) like any cue.
2. While firing, the wall shows the live TimerPi page — genuinely live
   (the timer digits tick; poll results update), not a screenshot.
3. Endpoint drop mid-cue → panic holding image; automatic return when the
   page is reachable again, without operator action.
4. Cue end restores the media stream; no stuck layers or vanishing
   windows; unplayable/absent endpoint at cue build time behaves like a
   missing media source (warning, re-link flow).

## Worked example (the room this exists for)

Room A's wall: 08:55 walk-in video (media cue) → 09:00 "Room A live timer"
(endpoint cue) → speaker Q&A: "polls/results page" (endpoint cue) → award
video next — all one CuTePi cue sheet, driven by the room's TimerPi GO via
OSC. TimerPi becomes both the control surface and one of CuTePi's sources;
CuTePi becomes one of TimerPi's screen endpoints.

Happy to review any design note or prototype against the wall's own rules —
reviewed from the TimerPi side whenever useful.
