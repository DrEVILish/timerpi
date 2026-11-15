# UI critique and optional design recommendations

Reviewed 2026-11-15 against TimerPi `9694670` and ftl-themes
`01f445f41fd59ad504d2d0ed60d49e9806b67c60`.

**These are optional proposals, not approved requirements or implemented UI
changes.** The strongest opportunity is to improve operational hierarchy and
space on tablets, while keeping the existing components and capabilities.

## Evidence and scope

Manual Chromium review used an isolated development server, disposable data,
and no Pi access. The fixture had two rooms, fourteen sessions including a
long title and speaker name, a running timer, a live poll on both targets,
a hidden Q&A item, a presenter message, and a browser captured as an audience
screen. Only fixture data was changed.

Inspected:

- Home at 1280×800 in `blue-future`.
- Run and Audience at desktop and 1024×768 tablet-sized viewports;
  the room page in both `blue-future` and the light `material` theme.
- Event Technician Live, Screens empty/waiting states, and the screen setup
  modal at a tablet-sized viewport.
- Audience phone at 390×844, before voting and after confirmed voting.
- Captured audience display at 1280×720 after its entry animation completed.

Viewport resizing does not emulate a physical coarse pointer or mobile user
agent. No touch-target compliance or contrast certification is inferred.
Populated Q&A moderation, error/offline states, portrait displays, the full
layout editor, physical TVs, keyboard-only workflows, assistive technology,
and the complete theme catalogue were not visually audited in this pass.
Source-based observations are identified separately below.

## What to preserve

- The large countdown, distinctive GO, persistent transport and active/next
  cue treatment provide a sound live-control foundation.
- The Run/Audience split retains the transport and room context across tabs.
- The phone poll has clear question/answer hierarchy, a visible selected
  answer, explicit “Vote received” copy and change-of-answer guidance.
  The reviewed phone view did not overflow horizontally.
- The audience display gives the question and QR room to breathe, with no
  operator controls. Preserve the library's signage and countdown primitives.
- Screen setup already uses `dialog.modal`, `.modal-header`, `.btn-close`
  and `.modal-footer`, with Cancel before the primary action. Its footer
  was reachable in the reviewed tablet viewport. Keep that contract.
- Inline editing already uses ftl editing/saving/saved states. Do not replace
  it with a second edit system just to obtain different styling.

## Optional recommendations

Priority is suggested design value, not a claim that an existing feature is
broken. Each proposal uses components verified in the checked-in library.
The acceptance checks apply only if that proposal is selected for implementation.

### UI1 — Reclaim tablet header space (high; N11 / L6)

**Observed:** In `blue-future`, the tablet header wraps Page theme and Leave
onto a second row. The current cue, tabs, running order, side column and
bottom transport then compete for the remaining height. The same viewport
in `material` has a shorter header; this is theme-dependent, not solely a
screen-width issue.

**Option:** Keep the event/room breadcrumb and room switcher immediately
available. Put browser-level actions such as Page theme and Leave in a
labelled Page options popover on tablet widths. Keep event-level navigation
accessible rather than removing it. Retain the current confirmation behavior
for Leave.

**Library basis:** `.app-bar`, `.breadcrumbs`, `.breadcrumb-item`, `.btn`,
`.dropdown[popover]`, `.dropdown-item`; no app-owned menu chrome.
[Contract](../third_party/ftl-themes/CONTRACT.md),
[navigation examples](../third_party/ftl-themes/components-navigation.html).

**Acceptance:** Run and Audience retain the clock, transport and room identity
at 1024×768 in both reviewed themes. Every moved action remains keyboard
reachable, has a visible label in the menu, and returns focus on close.
Measure the working-space gain without shrinking controls below library
minimums. Do not assume desktop-pointer screenshots prove touch usability.

**Current seam:** [base.html](../templates/base.html),
[timerpi.css](../public/css/timerpi.css).

### UI2 — Keep a compact live-target summary in view (high; A4 / T5)

**Observed:** On Run, Messages is deliberately ordered above On air now.
A live message and the long poll make the side column taller than the
available area. The operator must scroll to inspect some live audience state.
On Audience, the target controls and full question consume much of that column.

**Option:** Start the column with a compact summary of Audience and Presenter
outputs. Keep the message input and Send visible. Put message history and
expanded result detail behind native disclosures, not the urgent controls.
The summary should state the actual item and phase, not just a coloured lamp.

**Library basis:** `.panel`, `.panel-header`, `dl.props`, `.status`, `.badge`,
`.accordion-item`, `.accordion-trigger`, `.accordion-panel`.
[Property grid](../third_party/ftl-themes/docs/components/instruments.md),
[disclosure markup](../third_party/ftl-themes/CONTRACT.md),
[section examples](../third_party/ftl-themes/components-sections.html).

A possible composition, not an implementation:

```html
<section class="panel" aria-label="Live screen outputs">
  <div class="panel-header">On air now</div>
  <dl class="props">
    <dt>Audience</dt><dd>Poll · voting open</dd>
    <dt>Presenter</dt><dd>Poll · voting open</dd>
  </dl>
  <!-- Keep the existing message input and Send here. -->
  <details class="accordion-item">
    <summary class="accordion-trigger">Message history</summary>
    <div class="accordion-panel"><!-- Existing message cards. --></div>
  </details>
</section>
```

**Acceptance:** With a long poll and several messages, both output summaries
and Send remain discoverable without scrolling through history. Independent
target visibility and message delivery semantics remain unchanged.

**Current seam:** [dashboard.html](../templates/dashboard.html),
[moderate.js](../public/src/moderate.js),
[timerpi.css](../public/css/timerpi.css).

### UI3 — Make the current session identifiable without hover (high; T1 / N11)

**Observed:** The desktop now band ellipsizes the fixture's long session title.
The running-order row wraps the title but clips the speaker name. The large
clock remains readable, but identifying the session requires looking elsewhere.

**Option:** Allow a controlled two-line title in the now band and provide a
visible way to read the complete title/speaker through the existing cue-details
modal. Preserve the countdown's prominence. Keep the table's sticky header,
active-row treatment and editable-cell states. Consider defaulting less-used
alert configuration columns to the existing details editor during live work,
while preserving access to every field.

**Library basis:** `.countdown`, `.panel`, `.table.is-sticky`, `.is-active`,
`.is-editable`, `.is-saving`, `.is-saved`, `.modal`.
[Table contract](../third_party/ftl-themes/docs/components/tables.md),
[table examples](../third_party/ftl-themes/components-tables.html),
[countdown](../third_party/ftl-themes/docs/components/signage.md).
Wrapping and placement belong in app layout CSS; typography, colour and
component chrome remain library-owned.

**Acceptance:** The reviewed long title and speaker can be read without a
hover-only tooltip. A normal-length title still leaves useful cue rows visible.
No additional scrolling layer or second cue-editing system is introduced.

**Current seam:** [current.html](../templates/fragments/current.html),
[cuelist.html](../templates/fragments/cuelist.html),
[timerpi.css](../public/css/timerpi.css).

### UI4 — Separate transport, attention and event-wide scope (medium; T2 / T7)

**Observed:** GO, navigation, FLASH, WRAP UP and BLANK share one transport
surface. Event Technician Live also gives phone sign-in, Blackout all and
Restore all comparable placement. Existing labels already distinguish the
all-room actions; the opportunity is grouping, not fixing missing wording.

**Option:** Keep the current one-tap controls. Group cue transport around GO,
then separate attention/output controls with the library's rule/group spacing.
On the event page, group all-room blackout/restore apart from phone sign-in
and room-configuration tools. Optionally move password/import setup tools to
Setup; leave a clear route to them and retain quick Add room if it is useful.

**Library basis:** `.transport`, `.btn-go`, `.btn-group`, `.btn-toolbar`, `.vr`,
`.badge`, `.tabs`, `.tab`, `.btn-danger`.
[Contract](../third_party/ftl-themes/CONTRACT.md),
[forms examples](../third_party/ftl-themes/components-forms.html).

**Acceptance:** Operators distinguish “this room” from “all rooms” by text
and grouping, not colour alone. GO and Blackout remain one-tap actions;
no confirmation is added to the live transport. This does not approve moving
setup tools or changing any owner-specified transport behavior.

**Current seam:** [dashboard.html](../templates/dashboard.html),
[event.html](../templates/event.html).

### UI5 — Use consistent verbs for the two push targets (medium; A4)

**Observed:** The library table uses Show/Hide under Audience and Presenter
columns, while the live card uses Off Audience / Off Presenter and Show
results. Both work, but switch between target names, state and action verbs.

**Option:** Use one consistent action vocabulary, such as Show to audience /
Hide from audience and the equivalent presenter wording. Keep Results as a
separate phase action. If toggles are preferred, use two independent ftl
checkbox-backed toggle buttons with their documented on/off labels.

**Library basis:** `.toggle-btn`, `.toggle-btn-off`, `.toggle-btn-on`, `.badge`,
`.btn-toolbar`.
[Toggle contract](../third_party/ftl-themes/CONTRACT.md),
[forms reference](../third_party/ftl-themes/docs/components/forms.md).
Do **not** use a mutually exclusive `.segmented` control: both targets may
be on simultaneously. Retain text alternatives and current pressed states.

**Acceptance:** Audience only, Presenter only, both and neither remain
representable. Pending/failed writes never leave a false selected state.
Long labels fit tablet widths without shifting adjacent actions.

**Current seam:** [moderate.js](../public/src/moderate.js).

### UI6 — Offer a preview before committing screen setup (medium; S1–S3)

**Observed:** The capture modal explains roles and describes the selected
layout, but shows no visual preview before Set up screen. Previews already
exist on configured screen cards; this proposes earlier feedback, not a
replacement gallery or a new layout editor.

**Option:** Add a read-only preview of the selected role/layout, plus a short
rotation/theme summary, inside the existing ftl modal. Reuse the existing
layout rendering path; do not build a second preview renderer. Use `.modal-lg`
only if the preview needs it, and preserve a usable narrow-viewport layout.

**Library basis:** `.modal`, `.modal-lg`, `.modal-header`, `.modal-footer`,
`.card.has-media`, `.card-media`, `.card-body`, `dl.props`;
`--card-media-ratio` supplies the actual selected orientation.
[Media-card reference](../third_party/ftl-themes/docs/components/surfaces.md),
[surface examples](../third_party/ftl-themes/components-surfaces.html),
[modal contract](../third_party/ftl-themes/CONTRACT.md).

**Acceptance:** Preview agrees with role, orientation and selected theme.
Cancel remains side-effect free. Cancel and Set up screen remain reachable;
no custom backdrop, padding, border or action row is added. The quick
“Show the timer here” path remains available.

**Current seam:** [screens.html](../templates/screens.html),
[screens.js](../public/src/screens.js).

### UI7 — Reduce first-visit choices on Home (low; E1 / E2)

**Observed:** Home gives Join, the full Create form, and Open a screen three
parallel panels. The create form is substantially taller, and Open a screen
is also present in the header. This is an information-hierarchy choice,
not a failed workflow.

**Option:** Keep Join immediately usable. Consider placing the longer Create
form behind an explicit disclosure while retaining an obvious Create heading,
and use the screen-opening panel for concise setup guidance. Tradeoff: this
adds a step for a first-time Event Technician, so prefer it only if returning
moderators are the common entry audience.

**Library basis:** Existing `.panel`, `.field`, `.input`, `.btn-primary`,
`.btn-secondary`, plus `.accordion-item` / `.accordion-trigger` /
`.accordion-panel` if selected.
[Contract](../third_party/ftl-themes/CONTRACT.md),
[forms examples](../third_party/ftl-themes/components-forms.html).

**Acceptance:** Join, Create and Open a screen all remain easy to find.
Creation errors focus and reveal the relevant field. No capability is retired
and no new wizard dependency is introduced.

**Current seam:** [home.html](../templates/home.html).

### UI8 — Offer library-native operator readability preferences (low; L1 / L6)

**Observed:** The same tablet composition wraps differently between the
monospaced `blue-future` and light `material` themes. Current Page theme is
already browser-local; a theme switch should not be the only route to larger
text or stronger contrast.

**Option:** Consider adding only text-size and contrast preferences to the
browser-local Page options. Treat this as an optional capability, not an
accessibility failure proven by this review. Preserve the selected theme
instead of overriding it with custom colours or fonts.

**Library basis:** `.prefs`, `.prefs-item`, `.prefs-label`, `.segmented`,
`data-text-size`, `data-contrast`; the library already defines these attributes.
[Experience reference](../third_party/ftl-themes/docs/components/experience.md),
[experience examples](../third_party/ftl-themes/components-experience.html).
No motion preference change is proposed: display screens still always animate,
and only the audience page honours reduced-motion preferences.

**Acceptance:** Larger text does not hide transport or modal actions, high
contrast preserves state labels, and browser-local preferences do not alter
room screens or another operator's browser.

**Current seam:** [base.html](../templates/base.html),
[theme.js](../public/src/theme.js).

## Suggested selection and validation

Start with UI1–UI3 if tablet operation is the priority. UI4–UI6 are separate
choices; UI7–UI8 are lower-priority experiments with explicit tradeoffs.
Nothing in this document automatically becomes release scope.

For any selected change, add behavior checks to `tools/browser-tests/` and
review 1280×800, 1024×768 and 390×844 in both themes. Include long text, empty
and populated states, keyboard focus, actual coarse-pointer hit areas and
larger text. Keep the working phone poll, signage-only displays, simultaneous
push targets, inline save feedback and existing ftl modal structure.

Every proposed surface above has an existing library component. No new
component or upstream feature request is necessary for these options. Only
app layout, application state and accessibility wiring would need development;
ftl-themes retains tokens and component chrome. The dependency was not edited.
