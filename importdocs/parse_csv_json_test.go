package importdocs

import (
	"reflect"
	"strings"
	"testing"

	"timerpi/timerpi"
)

// ---------------------------------------------------------------------------
// ParseDurationMS — the one duration rule set shared by every format.

func TestParseDurationMS(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		werr bool
	}{
		// Bare numbers are always SECONDS.
		{"65", 65000, false},
		{"90", 90000, false},
		{"1.5", 1500, false},
		{" 1,200 ", 1200000, false}, // thousands separators ignored
		// Clock forms (last part is seconds, each part before is 60×).
		{"1:05.5", 65500, false},
		{"1:05", 65000, false},
		{"0:30", 30000, false},
		{"00:05:00", 300000, false},
		{"01:00:00.25", 3600250, false},
		// Written units / mixed chunks.
		{"1m5s", 65000, false},
		{"1m 5 s", 65000, false},
		{"2h", 7200000, false},
		{"45 min", 2700000, false},
		{"1.5s", 1500, false},
		{"90000ms", 90000, false},
		{"5 seconds", 5000, false},
		{"2m30s", 150000, false},
		// Empty / no-value / negative.
		{"", 0, false},
		{"   ", 0, false},
		{"-5", 0, false},
		// Errors.
		{"banana", 0, true},
		{"12zoom", 0, true},
		{":30", 0, true},
		{"1:05:00:00", 0, true},
		{"1:05:00 pm", 0, true},
	}
	for _, c := range cases {
		got, err := ParseDurationMS(c.in)
		if c.werr {
			if err == nil {
				t.Errorf("ParseDurationMS(%q) = %d, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseDurationMS(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseDurationMS(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// CSV.

func TestParseCSV_Basic(t *testing.T) {
	csvData := "\xEF\xBB\xBF" + "Label,Duration,Speaker,Notes,Alert1,Alert2,Hold,EndAction,AutoContinue,Kind\n" +
		"\"Opening, keynote\",\"1:05.5\",\"Amy\",\"says, hi\",\"0:30\",\"0:10\",\"5\",OVERTIME,YES,session\n" +
		"Break,\"65\",,,,,,,yes,\n" +
		"VT,\"1m5s\",,,,,,blank,n,changeover\n"
	cues, err := ParseCSV([]byte(csvData))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	want := []Cue{
		{
			Label: "Opening, keynote", DurationMS: 65500, Speaker: "Amy",
			Notes: "says, hi", Alert1MS: 30000, Alert2MS: 10000, HoldMS: 5000,
			EndAction: "OVERTIME", AutoContinue: true, Kind: "session",
		},
		{Label: "Break", DurationMS: 65000, AutoContinue: true},
		{Label: "VT", DurationMS: 65000, EndAction: "BLANK", Kind: "break"},
	}
	if !reflect.DeepEqual(cues, want) {
		t.Fatalf("csv mismatch\n got: %#v\nwant: %#v", cues, want)
	}
}

func TestParseCSV_DelimiterSniffing(t *testing.T) {
	semi := "Cue Title;Time;Speaker\nKeynote;00:05:00;Amy\n"
	cues, err := ParseCSV([]byte(semi))
	if err != nil {
		t.Fatalf("semicolon csv: %v", err)
	}
	if len(cues) != 1 || cues[0].Label != "Keynote" || cues[0].DurationMS != 300000 || cues[0].Speaker != "Amy" {
		t.Fatalf("semicolon sniff failed: %#v", cues)
	}
	tab := "Label\tDuration\tSpeaker\nKeynote\t90\tBob\n"
	cues, err = ParseCSV([]byte(tab))
	if err != nil {
		t.Fatalf("tab csv: %v", err)
	}
	if len(cues) != 1 || cues[0].DurationMS != 90000 || cues[0].Speaker != "Bob" {
		t.Fatalf("tab sniff failed: %#v", cues)
	}
}

func TestParseCSV_ErrorsAndForgivingRows(t *testing.T) {
	if _, err := ParseCSV(nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty error, got %v", err)
	}
	if _, err := ParseCSV([]byte("no header here\njust noise\n")); err == nil {
		t.Fatal("want missing-header error")
	}
	// A bad duration still yields salvageable cues + a row-numbered error.
	out, err := ParseCSV([]byte("Label,Duration\nGood,1m5s\nBad,banana\nGood2,90\n"))
	if err == nil || !strings.Contains(err.Error(), "row 3") {
		t.Fatalf("want row-3 error, got %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 salvageable cues, got %#v", out)
	}
	// Blank lines between cues are skipped silently.
	cues, err := ParseCSV([]byte("Label,Duration\nGood,1m5s\n\n\nMore,65\n"))
	if err != nil || len(cues) != 2 {
		t.Fatalf("blank rows should be skipped: %v %#v", err, cues)
	}
}

// TestParseCSV_ExampleRoundTrip pins ExampleCSV to its fixture meaning.
func TestParseCSV_ExampleRoundTrip(t *testing.T) {
	cues, err := ParseCSV(ExampleCSV())
	if err != nil {
		t.Fatalf("ParseCSV(example): %v", err)
	}
	if want := exampleCuesFromRows(); !reflect.DeepEqual(cues, want) {
		t.Fatalf("csv example round-trip mismatch\n got: %#v\nwant: %#v", cues, want)
	}
}

// ---------------------------------------------------------------------------
// JSON.

func TestParseJSON_ArrayAndWrapping(t *testing.T) {
	arr := `[
	  {"label": "Keynote", "durationMS": 600000, "tags": ["VT", "GFX"],
	   "speaker": "Amy", "timerKind": "countdown", "endAction": "hold",
	   "autoContinue": true, "alert1MS": "01:30"},
	  {"label": "VT", "duration_ms": "1m5s", "kind": "changeover",
	   "holdMS": "0:10", "alert_2_ms": 90}
	]`
	cues, err := ParseJSON([]byte(arr))
	if err != nil {
		t.Fatalf("ParseJSON(array): %v", err)
	}
	want := []Cue{
		{
			Label: "Keynote", DurationMS: 600000, Tags: "VT GFX", Speaker: "Amy",
			TimerKind: "COUNTDOWN", EndAction: "HOLD", AutoContinue: true,
			Alert1MS: 90000,
		},
		{
			Label: "VT", DurationMS: 65000, Kind: "break",
			HoldMS: 10000, Alert2MS: 90, // numeric values are ALWAYS ms (wire rule)
		},
	}
	if !reflect.DeepEqual(cues, want) {
		t.Fatalf("json array mismatch\n got: %#v\nwant: %#v", cues, want)
	}

	// show/cues wrapping, and per-element "cue" wrapping.
	for _, doc := range []string{
		`{"show": {"title": "X"}, "cues": [{"label": "A", "durationMS": 60000}]}`,
		`{"cues": [{"cue": {"label": "A", "durationMS": 60000}}]}`,
	} {
		cues, err = ParseJSON([]byte(doc))
		if err != nil {
			t.Fatalf("ParseJSON(%s): %v", doc, err)
		}
		if len(cues) != 1 || cues[0].Label != "A" || cues[0].DurationMS != 60000 {
			t.Fatalf("wrap parse failed: %#v", cues)
		}
	}

	// A bare single cue object is tolerated.
	cues, err = ParseJSON([]byte(`{"title": "Solo", "durationMS": 1000}`))
	if err != nil || len(cues) != 1 || cues[0].Label != "Solo" {
		t.Fatalf("single-object parse failed: %v %#v", err, cues)
	}

	// Title/Name synonyms.
	cues, err = ParseJSON([]byte(`[{"name": "Solo", "time": "90"}]`))
	if err != nil || len(cues) != 1 || cues[0].Label != "Solo" || cues[0].DurationMS != 90000 {
		t.Fatalf("name/time synonym parse failed: %v %#v", err, cues)
	}
}

func TestParseJSON_Errors(t *testing.T) {
	cases := map[string]string{
		"bad json":       "]not json[",
		"truncated":      `[{`,
		"bad durationMS": `[{"label": "X", "durationMS": {"no": true}}]`,
		"bad timerKind":  `[{"label": "X", "durationMS": 60000, "timerKind": "telepathy"}]`,
		"bad endAction":  `[{"label": "X", "durationMS": 60000, "endAction": "explode"}]`,
		"bad color":      `[{"label": "X", "durationMS": 60000, "color": "hotpink"}]`,
		"cues not array": `{"cues": {"label": "X"}}`,
		"not objects":    `["one", "two"]`,
		"empty":          ``,
	}
	for name, doc := range cases {
		if _, err := ParseJSON([]byte(doc)); err == nil {
			t.Errorf("ParseJSON(%s): want an error", name)
		}
	}

	// Forgiving rows: a bad cue object errors but keeps the good cues.
	cues, err := ParseJSON([]byte(`[
		{"label": "Good", "durationMS": 60000},
		{"label": "Bad", "durationMS": []},
		{"label": "Good2", "durationMS": 65000}
	]`))
	if err == nil || !strings.Contains(err.Error(), "cue 2") {
		t.Fatalf("want cue-2 error, got %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("want 2 salvageable cues, got %#v", cues)
	}
}

// TestParseJSON_ExampleRoundTrip pins ExampleJSON to its fixture meaning.
func TestParseJSON_ExampleRoundTrip(t *testing.T) {
	cues, err := ParseJSON(ExampleJSON())
	if err != nil {
		t.Fatalf("ParseJSON(example): %v", err)
	}
	if want := exampleCuesFromRows(); !reflect.DeepEqual(cues, want) {
		t.Fatalf("json example round-trip mismatch\n got: %#v\nwant: %#v", cues, want)
	}
}

// JSON numbers are milliseconds by the wire rule; strings are human durations.
func TestParseJSON_NumberVsStringDurations(t *testing.T) {
	cues, err := ParseJSON([]byte(`[
		{"label": "Wire style", "duration": 60000},
		{"label": "Human style", "duration": "1:05"}
	]`))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if cues[0].DurationMS != 60000 || cues[1].DurationMS != 65000 {
		t.Fatalf("number/string semantics broken: %#v", cues)
	}
}

// ---------------------------------------------------------------------------
// Parse() / ParseFilename() sniffing.

func TestParseAutoAndFilename(t *testing.T) {
	x, err := ExampleXLSX()
	if err != nil {
		t.Fatalf("ExampleXLSX: %v", err)
	}
	csvData := ExampleCSV()
	jsonData := ExampleJSON()
	n := len(exampleCuesFromRows())

	for _, kind := range []string{"", "auto", "AUTO"} {
		for _, pair := range []struct {
			data []byte
			name string
		}{{x, "xlsx bytes (PK magic)"}, {csvData, "BOM'd csv"}, {jsonData, "json array"}} {
			cues, err := Parse(pair.data, kind)
			if err != nil {
				t.Fatalf("Parse(kind=%q, %s): %v", kind, pair.name, err)
			}
			if len(cues) != n {
				t.Fatalf("Parse(kind=%q) sniffed %s wrong: %d cues", kind, pair.name, len(cues))
			}
		}
	}
	cues, err := Parse(csvData, "CSV") // explicit kind, case-insensitive
	if err != nil || len(cues) != n {
		t.Fatalf("Parse(csv): %v %d", err, len(cues))
	}
	// Filename extension steers the parser (here: redundant with sniffing).
	cues, err = ParseFilename(csvData, "sheet.csv")
	if err != nil || len(cues) != n {
		t.Fatalf("ParseFilename(.csv): %v %d", err, len(cues))
	}
	cues, err = ParseFilename(jsonData, "import.json")
	if err != nil || len(cues) != n {
		t.Fatalf("ParseFilename(.json): %v %d", err, len(cues))
	}
	// A mismatched extension forces one parser on the wrong bytes → a clean
	// parse error, not silent nonsense.
	if _, err = ParseFilename(jsonData, "sheet.csv"); err == nil {
		t.Fatal("json bytes forced through the csv parser must error")
	}
}

// ---------------------------------------------------------------------------
// Adapter (importdocs.Cue ↔ timerpi.Cue).

func TestAdapter(t *testing.T) {
	raw := []byte(`[
		{"label": "Talk", "durationMS": 60000, "kind": "break", "alert1MS": 30000,
		 "alert2MS": 5000, "color": "#ff0000", "autoContinue": true}
	]`)
	cues, err := ParseJSON(raw)
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	tc, err := cues[0].ToTimerpiCue()
	if err != nil {
		t.Fatalf("ToTimerpiCue: %v", err)
	}
	if tc.Label != "Talk" || tc.DurationMS != 60000 || tc.Kind != "break" ||
		tc.Alert1MS != 30000 || tc.Alert2MS != 5000 || tc.Color != "#ff0000" ||
		tc.AutoContinue { // auto-continue is gone (STATUS U42): imports drop it
		t.Fatalf("adapter field mismatch: %#v", tc)
	}
	// Domain Normalize fills defaults for empty fields.
	if tc.TimerKind != "COUNTDOWN" {
		t.Errorf("want normalized TimerKind COUNTDOWN, got %q", tc.TimerKind)
	}
	if tc.AlertColor1 == "" || tc.AlertColor2 == "" {
		t.Errorf("want normalized alert colours, got %q %q", tc.AlertColor1, tc.AlertColor2)
	}
	if tc.Pos != 0 {
		t.Errorf("adapter leaves Pos to the db layer, got %d", tc.Pos)
	}
	// Batch: order kept, no Pos.
	got, err := ToTimerpiCues(cues)
	if err != nil || len(got) != len(cues) || got[0] != tc {
		t.Fatalf("ToTimerpiCues: %v %#v", err, got)
	}
	// And back — lossless but domain-normalized (TimerKind/colours/endAction
	// defaults are filled by timerpi.Cue.Normalize, that is by design).
	round := FromTimerpiCue(tc)
	wantRound := Cue{
		Label: "Talk", DurationMS: 60000, Kind: "break", TimerKind: "COUNTDOWN",
		Alert1MS: 30000, Alert2MS: 5000, AlertColor1: "#ffaa00",
		AlertColor2: "#ff4444", EndAction: "HOLD",
		Color: "#ff0000",
	}
	if !reflect.DeepEqual(round, wantRound) {
		t.Fatalf("FromTimerpiCue mismatch\n got: %#v\nwant: %#v", round, wantRound)
	}
	// Batch back.
	all := FromTimerpiCues([]timerpi.Cue{tc})
	if !reflect.DeepEqual(all, []Cue{wantRound}) {
		t.Fatalf("FromTimerpiCues mismatch: %#v", all)
	}
	// And a whole example document converts without complaint.
	cues, err = ParseJSON(ExampleJSON())
	if err != nil {
		t.Fatalf("ParseJSON(example): %v", err)
	}
	docs, err := ToTimerpiCues(cues)
	if err != nil || len(docs) != len(cues) {
		t.Fatalf("ToTimerpiCues(example): %v", err)
	}
}
