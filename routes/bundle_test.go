// Full-fidelity bundle tests (PLAN §11.9, phase 7): a v2 export carries
// polls + moderation state + votes + screens + boards + presets + zone
// (+ map asset), the round-trip restores them all into a NEW show
// re-keyed, v1 bundles keep importing, and future versions refuse.
package routes_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"timerpi/boards"
	"timerpi/timerpi"
)

const pngHeader = "\x89PNG\r\n\x1a\n"

func TestBundleFullFidelity(t *testing.T) {
	ts := newAPITest(t)

	// Stage the event: cue, poll + question + submission + votes, screens
	// config, board, preset, zone + map asset.
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Welcome", DurationMS: 300_000}); err != nil {
		t.Fatalf("cue: %v", err)
	}
	// The seeded board a real event would have (the default board).
	boards.Migrate(ts.db.DB)
	if _, err := boards.CreateBoard(ts.db.DB, ts.showID, "Main",
		`{"v":1,"widgets":[{"id":"a","type":"notice","x":0,"y":0,"w":4,"h":1}]}`); err != nil {
		t.Fatalf("board: %v", err)
	}
	if err := ts.db.SetShowZone(ts.showID, "Hall A"); err != nil {
		t.Fatalf("zone: %v", err)
	}
	img := append([]byte(pngHeader), bytes.Repeat([]byte{1, 2, 3}, 24)...)
	a, aerr := ts.db.CreateAsset("floorplan.png", "image/png", img)
	if aerr != nil {
		t.Fatalf("asset: %v", aerr)
	}
	if err := ts.db.SetZoneMap("Hall A", a.ID); err != nil {
		t.Fatalf("zone map: %v", err)
	}

	var cloud, q1, q2 int64
	if p, err := ts.db.CreatePoll(timerpi.Poll{ShowID: ts.showID, Kind: timerpi.KindPoll,
		Question: "Lunch?", Options: `["Pizza","Skyr"]`}); err != nil {
		t.Fatalf("poll: %v", err)
	} else {
		cloud = p.ID
	}
	if err := ts.db.SetPollState(ts.showID, cloud, timerpi.StateOpen); err != nil {
		t.Fatalf("open: %v", err)
	}
	if p, err := ts.db.CreatePollRaw(timerpi.Poll{ShowID: ts.showID, Kind: timerpi.KindQA,
		Question: "Louder please", State: timerpi.StateOpen, Author: "bh1"}); err != nil {
		t.Fatalf("qa raw: %v", err)
	} else {
		q1 = p.ID
	}
	if p, err := ts.db.CreatePollRaw(timerpi.Poll{ShowID: ts.showID, Kind: timerpi.KindWordCloud,
		Question: "cozy", State: timerpi.StateHidden, Parent: cloud, Author: "bh2"}); err != nil {
		t.Fatalf("word raw: %v", err)
	} else {
		q2 = p.ID
	}
	if err := ts.db.VoteRaw(cloud, "phone-1", "0", 111); err != nil {
		t.Fatalf("vote: %v", err)
	}
	if err := ts.db.VoteRaw(q1, "phone-2", "1", 222); err != nil {
		t.Fatalf("upvote: %v", err)
	}
	if err := ts.db.SetScreenConfig(ts.showID, "Stage Left", "lcars", 0, "Hall A"); err != nil {
		t.Fatalf("screen: %v", err)
	}
	if _, err := ts.db.SavePreset(ts.showID, "Boards", `{"theme":"lcars"}`); err != nil {
		t.Fatalf("preset: %v", err)
	}

	// Export.
	code, raw := ts.call("GET", "/api/shows/"+ts.showCode+"/file", nil, "")
	if code != 200 {
		t.Fatalf("export: %d %.200s", code, raw)
	}
	var sf struct {
		ManifestVersion int            `json:"manifestVersion"`
		Zone            string         `json:"zone"`
		ZoneMapIndex    int64          `json:"zoneMapIndex"`
		Polls           []timerpi.Poll `json:"polls"`
		Votes           []struct {
			PollID int64  `json:"pollId"`
			Peer   string `json:"peer"`
		} `json:"votes"`
		Screens []struct {
			Name  string `json:"name"`
			Room  string `json:"room"`
			Theme string `json:"theme"`
		} `json:"screens"`
		Boards  []json.RawMessage `json:"boards"`
		Presets []json.RawMessage `json:"presets"`
		Assets  []struct {
			ID   int64  `json:"id"`
			Data string `json:"data"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(raw, &sf); err != nil {
		t.Fatalf("bundle parse: %v (%.200s)", err, raw)
	}
	if sf.ManifestVersion != 2 {
		t.Fatalf("version: %d", sf.ManifestVersion)
	}
	if sf.Zone != "Hall A" || len(sf.Polls) != 3 || len(sf.Screens) != 1 ||
		len(sf.Boards) == 0 || len(sf.Presets) == 0 || sf.ZoneMapIndex != int64(len(sf.Assets)) || len(sf.Assets) != 1 {
		t.Fatalf("bundle content: zone=%q polls=%d screens=%d assets=%d mapIdx=%d boards=%d presets=%d",
			sf.Zone, len(sf.Polls), len(sf.Screens), len(sf.Assets), sf.ZoneMapIndex, len(sf.Boards), len(sf.Presets))
	}
	if !bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(img))) {
		t.Error("map asset bytes missing from the bundle")
	}

	// Import into a NEW show.
	iw, ib := ts.call("POST", "/api/shows/import-file", raw, "")
	if iw != 201 {
		t.Fatalf("import: %d %.200s", iw, ib)
	}
	var importOut struct {
		Code string `json:"code"`
	}
	json.Unmarshal(ib, &importOut)
	impID, ok := timerpi.ResolveShowID(ts.db, importOut.Code)
	if !ok {
		t.Fatalf("imported show not resolvable: %s", importOut.Code)
	}
	impShow, serr := ts.db.GetShow(impID)
	if serr != nil {
		t.Fatalf("imported show: %v", serr)
	}
	if impShow.Zone != "Hall A" {
		t.Errorf("zone not restored: %q", impShow.Zone)
	}
	// Polls round-trip with moderation state + parent re-keyed.
	impPolls, _ := ts.db.ListPolls(impShow.ID)
	if len(impPolls) != 3 {
		t.Fatalf("imported polls: %d", len(impPolls))
	}
	var openQA, word int
	for _, p := range impPolls {
		switch {
		case p.Kind == timerpi.KindQA && p.State == timerpi.StateOpen:
			openQA++
		case p.Kind == timerpi.KindWordCloud && p.Parent > 0 && p.State == timerpi.StateHidden:
			word++
		}
	}
	if openQA != 1 || word != 1 {
		t.Fatalf("moderation state not restored: %+v", impPolls)
	}
	// Votes survived (dedupe key intact).
	voteCount := 0
	for _, p := range impPolls {
		if vs, err := ts.db.ListVotes(p.ID); err == nil {
			voteCount += len(vs)
		}
	}
	if voteCount != 2 {
		t.Errorf("votes not restored: %d", voteCount)
	}
	// Screens + registry came back.
	if scr, err := ts.db.GetScreenByName(impShow.ID, "Stage Left"); err != nil || scr.Theme != "lcars" || scr.Room != "Hall A" {
		t.Errorf("screen registry not restored: %+v err %v", scr, err)
	}
	// Zone map re-pointed at a NEW asset carrying the same bytes.
	if mid := ts.db.ZoneMap(impShow.Zone); mid <= 0 {
		t.Error("zone map pointer not restored")
	} else if na, err := ts.db.GetAsset(mid); err != nil || !bytes.Equal(na.Bytes, img) {
		t.Errorf("restored asset bytes differ: %v", err)
	}
	_ = q2
}

func TestBundleV1StillImports(t *testing.T) {
	ts := newAPITest(t)
	// A hand-written v1 bundle (cues+messages+schedule only).
	v1 := `{"manifestVersion":1,"exportedAt":0,"show":{"title":"Old Day"},
		"cues":[{"label":"Welcome","durationMS":120000,"pos":1}],
		"schedule":{"dayStartTS":0}}`
	code, b := ts.call("POST", "/api/shows/import-file", []byte(v1), "")
	if code != 201 {
		t.Fatalf("v1 import: %d %.200s", code, b)
	}
	// Future version refuses.
	bad := `{"manifestVersion":99,"show":{"title":"Future"},"cues":[]}`
	if code, _ = ts.call("POST", "/api/shows/import-file", []byte(bad), ""); code != 400 {
		t.Errorf("future version accepted: %d", code)
	}
}
