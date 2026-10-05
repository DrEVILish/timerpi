package importdocs

// example.go generates the downloadable example documents the Import dialog
// offers ("edit + re-import" workflow, PLAN §6.5). The three Example*
// builders share one fixture grid / expected cue list (exampleRows and
// exampleCuesFromRows) so formats stay consistent; the round-trip tests parse
// each generated document back and compare against that list.
//
// The JSON example uses exactly the PROTOCOL wire keys, so it doubles as the
// documented JSON import shape.

import (
	"bytes"
	"encoding/csv"
	"encoding/json"

	"github.com/xuri/excelize/v2"
)

// exampleColumns is the cue-sheet header shared by XLSX and CSV examples.
var exampleColumns = []string{
	"Label", "Duration", "Start", "Tags", "Speaker", "Notes",
	"Kind", "Hold", "Alert1", "Alert2", "EndAction", "AutoContinue",
}

// exampleRows is one fixture day: keynote opening, technical talk, coffee
// break, changeover, VT package, panel, lunch, closing — the shapes users
// actually schedule. Start times are informational (TimerPi computes starts
// from durations + holds); they sit in the sheet so people can eyeball the
// running order.
var exampleRows = [][]string{
	{
		"Opening keynote", "45:00", "09:00", "PRES GFX",
		"Leslie Knope", "Walk-in music, house lights down", "session",
		"", "05:00", "", "", "",
	},
	{
		"Tech outlook talk", "25:00", "09:48", "PRES CAM",
		"Ron Swanson", "Slides on the operator laptop, not the big screen", "session",
		"00:30", "02:00", "", "HOLD", "no",
	},
	{
		"Coffee break", "10:00", "10:16", "COM",
		"", "Catering in the foyer; mics muted", "break",
		"", "", "", "BLANK", "yes",
	},
	{
		"Changeover", "02:00", "10:29", "",
		"", "Reset stage for the panel", "break",
		"", "", "", "HOLD", "yes",
	},
	{
		"VT: highlights reel", "08:30", "10:31", "VT",
		"", "Hirez playback, no speaker idle check", "session",
		"", "", "", "BLANK", "yes",
	},
	{
		"Panel discussion", "30:00", "10:40", "COM CAM",
		"Panel: City Council", "4 mics, cards to cue 20:00", "session",
		"", "03:00", "", "HOLD", "no",
	},
	{
		"Lunch break", "45:00", "11:12", "",
		"", "Boxes in the loading dock", "break",
		"", "", "", "BLANK", "no",
	},
	{
		"Closing remarks & awards", "15:00", "12:00", "PRES GFX",
		"Leslie Knope", "Award row cards on stand-by", "session",
		"", "02:00", "", "OVERTIME", "no",
	},
}

// ExampleXLSX returns a ready-to-edit .xlsx workbook: a README sheet first
// (instructions + column glossary), then the "Cues" sheet with a bold,
// frozen, sorted-filterable header row and the fixture rows. The importer
// (ParseXLSX) picks the first sheet containing a real cue table, so leaving
// the README in place is safe.
func ExampleXLSX() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

	if err := f.SetSheetName("Sheet1", "README"); err != nil {
		return nil, err
	}
	readme := []string{
		"TimerPi cue list — example",
		"",
		"Columns: Label, Duration, Start, Tags, Speaker, Notes (+ optional Kind, Hold, Alert1, Alert2, EndAction, AutoContinue).",
		"Header row and column names are matched loosely — Label/Title/Name and Duration/Time/Minutes all work.",
		"",
		"Duration accepts:",
		"  45:00        (mm:ss)",
		"  00:05:00     (hh:mm:ss)",
		"  1:05.5       (fractional seconds)",
		"  1m5s         (spoken style)",
		"  90           (bares numbers are seconds; 90 = 1:30)",
		"  90000ms      (explicit milliseconds)",
		"",
		"Start is informational only — TimerPi computes starts from durations and holds.",
		"Kind: session | break. A break/changeover row is an ordinary cue with break_flag.",
		"EndAction: HOLD (freeze at 00:00), OVERTIME (count up), BLANK (blank the screen).",
		"AutoContinue: yes/no — advance to the next cue automatically at zero.",
		"Alert1/Alert2: per-cue thresholds (mm:ss) where the display changes colour.",
		"",
		"You can keep (or delete) this README sheet — the importer picks the first sheet that contains a cue table.",
	}
	for i, line := range readme {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetCellValue("README", cell, line); err != nil {
			return nil, err
		}
	}
	if err := f.SetColWidth("README", "A", "A", 110); err != nil {
		return nil, err
	}

	cuesIdx, err := f.NewSheet("Cues")
	if err != nil {
		return nil, err
	}
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
	})
	if err != nil {
		return nil, err
	}
	for c, name := range exampleColumns {
		cell, _ := excelize.CoordinatesToCellName(c+1, 1)
		if err := f.SetCellValue("Cues", cell, name); err != nil {
			return nil, err
		}
	}
	lastCol, err := excelize.CoordinatesToCellName(len(exampleColumns), 1)
	if err != nil {
		return nil, err
	}
	if err := f.SetCellStyle("Cues", "A1", lastCol, headerStyle); err != nil {
		return nil, err
	}
	for r, row := range exampleRows {
		for c, val := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			if err := f.SetCellValue("Cues", cell, val); err != nil {
				return nil, err
			}
		}
	}
	widths := []float64{30, 10, 8, 12, 20, 48, 10, 8, 8, 8, 11, 12}
	for i, w := range widths {
		col, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColWidth("Cues", col, col, w); err != nil {
			return nil, err
		}
	}
	if err := f.SetPanes("Cues", &excelize.Panes{
		Freeze:      true,
		YSplit:      1,
		TopLeftCell: "A2",
		Selection:   []excelize.Selection{{SQRef: "A2"}},
	}); err != nil {
		return nil, err
	}
	f.SetActiveSheet(cuesIdx)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExampleCSV returns the CSV example as UTF-8 WITH a BOM (Excel double-click
// opens BOM'd CSV as UTF-8; without it Excel renders mojibake). CRLF lines.
func ExampleCSV() []byte {
	var buf bytes.Buffer
	buf.Write(bomUTF8)
	w := csv.NewWriter(&buf)
	w.UseCRLF = true
	// csv.Writer.Write only fails on deferred writer errors; the ASCII-only
	// fixture cannot fail, so errors are not propagated (the API returns no
	// error by design).
	_ = w.Write(exampleColumns)
	for _, row := range exampleRows {
		_ = w.Write(row)
	}
	w.Flush()
	return buf.Bytes()
}

// ExampleJSON returns the JSON example: a bare array of cue objects using the
// exact PROTOCOL wire keys (also the accepted import shape for
// PUT /api/shows/:id/cues).
func ExampleJSON() []byte {
	cues := exampleCuesFromRows()
	out, err := json.MarshalIndent(cues, "", "  ")
	if err != nil {
		// exampleCuesFromRows only yields plain marshalable structs — this
		// cannot fail for the hardcoded fixture, but Example* is a public
		// API so never fall through with partial data.
		panic("importdocs: ExampleJSON marshal failed: " + err.Error())
	}
	return append(out, '\n')
}

// exampleCuesFromRows is the expected parse of exampleRows — single source of
// truth used by ExampleJSON and by the round-trip tests.
func exampleCuesFromRows() []Cue {
	return []Cue{
		{
			Label: "Opening keynote", DurationMS: 2700000, Kind: "session",
			Tags: "PRES GFX", Speaker: "Leslie Knope",
			Notes: "Walk-in music, house lights down", Alert1MS: 300000,
		},
		{
			Label: "Tech outlook talk", DurationMS: 1500000, Kind: "session",
			Tags: "PRES CAM", Speaker: "Ron Swanson",
			Notes:  "Slides on the operator laptop, not the big screen",
			HoldMS: 30000, Alert1MS: 120000, EndAction: "HOLD",
		},
		{
			Label: "Coffee break", DurationMS: 600000, Kind: "break",
			Tags: "COM", Notes: "Catering in the foyer; mics muted",
			EndAction: "BLANK", AutoContinue: true,
		},
		{
			Label: "Changeover", DurationMS: 120000, Kind: "break",
			Notes: "Reset stage for the panel", EndAction: "HOLD",
			AutoContinue: true,
		},
		{
			Label: "VT: highlights reel", DurationMS: 510000, Kind: "session",
			Tags: "VT", Notes: "Hirez playback, no speaker idle check",
			EndAction: "BLANK", AutoContinue: true,
		},
		{
			Label: "Panel discussion", DurationMS: 1800000, Kind: "session",
			Tags: "COM CAM", Speaker: "Panel: City Council",
			Notes: "4 mics, cards to cue 20:00", Alert1MS: 180000,
			EndAction: "HOLD",
		},
		{
			Label: "Lunch break", DurationMS: 2700000, Kind: "break",
			Notes: "Boxes in the loading dock", EndAction: "BLANK",
		},
		{
			Label: "Closing remarks & awards", DurationMS: 900000, Kind: "session",
			Tags: "PRES GFX", Speaker: "Leslie Knope",
			Notes: "Award row cards on stand-by", Alert1MS: 120000,
			EndAction: "OVERTIME",
		},
	}
}
