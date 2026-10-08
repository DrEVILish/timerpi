package importdocs

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// BUGLOG RW29: a sparse cell far away (XFD1048576) no longer builds a
// gigantic grid: rows and columns are capped, so the parse is quick and
// the real table still imports.
func TestXLSXFarCellIsCheap(t *testing.T) {
	f := excelize.NewFile()
	for i, row := range [][]any{{"Label", "Duration"}, {"Keynote", "0:30"}, {"Panel", "0:45"}} {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.SetCellValue("Sheet1", "XFD1048576", "x"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	cues, err := ParseXLSX(buf.Bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cues) != 2 || cues[1].Label != "Panel" {
		t.Errorf("cues = %+v", cues)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("parse took %v", d)
	}
}

// A real legacy .xls (OLE2) gets a clear "re-save as .xlsx" message.
func TestLegacyXLSAsksForResave(t *testing.T) {
	_, err := ParseXLS([]byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1 rest of an old workbook"))
	if err == nil || !strings.Contains(err.Error(), "save it as .xlsx") {
		t.Errorf("err = %v, want the re-save message", err)
	}
	if _, err := Parse([]byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"), "auto"); err == nil || !strings.Contains(err.Error(), ".xlsx") {
		t.Errorf("auto-detected .xls: %v", err)
	}
}
