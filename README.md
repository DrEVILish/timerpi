# TimerPi

![Version](https://img.shields.io/badge/version-2.0%20Rooms-7C3AED)

**Self-hosted cue timer and audience interaction for multi-room conferences.**
One appliance (a Raspberry Pi 4/5 or any Linux box) runs the whole event:

- walk-in signage for the event and for each room;
- in-room main screens that show polls, Q&A and word clouds on demand;
- speaker DSM / confidence timers;
- a room operator per room, plus a SuperOperator across all rooms;
- a Slido-style audience layer that phones join by QR code, scaled for 1,000+ devices per room.

Every screen is styled by [ftl-themes](https://github.com/DrEVILish/ftl-themes) and animates items in and out.

## Documentation

Read in this order:

| Doc | Answers |
|---|---|
| [docs/PRODUCT.md](docs/PRODUCT.md) | **What** we are building: the reference event, requirements, vocabulary. *Source of truth.* |
| [STATUS.md](STATUS.md) | **Where we are**: done, bugs, gaps, cleanup, open questions, suggested order |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | **How** it is built: packages, data model, engine, hub, layouts, auth, themes |
| [PROTOCOL.md](PROTOCOL.md) | Wire contract: every route and WebSocket frame |
| [AGENTS.md](AGENTS.md) | Rules for anyone (human or AI) changing this repo |
| [docs/OPS.md](docs/OPS.md) · [docs/PI-DEPLOY.md](docs/PI-DEPLOY.md) · [docs/HW-DRILLS.md](docs/HW-DRILLS.md) | Run, deploy and prove on hardware |
| [docs/OFFLINE-EDIT.md](docs/OFFLINE-EDIT.md) · [docs/TIMERPI-THEME-SPEC.md](docs/TIMERPI-THEME-SPEC.md) | Offline mesh editing spec · `timerpi` theme spec |
| [docs/archive/](docs/archive/README.md) | Historical notes. Not current |

## Quick start (development)

Requirements: Go 1.25, gcc (CGO for SQLite), git.

```sh
git clone https://github.com/DrEVILish/timerpi.git && cd timerpi
# Themes are NOT vendored or a submodule (see STATUS C2), so fetch them:
git clone https://github.com/DrEVILish/ftl-themes.git third_party/ftl-themes
make run            # http://localhost:8080, data in ./data
make test           # go test ./...
```

Then:

1. Open `http://localhost:8080`, create a room, and add sessions.
2. Open `http://localhost:8080/d/` in another window. It becomes a screen.
3. Capture that screen from the room's **Screens** page, and pick a template (DSM, Main, Room walk-in…).
4. Phones join at `/a/<room code>` (the QR is on the Main/Room templates).

## Where things live

`/` home · `/c/<code>` room operator · `/screens/<code>` room screens ·
`/super` SuperOperator · `/d/` new screen · `/d/<code>` room screen ·
`/a/<code>` audience · `/zone/<name>` event walk-in · `/settings` device.
