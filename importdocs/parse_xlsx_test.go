package importdocs

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/xuri/excelize/v2"

	"timerpi/timerpi"
)

// newXLSX builds an in-memory .xlsx workbook byte buffer: one sheet per
// (name, grid) pair.
type sheetDef struct {
	name string
	rows [][]string
}

// newXLSX builds an in-memory .xlsx workbook byte buffer from named grids.
func newXLSX(t *testing.T, sheets ...sheetDef) []byte {
	t.Helper()
	f := excelize.NewFile()
	t.Cleanup(func() { f.Close() })
	for i, sh := range sheets {
		if i == 0 {
			if err := f.SetSheetName("Sheet1", sh.name); err != nil {
				t.Fatalf("SetSheetName: %v", err)
			}
		} else if _, err := f.NewSheet(sh.name); err != nil {
			t.Fatalf("NewSheet(%q): %v", sh.name, err)
		}
		for r, row := range sh.rows {
			for c, v := range row {
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					t.Fatalf("CoordinatesToCellName: %v", err)
				}
				if err := f.SetCellValue(sh.name, cell, v); err != nil {
					t.Fatalf("SetCellValue %s: %v", cell, err)
				}
			}
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("WriteToBuffer: %v", err)
	}
	return buf.Bytes()
}

func TestParseXLSX_BasicSynonymsAndRows(t *testing.T) {
	data := newXLSX(t,
		sheetDef{name: "Sheet1", rows: [][]string{
			{"Day timer plan"}, // title above the header: ignored
			{"", ""},           // blank: ignored
			{"CUE TITLE", "TIME", "SPEAKER", "NOTES", "THRESHOLD 1", "THRESHOLD 2", "BUFFER", "END ACTION", "CONTINUE", "TYPE"},
			{"Keynote", "1:05.5", "Amy", "mics", "0:30", "0:10", "5", "OVERTIME", "YES", "Session"},
			{"", "", "", "", "", "", "", "", "", ""}, // full blank row: skipped
			{"Technical talk", "00:05:00", "Bob", "", "", "", "", "hold", "n", "Break"},
			{"Only label"}, // label without duration → cue with 0 ms
			{"Changeover strip", "02:00", "Ops", "", "", "", "00:30", "BLANK", "1", "changeover"},
		}})
	cues, err := ParseXLSX(data)
	if err != nil {
		t.Fatalf("ParseXLSX: %v", err)
	}
	want := []timerpi.Cue{
		{
			Label: "Keynote", DurationMS: 65500, Kind: "session",
			Speaker: "Amy", Notes: "mics",
			Alert1MS: 30000, Alert2MS: 10000, // BUFFER/CONTINUE columns ignored
			EndAction: "OVERTIME",
		},
		{
			Label: "Technical talk", DurationMS: 300000, Kind: "break",
			Speaker: "Bob", EndAction: "HOLD",
		},
		{Label: "Only label"},
		{
			Label: "Changeover strip", DurationMS: 120000, Kind: "break",
			Speaker: "Ops", EndAction: "BLANK",
		},
	}
	if !reflect.DeepEqual(cues, want) {
		t.Fatalf("cues mismatch\n got: %#v\nwant: %#v", cues, want)
	}
}

func TestParseXLSX_BadDurationReportsButKeepsRest(t *testing.T) {
	data := newXLSX(t,
		sheetDef{name: "S", rows: [][]string{
			{"Label", "Duration"},
			{"Good", "1m5s"},
			{"Bad", "banana"},
			{"Also good", "90"},
		}})
	cues, err := ParseXLSX(data)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("row 3")) {
		t.Fatalf("want row-3 error, got %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("want 2 salvaged cues (document parsing is forgiving), got %d: %#v", len(cues), cues)
	}
}

func TestParseXLSX_READMEFirstSheetIsSkipped(t *testing.T) {
	data := newXLSX(t,
		sheetDef{name: "README", rows: [][]string{
			{"TimerPi cue list — columns: Label, Duration, Start, Tags, Speaker, Notes"},
			{"Duration accepts 1:05.5, 1m5s, 00:05:00 or plain seconds."},
		}},
		sheetDef{name: "Cues", rows: [][]string{
			{"Label", "Duration"},
			{"Opening", "45:00"},
			{"Break", "10:00"},
		}})
	cues, err := ParseXLSX(data)
	if err != nil {
		t.Fatalf("ParseXLSX: %v", err)
	}
	if len(cues) != 2 || cues[0].Label != "Opening" {
		t.Fatalf("want the Cues sheet's rows, got %#v", cues)
	}
}

func TestParseXLSX_ExampleRoundTrip(t *testing.T) {
	x, err := ExampleXLSX()
	if err != nil {
		t.Fatalf("ExampleXLSX: %v", err)
	}
	cues, err := ParseXLSX(x)
	if err != nil {
		t.Fatalf("ParseXLSX(example): %v", err)
	}
	if want := exampleCues; !reflect.DeepEqual(cues, want) {
		t.Fatalf("example round-trip mismatch\n got: %#v\nwant: %#v", cues, want)
	}
}

func TestParseXLSX_NotAnXLSXFile(t *testing.T) {
	if _, err := ParseXLSX([]byte("this is not a zip")); err == nil {
		t.Fatal("want an error for non-xlsx bytes")
	}
	if _, err := ParseXLSX(nil); err == nil {
		t.Fatal("want an error for empty bytes")
	}
}

func TestParseKinds(t *testing.T) {
	// Parse() dispatch: xlsx sniff, explicit kinds, unknown kind error.
	data := newXLSX(t,
		sheetDef{name: "S", rows: [][]string{{"Label", "Duration"}, {"A", "65"}}})
	for _, kind := range []string{"", "auto", "xlsx", "XLSX"} {
		cues, err := Parse(data, kind)
		if err != nil || len(cues) != 1 {
			t.Fatalf("Parse(kind=%q) = %v, %v", kind, cues, err)
		}
	}
	if _, err := Parse(data, "zzz"); err == nil {
		t.Fatal("unknown kind should error")
	}
}

// TestParseXLS_BestEffort exercises the legacy path: an .xlsx that merely
// carries an .xls name is read as XLSX; true BIFF .xls uploads error with a
// re-save hint.
func TestParseXLS_BestEffort(t *testing.T) {
	data := newXLSX(t, sheetDef{name: "Cues", rows: [][]string{
		{"Label", "Duration", "Speaker"}, {"Keynote", "1m5s", "Amy"}, {"Break", "65", ""}}})
	cues, err := ParseXLS(data)
	if err != nil {
		t.Fatalf("ParseXLS: %v", err)
	}
	want := []timerpi.Cue{
		{Label: "Keynote", DurationMS: 65000, Speaker: "Amy"},
		{Label: "Break", DurationMS: 65000},
	}
	if !reflect.DeepEqual(cues, want) {
		t.Fatalf("xls round-trip mismatch\n got: %#v\nwant: %#v", cues, want)
	}
	// And a true legacy/other file must fail cleanly, not panic.
	if _, err := ParseXLS([]byte("\xd0\xcf\x11\xe0 garbage")); err == nil {
		t.Fatal("want an error for non-xls bytes")
	}
}
