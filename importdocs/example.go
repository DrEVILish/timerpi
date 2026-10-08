package importdocs

// example.go generates the downloadable example documents the Import dialog
// offers ("edit + re-import" workflow, PLAN §6.5). The three Example*
// builders share one fixture grid (exampleRows) so formats stay consistent;
// the JSON example is the CSV example parsed back. The round-trip tests parse
// each generated document and compare against one expected cue list.
//
// The JSON example is the PROTOCOL wire cue object, so it doubles as the
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
	"Kind", "Alert1", "Alert2", "EndAction",
}

// exampleRows is one fixture day: keynote opening, technical talk, coffee
// break, changeover, VT package, panel, lunch, closing — the shapes users
// actually schedule. Start times are informational (TimerPi computes starts
// from durations); they sit in the sheet so people can eyeball the
// running order.
var exampleRows = [][]string{
	{
		"Opening keynote", "0:45", "09:00", "PRES GFX",
		"Leslie Knope", "Walk-in music, house lights down", "session",
		"0:05", "", "",
	},
	{
		"Tech outlook talk", "0:25", "09:48", "PRES CAM",
		"Ron Swanson", "Slides on the operator laptop, not the big screen", "session",
		"0:02", "", "HOLD",
	},
	{
		"Coffee break", "0:10", "10:16", "COM",
		"", "Catering in the foyer; mics muted", "break",
		"", "", "BLANK",
	},
	{
		"Changeover", "0:02", "10:29", "",
		"", "Reset stage for the panel", "break",
		"", "", "HOLD",
	},
	{
		"VT: highlights reel", "0:08:30", "10:31", "VT",
		"", "Hirez playback, no speaker idle check", "session",
		"", "", "BLANK",
	},
	{
		"Panel discussion", "0:30", "10:40", "COM CAM",
		"Panel: City Council", "4 mics, cards to cue 20:00", "session",
		"0:03", "", "HOLD",
	},
	{
		"Lunch break", "0:45", "11:12", "",
		"", "Boxes in the loading dock", "break",
		"", "", "BLANK",
	},
	{
		"Closing remarks & awards", "0:15", "12:00", "PRES GFX",
		"Leslie Knope", "Award row cards on stand-by", "session",
		"0:02", "", "OVERTIME",
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
		"Columns: Label, Duration, Start, Tags, Speaker, Notes (+ optional Kind, Alert1, Alert2, EndAction).",
		"Header row and column names are matched loosely — Label/Title/Name and Duration/Time/Minutes all work.",
		"",
		"Duration accepts:",
		"  0:45         (h:mm = 45 minutes; 45:00 would be 45 hours and is refused)",
		"  0:08:30      (h:mm:ss = 8 min 30 s)",
		"  45m, 1h30m   (spoken style)",
		"  1m5s         (spoken style)",
		"  90           (bares numbers are seconds; 90 = 1:30)",
		"  90000ms      (explicit milliseconds)",
		"",
		"Start is informational only — TimerPi computes starts from durations.",
		"Kind: session | break. A break/changeover row is an ordinary cue with break_flag.",
		"EndAction: HOLD (freeze at 00:00), OVERTIME (count up), BLANK (blank the screen).",
		"Alert1/Alert2: per-cue thresholds (h:mm or 2m30s) where the display changes colour.",
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
	widths := []float64{30, 10, 8, 12, 20, 48, 10, 8, 8, 11}
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

// ExampleJSON returns the JSON example: a bare array of wire cue objects
// (also the accepted import shape for PUT /api/shows/:id/cues) — the CSV
// example parsed back, so the two cannot drift.
func ExampleJSON() []byte {
	cues, err := ParseCSV(ExampleCSV())
	if err == nil {
		var out []byte
		if out, err = json.MarshalIndent(cues, "", "  "); err == nil {
			return append(out, '\n')
		}
	}
	// The hardcoded fixture cannot fail; Example* is a public API, so never
	// fall through with partial data.
	panic("importdocs: ExampleJSON: " + err.Error())
}
