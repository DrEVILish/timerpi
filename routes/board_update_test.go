package routes_test

import (
	"fmt"
	"testing"

	"timerpi/boards"
)

func TestFailedBoardUpdateKeepsNameAndLayout(t *testing.T) {
	for _, failure := range []string{"invalid layout", "storage failure"} {
		t.Run(failure, func(t *testing.T) {
			ts := newAPITest(t)
			before, err := boards.CreateBoard(ts.db.DB, ts.showID, "Keep me", "")
			if err != nil {
				t.Fatal(err)
			}
			layout := `{"v":1,"widgets":[{"id":"bad","type":"unknown","x":0,"y":0,"w":1,"h":1}]}`
			if failure == "storage failure" {
				layout = before.Layout
				if _, err := ts.db.Exec(`CREATE TRIGGER fail_layout BEFORE UPDATE OF layout_json ON display_boards
					BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
					t.Fatal(err)
				}
			}
			body := []byte(fmt.Sprintf(`{"name":"Changed","layout":%s}`, layout))
			code, response := ts.call("PUT", fmt.Sprintf("/api/shows/%s/boards/%d", ts.showCode, before.ID), body, "application/json")
			if code != 400 {
				t.Fatalf("update should fail: %d %s", code, response)
			}
			after, err := boards.GetBoard(ts.db.DB, ts.showID, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Name != before.Name || after.Layout != before.Layout || after.UpdatedAt != before.UpdatedAt {
				t.Errorf("failed update changed the layout: before %+v, after %+v", before, after)
			}
			if failure == "storage failure" {
				if _, err := ts.db.Exec(`DROP TRIGGER fail_layout`); err != nil {
					t.Fatal(err)
				}
			}
			body = []byte(`{"name":"Both changed","layout":{"v":1,"widgets":[{"id":"fresh","type":"notice","x":0,"y":0,"w":1,"h":1}]}}`)
			code, response = ts.call("PUT", fmt.Sprintf("/api/shows/%s/boards/%d", ts.showCode, before.ID), body, "application/json")
			if code != 200 {
				t.Fatalf("valid combined update: %d %s", code, response)
			}
			after, err = boards.GetBoard(ts.db.DB, ts.showID, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Name != "Both changed" || after.Layout == before.Layout {
				t.Errorf("valid combined update did not save both fields: %+v", after)
			}
		})
	}
}
