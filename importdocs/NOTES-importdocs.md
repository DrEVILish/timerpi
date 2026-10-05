# NOTES-importdocs.md — REST wiring for Agent D-full

Package `importdocs/` is complete and self-contained: `go build ./importdocs/...`
and `go test ./importdocs/ -count=1` pass. This file is the wiring contract for
the routes layer (PROTOCOL.md §REST) plus the parsed-rules summary.

## 1. Public API surface (exact signatures)

```go
package importdocs

// Document parsing → []Cue in document order (never blank rows).
func Parse(data []byte, kind string) ([]Cue, error)        // kind: "auto"|"xlsx"|"xls"|"csv"|"json", ""/unknown-cased ok
func ParseFilename(data []byte, filename string) ([]Cue, error) // ext-based guess + fallback sniff
func ParseXLSX(data []byte) ([]Cue, error)                 // excelize; first sheet with a cue table wins
func ParseXLS(data []byte) ([]Cue, error)                  // tealeg/xlsx, best-effort — see §5
func ParseCSV(data []byte) ([]Cue, error)                  // utf8 (BOM optional), comma/semi/tab sniffed, header row REQUIRED
func ParseJSON(data []byte) ([]Cue, error)                 // array, {"cues":[…]} wrap, per-element {"cue":{…}} wrap

// Duration rules shared by every format.
func ParseDurationMS(s string) (int64, error)

// Example documents for the Import dialog / download links.
func ExampleXLSX() ([]byte, error) // README sheet first + "Cues" sheet (bold frozen header, widths)
func ExampleCSV() []byte           // UTF-8 WITH BOM, CRLF, comma
func ExampleJSON() []byte          // PROTOCOL wire keys, MarshalIndent

type Cue struct { // mirror of the PROTOCOL wire cue object — same JSON keys
    Label string `json:"label"`; DurationMS int64 `json:"durationMS"`
    Kind string `json:"kind"`; Tags string `json:"tags"`; Speaker string `json:"speaker"`
    HoldMS int64 `json:"holdMS"`; TimerKind string `json:"timerKind"`
    Alert1MS int64 `json:"alert1MS"`; Alert2MS int64 `json:"alert2MS"`
    AlertColor1 string `json:"alertColor1"`; AlertColor2 string `json:"alertColor2"`
    EndAction string `json:"endAction"`; AutoContinue bool `json:"autoContinue"`
    Notes string `json:"notes"`; Color string `json:"color"`
}
```

`adapter.go` (imports `timerpi` — drop/rebuild this file alone if the domain
package is ever mid-refactor and you need importdocs standalone):

```go
func (c Cue) ToTimerpiCue() (timerpi.Cue, error)        // + Normalize + Validate (defaults filled)
func ToTimerpiCues([]Cue) ([]timerpi.Cue, error)        // first failing row = error "cue N: …"; Pos stays 0
func FromTimerpiCue(timerpi.Cue) Cue                    // for JSON export (round-trip)
func FromTimerpiCues([]timerpi.Cue) []Cue
```

## 2. Route wiring to add in `routes/`

### POST /api/shows/:id/import — file upload

Multipart fields per PROTOCOL: `file` (the document), `kind` =
`auto|xlsx|xls|csv|json` (default auto). `?mode=replace|append` (or a
`mode` form field; replace is the safe default — full re-import is the main
use case for "download example → edit → upload").

```go
fh, err := c.FormFile("file"); // <input type="file" name="file">
kind := c.PostForm("kind")     // "" = auto = byte sniffing
mode := c.DefaultPostForm("mode", c.DefaultQuery("mode", "replace"))

f, err := fh.Open(); defer f.Close()
data, err := io.ReadAll(f)

cues, err := importdocs.ParseFilename(data, fh.Filename)
if kind != "" { cues, err = importdocs.Parse(data, kind) }   // explicit kind wins
if len(cues) == 0 && err == nil { err = errors.New("empty document") } // refuse no-op imports

tcs, err := importdocs.ToTimerpiCues(cues)   // ""-kind rows validated here
switch mode {
case "append":
    for _, tc := range tcs { if _, err := db.CreateCue(showID, tc); err != nil { … } } // Pos 0 appends
default: // replace (default), incl. for PUT-less import buttons
    err = db.ReplaceCues(showID, tcs)        // renumbers Pos 1..N, one tx
}
```

Error shape per PROTOCOL §Htmx: mutating ops must NOT replace panels with
errors — respond status+message (`c.Data(http.StatusUnprocessableEntity, …)` or
an hx-swap-none styled fragment), 200 + `outerHTML` fragment on success.
Parse errors already quote row/cue numbers ("importdocs: row 3: …"), so show
them verbatim in the import modal.

### GET /api/shows/:id/import-example?fmt=xlsx|csv|json — example download

```go
switch c.Query("fmt") {
case "xlsx": body, err := importdocs.ExampleXLSX() // built here only to attach the error path
    Content-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
    Content-Disposition: attachment; filename="timerpi-cue-list-example.xlsx"
case "csv": body := importdocs.ExampleCSV()  // BOM already included
    Content-Type: text/csv; charset=utf-8 ; filename="timerpi-cue-list-example.csv"
case "json": body := importdocs.ExampleJSON()
    Content-Type: application/json ; filename="timerpi-cue-list-example.json"
}
```

(Show-id is accepted in the path for consistency with the other cue routes but
the examples are static documents. If a "current show" prefill is later
wanted, use importdocs.FromTimerpiCues(db.ListCues(showID)) instead of the
Example* generators — same shape.)

Same template for the show-wide JSON export if you wire one:
`json.MarshalIndent(importdocs.FromTimerpiCues(cues), "", "  ")`.

## 3. Column parsing rules (applies to all formats)

Header detection: the FIRST row containing at least one recognized synonym is
the header ("first data row wins"); titles above it are skipped. XLSX tries
sheets in order and takes the FIRST SHEET with a cue table, so a README/instructions
sheet in front is fine. Matching is case/whitespace/punctuation-insensitive
("End Action (hh:mm:ss)" ≡ "end_action"). Unknown columns are ignored.

| Canonical      | Accepted headers                                                               |
|----------------|--------------------------------------------------------------------------------|
| label          | label, title, name, cue, cue label, cue title, item                            |
| duration       | duration(s), time, minutes, mins, min                                          |
| start          | start, start time, from — **recognized but NOT imported** (schedule is computed; kept so real sheets don't confuse detection) |
| tags           | tags, tag (JSON also accepts an array → joined with spaces)                      |
| speaker        | speaker, presenter                                                             |
| notes          | notes, note, comments, comment, remarks, remark                                 |
| color/colour   | hex only (#rgb/#rgba/#rrggbb/#rrgbbaa; bare 3/6-hex gets a '#')                  |
| hold           | hold, buffer, changeover (durations, like Duration)                              |
| alert1 / alert2| alert1/2, threshold1/2, alert1ms/2ms, threshold1ms/2ms                          |
| endaction      | endaction, end, onend, onzero, end action — HOLD/OVERTIME/BLANK case-insensitive |
| autocontinue   | autocontinue, continue — true/false, yes/no, y/n, 1/0, on/off                    |
| kind           | kind, type — anything containing break/changeover/host → "break", else "session" |

Skip rule: a row with **neither** label **nor** duration is skipped (blank
rows silently; mixed documents keep their good rows and the first bad row is
reported: `"importdocs: row N: duration "banana" invalid"`, with the
salvageable cues still returned so partial imports are possible).

Duration grammar (`ParseDurationMS`):
- `"1:05.5"` mm:ss(.d) — last part seconds, each earlier part ×60; 3 parts = hh:mm:ss
- `"1m5s"`, `"2h"`, `"45 min"` — mixed unit chunks; unknown unit = error
- bare number (`"65"`, `"1.5"`) = **seconds** (CuTePi rule), never minutes or Excel day-serials
- `"90000ms"` = ms; `""` = 0 no error; negatives clamp to 0

JSON value semantics: NUMBERS are always ms (PROTOCOL wire rule), STRINGS are
always human durations. snake_case and camelCase keys both accepted. Single
bare cue object tolerated. `{"cues": …}` / elements wrapped as `{"cue": {…}}`
both accepted.

## 4. Test coverage

`parse_xlsx_test.go`: header synonyms in mixed case, title rows above the
header, bad-duration rows (salvage + "row 3" error), README-first-sheet
precedence, garbage bytes, Parse() kind dispatch + unknown kind, tealeg XLS
round-trip (tealeg output re-read; real BIFF .xls → clean error), full
ExampleXLSX round-trip vs the fixture cue list.

`parse_csv_json_test.go`: ParseDurationMS table (26 cases), CSV with BOM +
quoted commas, comma/semicolon/tab sniffing, bad rows + missing header +
blank-line skipping, JSON array/wrapped/element-wrapped/cue-object forms,
snake_case, tags array, string vs number durations, endAction/timerKind/color
validation, forgiving bad elements, all Example* round-trips, adapter
Normalize/Validate/Pos-0/batch/export.

## 5. Limitations & gotchas (v1, documented honestly)

1. **Legacy XLS**: `tealeg/xlsx v3` reads OOXML (.xlsx-family), not true BIFF
   .xls. Real legacy uploads will error cleanly with a re-save hint (matches
   PLAN §8.2 "recommend users re-save as XLSX/CSV"). TimerPi's own exports
   round-trip fine through it.
2. **Excel hour- vs minute-formatted durations**: a cell typed as a duration
   can display "05:00" for h:mm — read back as 5 **seconds** by the mm:ss
   rule. Advise hh:mm:ss (or plain seconds) in the README sheet; the example
   documents model the safe forms.
3. Bare numbers are seconds by design; `"0:30"` in an Excel time cell yields
   30 s, but a serial like `0.0472` (5 min as a day fraction) is read as
   0.047 s — same advice: prefer hh:mm:ss or plain seconds.
4. Tags are a single free-text field in sheets ("VT GFX"); only JSON arrays
   get joined.
5. `Start` values are read and discarded (single source of truth is the
   computed schedule, PROTOCOL §Engine).
6. Color validates to hex only; named colors are rejected deliberately —
   they'd flow into style attributes.
7. The whole grid lives in memory (max ~sheet size); fine for day-scale cue
   lists.
