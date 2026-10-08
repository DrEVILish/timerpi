// package routes — import.go: cue-list import + example documents via
// package importdocs (NOTES-importdocs.md §2 recipe).
//
// Operator-visibility note: the dashboard form targets #import-result with
// innerHTML and timerpi.js has no htmx:responseError handler yet, so parse
// errors answer 200 + an alert-error FRAGMENT (verbatim document error,
// html-escaped) — they land in the small feedback box instead of replacing
// any panel (PROTOCOL "errors never replace panels"; CONTRACT-UI §3 "never
// an empty error page"). Hard client faults (no file) stay 4xx.
package routes

import (
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"timerpi/importdocs"
	"timerpi/timerpi"
)

// importCues applies a parsed cue list to the show, all or nothing
// (replace = ReplaceCues, append = AppendCues; one transaction each).
func (d *Deps) importCues(showID int64, cues []timerpi.Cue, mode string) error {
	if mode == "append" {
		return d.Store.AppendCues(showID, cues) // one transaction (RS30)
	}
	return d.Store.ReplaceCues(showID, cues)
}

// POST /api/shows/:id/import — multipart per PROTOCOL/CONTRACT-UI:
//
//	file (required), kind=auto|xlsx|xls|csv|json (default auto =
//	byte-sniffing), mode=replace|append.
//
// CONTRACT-UI §3: the form can't carry ?mode=, so the mode FIELD is read
// from the body, with the query string as the API fallback. Partial-reply
// salvage per scope: importdocs may return cues + an error (mixed
// documents) — the good rows are applied AND the error text surfaces.
func (d *Deps) apiImport(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	fh, ferr := c.FormFile("file")
	if ferr != nil {
		msg := `No file in the upload (field "file").`
		if uploadTooBig(ferr) {
			msg = uploadTooBigMsg + "."
		}
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8",
			[]byte(`<div class="alert alert-error">`+html.EscapeString(msg)+`</div>`))
		return
	}
	kind := c.PostForm("kind") // "" = auto = sniff
	mode := c.DefaultPostForm("mode", c.DefaultQuery("mode", "replace"))
	if mode != "append" {
		mode = "replace" // safe default: full re-import is the main flow
	}

	f, oerr := fh.Open()
	if oerr != nil {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8",
			[]byte(`<div class="alert alert-error">Upload unreadable.</div>`))
		return
	}
	defer f.Close()
	data, rerr := io.ReadAll(f)
	if rerr != nil {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8",
			[]byte(`<div class="alert alert-error">Upload unreadable.</div>`))
		return
	}

	// An image, PDF or archive is not a running order: say so instead of
	// "no cue table found in the csv".
	if mt := http.DetectContentType(data); strings.HasPrefix(mt, "image/") || strings.HasPrefix(mt, "audio/") ||
		strings.HasPrefix(mt, "video/") || mt == "application/pdf" || mt == "application/x-gzip" {
		d.importFragmentErr(c, errors.New("that file is not a running order — import a .csv, .xlsx or .json file (download an example to start from)"))
		return
	}

	// Parse: explicit kind wins, else the filename extension (+sniff).
	cues, perr := importdocs.ParseFilename(data, fh.Filename)
	if kind != "" && kind != "auto" {
		cues, perr = importdocs.Parse(data, kind)
	}
	if len(cues) == 0 && perr == nil {
		perr = errors.New("importdocs: empty document — nothing importable")
	}

	// Document cues → validated domain cues (""-kind rows normalized).
	var tcs []timerpi.Cue
	var terr error
	if len(cues) > 0 {
		tcs, terr = importdocs.ToTimerpiCues(cues)
	}
	if perr == nil {
		perr = terr
	}

	// Partial-import salvage: apply whatever converted, then report.
	if len(tcs) > 0 {
		// Replace still needs ALL rows valid (single tx); a salvage apply
		// of only the good rows under replace would silently drop rows the
		// operator believes were imported. So: errors + rows together are
		// refused under replace, applied under append (additives are safe).
		if perr != nil && mode == "replace" {
			d.importFragmentErr(c, fmt.Errorf("%w. Nothing was imported: fix that row and import again", perr))
			return
		}
		if aerr := d.importCues(id, tcs, mode); aerr != nil {
			d.importFragmentErr(c, aerr)
			return
		}
		d.logAction(id, "import", fmt.Sprintf("%s %d cues", mode, len(tcs)))
		d.notifyShow(id)
	}
	if perr != nil {
		// Verbatim (row numbers quoted by importdocs), html-escaped.
		d.importFragmentErr(c, perr)
		return
	}
	if len(tcs) == 0 {
		d.importFragmentErr(c, errors.New("importdocs: no cues found in the document"))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8",
		[]byte(`<div class="alert alert-success">Imported `+strconv.Itoa(len(tcs))+
			` cues (`+html.EscapeString(mode)+`).</div>`))
}

// importFragmentErr answers the import target with the error text rendered
// into the feedback box (status 200 so htmx swaps it in; no panel replaced).
func (d *Deps) importFragmentErr(c *gin.Context, err error) {
	c.Data(http.StatusOK, "text/html; charset=utf-8",
		[]byte(`<div class="alert alert-error">`+html.EscapeString(friendlyImportErr(err))+`</div>`))
}

// friendlyImportErr drops the package prefix operators shouldn't see
// ("importdocs: row 3: …" → "Row 3: …").
func friendlyImportErr(err error) string {
	msg := strings.ReplaceAll(err.Error(), "importdocs: ", "")
	msg = strings.ReplaceAll(msg, "timerpi: ", "")
	if r, size := utf8.DecodeRuneInString(msg); size > 0 {
		msg = string(unicode.ToUpper(r)) + msg[size:]
	}
	return msg
}

// GET /api/import-example?fmt=xlsx|csv|json AND
// GET /api/shows/:id/import-example?fmt=… — both per CONTRACT-UI §3 /
// importdocs NOTES §2; the show variant exists for PROTOCOL REST parity
// (examples are static documents).
func serveExample(c *gin.Context) {
	var (
		blob  []byte
		name  string
		ctype string
	)
	switch c.Query("fmt") {
	case "xlsx":
		b, err := importdocs.ExampleXLSX() // error attaches here only to fail loudly
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		blob, name, ctype = b, "timerpi-cue-list-example.xlsx",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "csv":
		blob, name, ctype = importdocs.ExampleCSV(), "timerpi-cue-list-example.csv",
			"text/csv; charset=utf-8" // BOM+CRLF already inside the bytes
	case "json":
		blob, name, ctype = importdocs.ExampleJSON(), "timerpi-cue-list-example.json",
			"application/json; charset=utf-8"
	default:
		c.String(http.StatusBadRequest, "fmt must be xlsx|csv|json")
		return
	}
	c.Header("Content-Disposition", "attachment; filename=\""+name+"\"")
	c.Data(http.StatusOK, ctype, blob)
}
