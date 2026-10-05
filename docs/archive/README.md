# Archive — historical notes (do not treat as current)

These files were the working notes of earlier development rounds. They were
archived during the 2026-10-05 documentation reset because they mixed plans,
history and current truth, and had drifted from the code.

**They are kept for context only. When they disagree with the current docs,
the current docs and the code win.**

Current docs: [../PRODUCT.md](../PRODUCT.md) (what), [../ARCHITECTURE.md](../ARCHITECTURE.md) (how),
[../../PROTOCOL.md](../../PROTOCOL.md) (wire contract), [../../STATUS.md](../../STATUS.md) (done/open).

| File | Was | Notes |
|---|---|---|
| `PLAN-v1-v2.md` | `PLAN.md` | v1 plan + "PLAN MAJOR v2 Rooms" §11. Code comments that say "PLAN §11.x" point here |
| `PROJECT-ledger.md` | `PROJECT.md` | Feature ledger Tiers A–F + a history log of every round to 2026-10-05. Code comments citing ladder IDs (A1, B7, E3, F1, …) point here |
| `CONTRACT-UI.md` | `templates/CONTRACT-UI.md` | Template/element-id contract plus appended batch notes. §5–§7 (frames, oob targets, element ids) are still mostly accurate; `/frag/shows` (§3) never existed |
| `NOTES-board.md` | `NOTES-board.md` | Layout-board handoff (11 widgets at the time; now 17) |
| `NOTES-timerpi.md` | `timerpi/NOTES-timerpi.md` | Domain/engine handoff + deviations from the old protocol |
| `NOTES-importdocs.md` | `importdocs/NOTES-importdocs.md` | Import column rules. Still a useful reference for import parsing |
| `NOTES-mdns.md`, `NOTES-drm.md` | package dirs | mDNS and DRM handoffs (`drm/README.md` remains the live DRM doc) |

Files cited by old notes and code comments that **never existed in the repo**:
`reviews/UX1-operator-report.md`, `reviews/UX2-display-report.md`,
`reviews/SUPERVISOR-report.md`, `reviews/FIX3-report.md`,
`reviews/FIXO-report.md`, `reviews/NOTES-display.md`, `reviews/NOTES-ops.md`,
`reviews/NOTES-setup.md`. Cleanup item C6 in STATUS.md repoints those comments.
