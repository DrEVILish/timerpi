package routes_test

import (
	"strings"
	"testing"
)

// STATUS U5: screens never show the room code or the control-room link,
// on any view (stage, next, clock, day sheet, layout board).
func TestScreensShowNoCode(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.Exec(`INSERT INTO cues (show_id, pos, label, duration_ms, updated_at) VALUES (?, 1, 'Keynote', 60000, 1)`, ts.showID); err != nil {
		t.Fatal(err)
	}
	p := newPersona(ts) // a stranger's TV
	for _, view := range []string{"", "?view=next", "?view=clock", "?view=daysheet", "?view=board"} {
		code, page := p.do("GET", "/d/"+ts.showCode+view, "")
		if code != 200 {
			t.Fatalf("/d/%s%s: %d", ts.showCode, view, code)
		}
		body := page
		// The code legitimately rides markup attributes (data-show, the
		// audience QR's src); it must not be shown as text.
		for _, attr := range []string{`data-show="` + ts.showCode + `"`, `/api/audience/` + ts.showCode + `/qr`} {
			body = strings.ReplaceAll(body, attr, "")
		}
		dashed := ts.showCode[:4] + "-" + ts.showCode[4:] // how screens used to print it
		visible := strings.ReplaceAll(body, ts.showCode, "")
		if visible != body || strings.Contains(body, dashed) || strings.Contains(body, "/c/"+ts.showCode) {
			t.Errorf("view %q shows the room code or control link", view)
		}
		if strings.Contains(page, "Control room") || strings.Contains(page, "tp-join-card\"") {
			t.Errorf("view %q still has the join card", view)
		}
	}
}

// STATUS U6: screens print "Room: <name>" when the event has several
// rooms, and just the name for a single-room event. The walk-in feed
// carries the same label.
func TestScreensRoomPrefix(t *testing.T) {
	ts := newAPITest(t) // one room: "API Test Show"
	p := newPersona(ts)
	_, page := p.do("GET", "/d/"+ts.showCode, "")
	if strings.Contains(page, "Room: API Test Show") || !strings.Contains(page, "API Test Show") {
		t.Errorf("single-room event: want the bare name")
	}
	ts.newRoom("Stark")
	for _, view := range []string{"", "?view=next", "?view=clock", "?view=board"} {
		_, page = p.do("GET", "/d/"+ts.showCode+view, "")
		if !strings.Contains(page, "Room: API Test Show") || !strings.Contains(page, `data-room-prefix="Room: "`) {
			t.Errorf("view %q: no \"Room: \" prefix with two rooms", view)
		}
	}
	_, feed := p.do("GET", "/api/shows/"+ts.showCode+"/walkin", "")
	if !strings.Contains(feed, `"label":"Room: Stark"`) || !strings.Contains(feed, `"label":"Room: API Test Show"`) {
		t.Errorf("walk-in feed labels: %.300s", feed)
	}
}
