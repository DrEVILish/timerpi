// Package importdocs imports and exports cue-list documents for TimerPi
// (PLAN §6.5 / PROTOCOL.md §REST). Supported formats: XLSX (first-class,
// excelize), CSV (header row required), JSON (array of cue objects, with or
// without show/cues wrapping) and legacy .xls (refused with a "re-save as .xlsx" message).
//
// Parsers return raw timerpi.Cue values in document order (ID/Pos left 0 —
// order is positional); ToTimerpiCues (adapter.go) normalizes and validates
// them for the db layer.
//
// Parsing is forgiving by design: headers are matched case/whitespace/
// punctuation-insensitively against a synonym table, BOMs are stripped,
// titles above the header row are skipped, XLSX picks the first sheet that
// yields cues, and rows that have neither a label nor a duration are skipped.
package importdocs

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"timerpi/timerpi"
)

// Parse dispatches on kind ("auto" | "xlsx" | "xls" | "csv" | "json",
// case-insensitive; empty or "auto" sniffs the bytes, which is what the REST
// import route's kind field sends). It returns parsed cues in document order
// and never empty rows.
func Parse(data []byte, kind string) ([]timerpi.Cue, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "auto":
		return parseAuto(data)
	case "xlsx":
		return ParseXLSX(data)
	case "xls":
		return ParseXLS(data)
	case "csv", "tsv":
		return ParseCSV(data)
	case "json":
		return ParseJSON(data)
	default:
		return nil, fmt.Errorf("importdocs: unknown document kind %q (want xlsx, xls, csv or json)", kind)
	}
}

// ParseFilename is Parse with the guess taken from a file extension
// (.xlsx/.xlsm/.xls/.csv/.tsv/.json), falling back to byte sniffing.
func ParseFilename(data []byte, filename string) ([]timerpi.Cue, error) {
	if kind, ok := map[string]string{
		".xlsx": "xlsx", ".xlsm": "xlsx", ".xls": "xls",
		".csv": "csv", ".tsv": "tsv", ".json": "json",
	}[strings.ToLower(path.Ext(filename))]; ok {
		return Parse(data, kind)
	}
	return parseAuto(data)
}

// parseAuto sniffs by magic bytes / first non-space byte: ZIP → XLSX, OLE2
// compound file → XLS, [ or { → JSON, otherwise CSV (most forgiving).
func parseAuto(data []byte) ([]timerpi.Cue, error) {
	head := stripBOM(data)
	switch {
	case len(head) >= 2 && head[0] == 'P' && head[1] == 'K':
		return ParseXLSX(data)
	case len(head) >= 3 && head[0] == 0xd0 && head[1] == 0xcf && head[2] == 0x11:
		return ParseXLS(data)
	default:
		for _, b := range head {
			if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
				continue
			}
			if b == '[' || b == '{' {
				return ParseJSON(data)
			}
			break
		}
		return ParseCSV(data)
	}
}

// ---------------------------------------------------------------------------
// Header detection (shared by grid formats; JSON reuses the same normalizer).

// normCell prepares a header cell (or JSON key) for matching: strips the BOM,
// trims, lowercases and drops everything that is not a letter or digit, so
// "End Action (hh:mm:ss)", "end_action" and "END-ACTION" all hit one key.
func normCell(s string) string {
	s = strings.ToLower(strings.TrimPrefix(s, "\ufeff"))
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, strings.TrimSpace(s))
}

// headerSynonyms maps a normalized header to a canonical column id.
var headerSynonyms = map[string]string{
	// Label/title/name — the row's headline text.
	"cue": "label", "cuelabel": "label", "cuetitle": "label", "label": "label",
	"title": "label", "name": "label", "item": "label",
	// Duration — accepts h:mm, h:mm:ss(.d), "1m5s", "45" (seconds), "45ms".
	"duration": "duration", "durations": "duration", "time": "duration",
	"minutes": "duration", "mins": "duration", "min": "duration",
	// Start is informational (TimerPi computes starts from durations) but
	// recognized so real-world sheets do not confuse the picker.
	"start": "start", "starttime": "start", "from": "start",
	"tags": "tags", "tag": "tags",
	"speaker": "speaker", "presenter": "speaker", "speakername": "speaker",
	"location": "location", "venue": "location", "place": "location", "where": "location",
	"notes": "notes", "note": "notes", "comment": "notes",
	"comments": "notes", "remark": "notes", "remarks": "notes",
	"color": "color", "colour": "color", "rowcolor": "color",
	"alert1": "alert1", "threshold1": "alert1",
	"alert1ms": "alert1", "threshold1ms": "alert1",
	"alert2": "alert2", "threshold2": "alert2",
	"alert2ms": "alert2", "threshold2ms": "alert2",
	"endaction": "endaction", "end": "endaction", "onend": "endaction",
	"onzero": "endaction",
	"kind":   "kind", "type": "kind",
}

// headerMap picks the canonical column ids out of one candidate row. A row
// counts as the header when at least one cell matches a synonym; unknown
// headers are ignored on purpose (people paste extra columns freely). Every
// canonical id is preset to -1 so callers never fall back to column A, and
// cell() yields "" for a negative index. nil is returned when nothing
// matches.
func headerMap(row []string) map[string]int {
	m := map[string]int{}
	found := 0
	for i, c := range row {
		if id, ok := headerSynonyms[normCell(c)]; ok {
			if _, dup := m[id]; !dup { // first match wins
				m[id] = i
				found++
			}
		}
	}
	if found == 0 {
		return nil
	}
	// Default every as-yet-unfound column id to "absent" (-1 sentinel).
	for _, id := range []string{
		"label", "duration", "start", "tags", "speaker", "notes", "location",
		"color", "alert1", "alert2", "endaction", "kind",
	} {
		if _, ok := m[id]; !ok {
			m[id] = -1
		}
	}
	return m
}

func cell(record []string, i int) string {
	if i < 0 || i >= len(record) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(record[i], "\ufeff"))
}

// errNoTable marks a grid/sheet that has no recognized header row — callers
// skip such sheets and look at the next one. Any other error is a real row
// problem and wins.
var errNoTable = errors.New("importdocs: no cue table found")

// parseGrid is the shared grid walker: find the header row ("first data row
// wins for header detection" — the first row containing at least one known
// column), then convert the rows below it into cues. errNoTable is returned
// when the header row is missing; errors from data rows are returned
// alongside the salvageable cues.
func parseGrid(rows [][]string) ([]timerpi.Cue, error) {
	var (
		cols  map[string]int
		start = -1
	)
	for i, row := range rows {
		if c := headerMap(row); c != nil {
			cols, start = c, i+1
			break
		}
	}
	if cols == nil {
		return nil, errNoTable
	}
	var (
		cues   []timerpi.Cue
		merged error
	)
	for i := start; i < len(rows); i++ {
		if isBlankRow(rows[i]) {
			continue
		}
		c, err := cueFromRecord(rows[i], cols, i) // rowIdx+1 is shown to users
		if err != nil {
			if merged == nil {
				merged = err
			}
			continue // keep collecting the rest of the document
		}
		if c.Label != "" || c.DurationMS != 0 {
			cues = append(cues, c)
		}
	}
	return cues, merged
}

func isBlankRow(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(strings.TrimPrefix(c, "\ufeff")) != "" {
			return false
		}
	}
	return true
}

// cueFromRecord builds one cue from a data row using the header map. Errors
// quote the 1-based row index for humans. A row with neither label nor
// duration yields a zero Cue, which parseGrid drops. Absent columns
// carry the -1 sentinel and read as "".
func cueFromRecord(row []string, cols map[string]int, rowIdx int) (timerpi.Cue, error) {
	rowNo := rowIdx + 1
	var c timerpi.Cue
	label := cell(row, cols["label"])
	durRaw := cell(row, cols["duration"])

	if label == "" && durRaw == "" {
		return timerpi.Cue{}, nil
	}
	if label == "" {
		return timerpi.Cue{}, fmt.Errorf("importdocs: row %d: the title is empty — every row needs a title (Label column)", rowNo)
	}
	c.Label = label

	if durRaw != "" {
		ms, err := ParseDurationMS(durRaw)
		if err != nil {
			return timerpi.Cue{}, fmt.Errorf("importdocs: row %d: %w", rowNo, err)
		}
		c.DurationMS = ms
	}

	_ = cell(row, cols["start"]) // recognized but informational; not imported
	c.Tags = cell(row, cols["tags"])
	c.Speaker = cell(row, cols["speaker"])
	c.Notes = cell(row, cols["notes"])
	c.Location = cell(row, cols["location"])

	if v := cell(row, cols["color"]); v != "" {
		colHex, err := parseColor(v)
		if err != nil {
			return timerpi.Cue{}, fmt.Errorf("importdocs: row %d: %w", rowNo, err)
		}
		c.Color = colHex
	}
	if ms, err := parseOptionalDuration(cell(row, cols["alert1"]), rowNo); err != nil {
		return timerpi.Cue{}, err
	} else {
		c.Alert1MS = ms
	}
	if ms, err := parseOptionalDuration(cell(row, cols["alert2"]), rowNo); err != nil {
		return timerpi.Cue{}, err
	} else {
		c.Alert2MS = ms
	}
	if v := cell(row, cols["kind"]); v != "" {
		c.Kind = parseKind(v)
	}
	if v := cell(row, cols["endaction"]); v != "" {
		action, err := parseEndAction(v)
		if err != nil {
			return timerpi.Cue{}, fmt.Errorf("importdocs: row %d: %w", rowNo, err)
		}
		c.EndAction = action
	}
	return c, nil
}

func parseOptionalDuration(raw string, rowNo int) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	ms, err := ParseDurationMS(raw)
	if err != nil {
		return 0, fmt.Errorf("importdocs: row %d: %w", rowNo, err)
	}
	return ms, nil
}

// parseKind folds the flexible kind vocabulary onto the domain pair
// ("session"|"break", PROTOCOL §Domain): anything mentioning break /
// changeover / host slot is break, everything else session.
func parseKind(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.Contains(v, "break"), strings.Contains(v, "changeover"),
		strings.Contains(v, "host"):
		return "break"
	default:
		return "session"
	}
}

// parseEndAction recognizes HOLD / OVERTIME / BLANK case-insensitively.
// Empty stays empty: the domain layer's Normalize defaults it to HOLD.
func parseEndAction(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return "", nil
	case "hold", "h":
		return timerpi.EndHold, nil
	case "overtime", "over", "o":
		return timerpi.EndOvertime, nil
	case "blank", "b":
		return timerpi.EndBlank, nil
	default:
		return "", fmt.Errorf("end action %q invalid (want hold, overtime or blank)", v)
	}
}

// parseColor accepts #rgb/#rgba/#rrggbb/#rrggbbaa, with or without the
// leading '#'. Empty stays empty.
func parseColor(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v != "" && !strings.HasPrefix(v, "#") {
		v = "#" + v
	}
	if !timerpi.ValidColor(v) {
		return "", fmt.Errorf("colour %q invalid (want #rgb or #rrggbb)", v)
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Duration parsing — one rule set shared by every format.

// ParseDurationMS parses operator-entered durations:
//
//	"0:45"     → h:mm = 45 min (same rule as the operator UI; "45:00" is
//	             refused with a hint — it would be 45 hours)
//	"00:05:00" → h:mm:ss = 5 min (the seconds part may be fractional)
//	"1m5s"     → mixed units; "2h", "45 min", "1.5s" also work
//	"65"       → a bare number is SECONDS (decimals allowed: "1.5" = 1.5 s)
//	"90000ms"  → explicit milliseconds
//	""         → 0, no error (an empty cell means "no value set")
//
// Negatives clamp to 0; commas and spaces inside the value are ignored.
// The ambiguity rule ("90" is seconds, never minutes or an Excel day serial)
// is documented in NOTES-importdocs.md.
func ParseDurationMS(s string) (int64, error) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
	if s == "" {
		return 0, nil
	}
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r == ',' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)

	if strings.HasPrefix(s, "-") {
		return 0, nil // a negative duration means "no duration" here
	}
	if strings.Contains(s, ":") {
		return parseClockTimeMS(s)
	}
	return parseUnitChunksMS(s)
}

// parseClockTimeMS handles "h:mm" (2 parts, the operator UI's rule:
// "0:45" is 45 minutes) and "h:mm:ss(.d)" (3 parts). A two-part value
// with hours ≥ 24 or minutes ≥ 60 is almost always a spreadsheet "mm:ss"
// ("45:00"), so it is refused with a hint instead of becoming 45 hours
// (REPORT #13).
func parseClockTimeMS(s string) (int64, error) {
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 2:
		h, herr := strconv.Atoi(parts[0])
		m, merr := strconv.Atoi(parts[1])
		if herr != nil || merr != nil || h < 0 || m < 0 {
			return 0, fmt.Errorf("duration %q invalid (want h:mm, h:mm:ss or 45m)", s)
		}
		if h >= 24 {
			hint := fmt.Sprintf("use %d:%02d for %d minutes or %dm", h/60, h%60, h, h)
			if m != 0 {
				hint = fmt.Sprintf("use 0:%02d:%02d or %dm%ds for %d min %d s", h, m, h, m, h, m)
			}
			return 0, fmt.Errorf("duration %q is %d hours (two-part times are hours:minutes) — %s", s, h, hint)
		}
		if m >= 60 {
			return 0, fmt.Errorf("duration %q invalid: minutes must be 0–59 (two-part times are hours:minutes, e.g. 0:45 = 45 minutes)", s)
		}
		return int64(h*60+m) * 60000, nil
	case 3:
	default:
		return 0, fmt.Errorf("duration %q invalid (want h:mm or h:mm:ss)", s)
	}
	var total float64
	factor := 1.0
	for i := len(parts) - 1; i >= 0; i-- {
		f, err := strconv.ParseFloat(parts[i], 64)
		if err != nil {
			return 0, fmt.Errorf("duration %q invalid (want h:mm or h:mm:ss)", s)
		}
		total += f * factor
		factor *= 60
	}
	return capMS(total*1000, s)
}

// capMS converts a parsed millisecond count, refusing anything longer than
// timerpi.MaxDurationMS (7 days), NaN or infinite. A bigger float would
// overflow the int64 conversion: MinInt64 on amd64, MaxInt64 on the Pi's
// arm64, and the schedule maths would go negative (BUGLOG RW30).
func capMS(ms float64, src string) (int64, error) {
	if !(ms <= float64(timerpi.MaxDurationMS)) { // also catches NaN
		return 0, fmt.Errorf("duration %q is longer than 7 days", src)
	}
	if ms < 0 {
		return 0, nil
	}
	return int64(ms), nil
}

// durationUnits maps a written unit to milliseconds.
var durationUnits = map[string]float64{
	"h": 3600000, "hr": 3600000, "hrs": 3600000, "hour": 3600000, "hours": 3600000,
	"m": 60000, "min": 60000, "mins": 60000, "minute": 60000, "minutes": 60000,
	"s": 1000, "sec": 1000, "secs": 1000, "second": 1000, "seconds": 1000,
	"ms": 1, "millis": 1, "millisecond": 1, "milliseconds": 1,
}

// parseUnitChunksMS parses a sequence of number+unit chunks: "1h2m3s",
// "5min", "90" (bare = seconds), "450ms", "1.5s". The whole string must be
// consumed as clean chunks — "banana" or "12zoom" are errors.
func parseUnitChunksMS(s string) (int64, error) {
	i := 0
	var ms float64
	for i < len(s) {
		j := i
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
			j++
		}
		if j == i {
			return 0, fmt.Errorf("duration %q invalid", s)
		}
		num, err := strconv.ParseFloat(s[i:j], 64)
		if err != nil {
			return 0, fmt.Errorf("duration %q invalid", s)
		}
		i = j
		k := i
		for k < len(s) && s[k] >= 'a' && s[k] <= 'z' {
			k++
		}
		unit := s[i:k]
		i = k
		mult, ok := durationUnits[unit]
		if !ok {
			if unit == "" {
				mult = 1000 // bare number = seconds
			} else {
				return 0, fmt.Errorf("duration unit %q in %q invalid", unit, s)
			}
		}
		ms += num * mult
	}
	return capMS(ms, s)
}

// ---------------------------------------------------------------------------
// XLSX (excelize).

// ParseXLSX parses an .xlsx workbook. Sheets are tried in order and the first
// sheet that yields at least one cue wins, so a README/intro sheet in front
// of the cue table is fine.
func ParseXLSX(data []byte) ([]timerpi.Cue, error) {
	// A small file can unzip to gigabytes (BUGLOG RW29): cap what excelize
	// may inflate, and read rows through the streaming iterator with a row
	// and column cap, so one cell at XFD1048576 can't build a 1.7e10-cell
	// grid on the Pi.
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		UnzipSizeLimit:    64 << 20,
		UnzipXMLSizeLimit: 16 << 20,
	})
	if err != nil {
		return nil, fmt.Errorf("importdocs: not a readable xlsx document: %w", err)
	}
	defer f.Close()
	for _, sheet := range f.GetSheetList() {
		rows, err := sheetGrid(f, sheet)
		if err != nil {
			continue
		}
		cues, perr := parseGrid(rows)
		if errors.Is(perr, errNoTable) {
			continue
		}
		if perr != nil {
			// Real row problem: surface the error but keep the salvageable
			// cues so operators can still import a partially-broken document.
			return cues, perr
		}
		if len(cues) > 0 {
			return cues, nil
		}
	}
	return nil, fmt.Errorf("importdocs: no cue rows found in the workbook (need a sheet whose first data row is a header with Label/Name and Duration/Time columns)")
}

// Spreadsheet read limits: a running order is a few hundred rows of a
// dozen columns; anything past these is ignored.
const (
	maxSheetRows = 2000
	maxSheetCols = 64
)

// sheetGrid streams one sheet into a string grid, at most maxSheetRows
// rows of maxSheetCols cells.
func sheetGrid(f *excelize.File, sheet string) ([][]string, error) {
	it, err := f.Rows(sheet)
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var grid [][]string
	for len(grid) < maxSheetRows && it.Next() {
		cols, cerr := it.Columns()
		if cerr != nil {
			return grid, cerr
		}
		if len(cols) > maxSheetCols {
			cols = cols[:maxSheetCols]
		}
		grid = append(grid, cols)
	}
	return grid, it.Error()
}

// ---------------------------------------------------------------------------
// Legacy XLS.

// ParseXLS handles a file named .xls. Real legacy Excel files (BIFF in an
// OLE2 container) can't be read: the operator is asked to re-save them as
// .xlsx or .csv (BUGLOG RW29: the old reader only understood XLSX anyway,
// so they always failed with "not a valid zip file"). An XLSX that merely
// carries an .xls name is read as XLSX.
func ParseXLS(data []byte) ([]timerpi.Cue, error) {
	if len(data) >= 2 && data[0] == 'P' && data[1] == 'K' {
		return ParseXLSX(data)
	}
	if len(data) >= 4 && bytes.Equal(data[:4], []byte{0xd0, 0xcf, 0x11, 0xe0}) {
		return nil, errLegacyXLS
	}
	return nil, fmt.Errorf("importdocs: not a readable spreadsheet — save it as .xlsx or .csv and import that")
}

var errLegacyXLS = errors.New("importdocs: this is an old Excel .xls file, which TimerPi can't read — open it in Excel or Numbers, save it as .xlsx (or .csv) and import that")

// ---------------------------------------------------------------------------
// CSV / TSV.

// ParseCSV parses comma/semicolon/tab-separated text with a REQUIRED header
// row (a title line above it is tolerated when it does not look like a
// header). The delimiter is sniffed from the first content line, so Excel's
// semicolon locales and tab exports just work. Input must be UTF-8, with or
// without BOM.
func ParseCSV(data []byte) ([]timerpi.Cue, error) {
	data = stripBOM(data)
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("importdocs: csv document is empty (a header row is required)")
	}
	rd := csv.NewReader(bytes.NewReader(data))
	rd.Comma = sniffDelimiter(data)
	rd.LazyQuotes = true
	rd.TrimLeadingSpace = true
	rd.FieldsPerRecord = -1 // ragged rows tolerated
	records, err := rd.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("importdocs: csv parse: %w", err)
	}
	cues, err := parseGrid(records)
	if errors.Is(err, errNoTable) {
		return nil, fmt.Errorf("importdocs: no cue table found in the csv — a header row with at least Label/Name and Duration/Time columns is required")
	}
	if err != nil {
		return cues, err // real row problem: keep the salvageable cues
	}
	if len(cues) == 0 {
		return nil, fmt.Errorf("importdocs: no cue rows found after the csv header row")
	}
	return cues, nil
}

// sniffDelimiter counts candidate separators on the first content line.
func sniffDelimiter(b []byte) rune {
	b = bytes.TrimSpace(stripBOM(b))
	nComma, nSemi, nTab := 0, 0, 0
	for i := 0; i < len(b) && b[i] != '\n'; i++ {
		switch b[i] {
		case ',':
			nComma++
		case ';':
			nSemi++
		case '\t':
			nTab++
		}
	}
	switch {
	case nSemi > nComma && nSemi >= nTab:
		return ';'
	case nTab > nComma && nTab > nSemi:
		return '\t'
	default:
		return ','
	}
}

// ---------------------------------------------------------------------------
// JSON.

// ParseJSON parses either a bare array of cue objects, or an object with a
// "cues" array (show/cues wrapping accepted); each element may also nest the
// cue under a "cue" key. Field names are matched case/punctuation-
// insensitively, accepting both PROTOCOL camelCase ("durationMS") and
// friendly snake_case ("duration_ms").
//
// Value semantics: NUMBERS are milliseconds (the wired PROTOCOL format);
// STRINGS are always human durations ("1m5s", "90", "00:05:00"). The alert
// thresholds follow the same rule.
func ParseJSON(data []byte) ([]timerpi.Cue, error) {
	data = stripBOM(data)
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("importdocs: invalid JSON: %w", err)
	}
	switch v := raw.(type) {
	case []any:
		return cuesFromJSONList(v)
	case map[string]any:
		if list, ok := v["cues"]; ok {
			elems, ok := list.([]any)
			if !ok {
				return nil, fmt.Errorf(`importdocs: JSON "cues" must be an array of cue objects`)
			}
			return cuesFromJSONList(elems)
		}
		// tolerate a bare single cue object
		c, err := cueFromJSONMap(rekey(v), 1)
		if err != nil {
			return nil, fmt.Errorf(`importdocs: JSON document must be an array of cues or an object with a "cues" array: %w`, err)
		}
		return []timerpi.Cue{c}, nil
	default:
		return nil, fmt.Errorf("importdocs: JSON document must be an array of cues or an object with a \"cues\" array")
	}
}

func cuesFromJSONList(list []any) ([]timerpi.Cue, error) {
	var (
		cues   []timerpi.Cue
		merged error
	)
	for i, elem := range list {
		m, ok := elem.(map[string]any)
		if !ok {
			if merged == nil {
				merged = fmt.Errorf("importdocs: cue %d: not an object", i+1)
			}
			continue
		}
		if inner, ok := m["cue"].(map[string]any); ok { // {"cue": {...}} wrap
			m = inner
		}
		c, err := cueFromJSONMap(rekey(m), i+1)
		if err != nil {
			if merged == nil {
				merged = err
			}
			continue
		}
		cues = append(cues, c)
	}
	return cues, merged
}

// rekey rekeys a decoded object with normCell so lookups are tolerant.
func rekey(m map[string]any) map[string]any {
	nm := make(map[string]any, len(m))
	for k, v := range m {
		nm[normCell(k)] = v
	}
	return nm
}

func cueFromJSONMap(nm map[string]any, rowNo int) (timerpi.Cue, error) {
	var c timerpi.Cue
	if err := pickJSONStr(nm, &c.Label, "label", "title", "name"); err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	ms, err := pickJSONDur(nm, "durationms", "duration", "time")
	if err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	c.DurationMS = ms
	// tags: string, or an array of strings joined with spaces.
	if v, ok := nm["tags"]; ok && v != nil {
		switch t := v.(type) {
		case string:
			c.Tags = strings.TrimSpace(t)
		case []any:
			var words []string
			for _, e := range t {
				if s, ok := e.(string); ok {
					words = append(words, s)
				}
			}
			c.Tags = strings.TrimSpace(strings.Join(words, " "))
		default:
			return timerpi.Cue{}, wrapJSON(rowNo, fmt.Errorf(`field "tags" must be a string or an array of strings`))
		}
	} else if err := pickJSONStr(nm, &c.Tags, "tag"); err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	if err := pickJSONStr(nm, &c.Speaker, "speaker", "presenter"); err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	if err := pickJSONStr(nm, &c.Notes, "notes", "note", "comment", "comments"); err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	ms, err = pickJSONDur(nm, "alert1ms", "alert1", "threshold1")
	if err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	c.Alert1MS = ms
	ms, err = pickJSONDur(nm, "alert2ms", "alert2", "threshold2")
	if err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	c.Alert2MS = ms
	if err := pickJSONStr(nm, &c.TimerKind, "timerkind"); err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	c.TimerKind = strings.ToUpper(strings.TrimSpace(c.TimerKind))
	switch c.TimerKind {
	case "", "COUNTDOWN", "COUNTSTOP", "CLOCK":
	default:
		return timerpi.Cue{}, fmt.Errorf("importdocs: cue %d: timerKind %q invalid", rowNo, c.TimerKind)
	}
	for canon, dst := range map[string]*string{
		"alertcolor1": &c.AlertColor1,
		"alertcolor2": &c.AlertColor2,
		"color":       &c.Color,
	} {
		if err := pickJSONColor(nm, dst, canon); err != nil {
			return timerpi.Cue{}, wrapJSON(rowNo, err)
		}
	}
	if err := pickJSONStr(nm, &c.EndAction, "endaction", "end", "onend", "onzero"); err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	action, aerr := parseEndAction(c.EndAction)
	if aerr != nil {
		return timerpi.Cue{}, fmt.Errorf("importdocs: cue %d: %w", rowNo, aerr)
	}
	c.EndAction = action
	if err := pickJSONStr(nm, &c.Kind, "kind", "type"); err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	if c.Kind != "" {
		c.Kind = parseKind(c.Kind)
	}
	if err := pickJSONStr(nm, &c.Location, "location"); err != nil {
		return timerpi.Cue{}, wrapJSON(rowNo, err)
	}
	return c, nil
}

func wrapJSON(rowNo int, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("importdocs: cue %d: %w", rowNo, err)
}

// pickJSONStr copies the first present string field into *dst.
func pickJSONStr(nm map[string]any, dst *string, keys ...string) error {
	for _, k := range keys {
		v, ok := nm[k]
		if !ok || v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("field %q must be a string", k)
		}
		*dst = strings.TrimSpace(s)
		return nil
	}
	return nil
}

// pickJSONDur: the first present duration key; numbers are milliseconds, but
// zero stays zero yet still counts as "set" (0 ms is a legal wire value); a
// string is always a human duration.
func pickJSONDur(nm map[string]any, keys ...string) (int64, error) {
	for _, k := range keys {
		v, ok := nm[k]
		if !ok || v == nil {
			continue
		}
		return jsonToMS(v)
	}
	return 0, nil
}

// jsonToMS converts one decoded JSON value to milliseconds.
func jsonToMS(v any) (int64, error) {
	switch n := v.(type) {
	case float64:
		return capMS(n, fmt.Sprint(n)) // wire value; JSON numbers are always ms
	case int:
		return capMS(float64(n), fmt.Sprint(n))
	case int64:
		return capMS(float64(n), fmt.Sprint(n))
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, fmt.Errorf("duration %q invalid", string(n))
		}
		return capMS(f, string(n))
	case string:
		return ParseDurationMS(n) // strings are always human durations
	default:
		return 0, fmt.Errorf("duration value %v is neither a number nor a string", v)
	}
}

// pickJSONColor validates and copies a colour field (CSS hex colour).
func pickJSONColor(nm map[string]any, dst *string, key string) error {
	v, ok := nm[key]
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("field %q must be a string colour", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	col, err := parseColor(s)
	if err != nil {
		return err
	}
	*dst = col
	return nil
}

// ---------------------------------------------------------------------------
// Shared byte helpers.

var bomUTF8 = []byte{0xEF, 0xBB, 0xBF}

func stripBOM(b []byte) []byte {
	if bytes.HasPrefix(b, bomUTF8) {
		return b[len(bomUTF8):]
	}
	return b
}
