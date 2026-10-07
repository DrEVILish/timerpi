package venue

// End to end in one process (STATUS N12–N16): a cloud and a box, each with
// its own database, hub and router. The box pairs by code, pulls its event
// from the cloud, links up, relays the audience, takes a remote operator's
// change through the tunnel once the link is stable, streams its copy, and
// hands the final copy back at release.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"timerpi/routes"
	"timerpi/timerpi"
	"timerpi/views"
	"timerpi/ws"
)

type side struct {
	db   *timerpi.DB
	deps *routes.Deps
	hub  *ws.Hub
	srv  *httptest.Server
	port int
}

func newSide(t *testing.T, role string) *side {
	t.Helper()
	db, err := timerpi.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	engines := timerpi.NewEngines(db)
	tmpl, err := views.New("../templates")
	if err != nil {
		t.Fatal(err)
	}
	hub := ws.NewHub(engines, tmpl.Fragment)
	hub.SetStore(db)
	hub.SetMessagesFunc(db.ListMessages)
	hub.SetLogger(func(string, ...any) {})
	gin.SetMode(gin.TestMode)
	deps := &routes.Deps{Engines: engines, Store: db, Hub: hub, Tmpl: tmpl, Role: role}
	g := routes.New(deps)
	if role == "cloud" {
		hub.SetPollsFunc(deps.WrapPolls(db.OnAirNow))
		hub.SetTunnel(deps.Tunnel)
		hub.SetCommandGate(deps.CommandGate)
	} else {
		hub.SetPollsFunc(db.OnAirNow)
	}
	srv := httptest.NewServer(g)
	t.Cleanup(func() { srv.Close(); hub.Stop() })
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	var p int
	fmt.Sscan(port, &p)
	return &side{db: db, deps: deps, hub: hub, srv: srv, port: p}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestMain sets short link timings once for this test binary (link
// goroutines keep reading them while they wind down).
func TestMain(m *testing.M) {
	routes.StableAfter, CopyEvery = 400*time.Millisecond, 50*time.Millisecond
	os.Exit(m.Run())
}

func TestCloudVenueEndToEnd(t *testing.T) {
	timerpi.SetPasswordHashIterations(1000)

	cloud := newSide(t, "cloud")
	box := newSide(t, "box")

	// The event is prepared on the cloud.
	ev, rooms, err := cloud.db.CreateEvent("Conf 2026", "techpass", []string{"Room A"})
	if err != nil {
		t.Fatal(err)
	}
	room := rooms[0]
	jar, _ := cookiejar.New(nil)
	op := &http.Client{Jar: jar} // the Event Technician's browser on the cloud
	post := func(c *http.Client, url, body string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, b := post(op, cloud.srv.URL+"/api/events/"+ev.Code+"/login", `{"pw":"techpass"}`); code != 200 {
		t.Fatalf("login: %d %s", code, b)
	}

	// 1. The box shows a code; the Event Technician pairs it on the cloud.
	a := &Agent{Store: box.db, Srv: box.deps, Handler: box.srv.Config.Handler, Hub: box.hub,
		Port: box.port, Name: "box-a", CloudURL: func() string { return cloud.srv.URL }, Logf: t.Logf}
	box.deps.BoxSelf = func() any { return a.Self() }
	box.deps.BoxEvent = func() string { return a.Pairing().Event }
	box.deps.OnRelease = a.OnRelease
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.step(ctx) // registers its code with the cloud
	code := a.Self().Code
	if len(code) != 6 {
		t.Fatalf("no pairing code: %+v", a.Self())
	}
	if c, b := post(op, cloud.srv.URL+"/api/events/"+ev.Code+"/pair",
		fmt.Sprintf(`{"pairCode":%q,"code":%q,"kind":"presenter","template":"dsm","name":"Stage"}`, code, room.Code)); c != 200 {
		t.Fatalf("pair: %d %s", c, b)
	}
	a.step(ctx) // takes the pairing, then pulls the event from the cloud
	a.step(ctx)
	p := a.Pairing()
	if p.Event != ev.Code || p.Room != room.Code || p.Screen != "Stage" || p.ScreenKey == "" {
		t.Fatalf("pairing = %+v", p)
	}
	local, ok := box.db.ResolveEvent(ev.Code)
	if !ok {
		t.Fatal("the box didn't pull its event from the cloud")
	}
	if !box.db.ScreenKeyValid(room.ID, "Stage", p.ScreenKey) && !keyValidByCode(box.db, room.Code, "Stage", p.ScreenKey) {
		t.Fatal("the box's own screen key doesn't work on its copy")
	}
	if s := a.Self(); !s.Paired || !strings.HasPrefix(s.Target, "/d/"+room.Code+"?screen=Stage&key=") {
		t.Fatalf("box screen = %+v", s)
	}

	// 2. The box links up; the cloud now treats the event as at the venue.
	go a.linkLoop(ctx)
	waitFor(t, "the link", func() bool { e, _ := cloud.db.ResolveEvent(ev.Code); return e.Home == "venue" })

	// 3. Audience relay: an item shown at the venue reaches phones on the
	// cloud; a phone's vote on the cloud is counted at the venue.
	boxRoom, _ := box.db.ResolveShowInEvent(local.ID, room.Code)
	pl, err := box.db.CreatePoll(timerpi.Poll{ShowID: boxRoom.ID, Kind: "poll", Question: "Lunch?", Options: `["Pizza","Soup"]`})
	if err != nil {
		t.Fatal(err)
	}
	if err := box.db.ShowTo(boxRoom.ID, pl.ID, "audience", true); err != nil {
		t.Fatal(err)
	}
	box.hub.BroadcastPoll(boxRoom.ID)
	phone := &http.Client{}
	waitFor(t, "the item on the cloud", func() bool {
		resp, err := phone.Get(cloud.srv.URL + "/api/audience/" + room.Code)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return strings.Contains(string(b), "Lunch?")
	})
	if c, b := post(phone, cloud.srv.URL+"/api/audience/"+room.Code+"/vote", fmt.Sprintf(`{"pollId":%d,"choice":"1"}`, pl.ID)); c != 200 {
		t.Fatalf("phone vote through the cloud: %d %s", c, b)
	}
	if votes, _ := box.db.ListVotes(pl.ID); len(votes) != 1 || votes[0].Choice != "1" {
		t.Fatalf("venue votes = %+v", votes)
	}

	// 4. A remote operator: read-only until the link is stable, then the
	// change goes to the venue (rule 5).
	addMsg := func() (int, string) {
		return post(op, cloud.srv.URL+"/api/shows/"+room.Code+"/messages", `{"text":"WRAP UP","color":""}`)
	}
	if c, _ := addMsg(); c != http.StatusServiceUnavailable {
		t.Fatalf("a cloud change before the link was stable: %d", c)
	}
	time.Sleep(routes.StableAfter + 100*time.Millisecond)
	if c, b := addMsg(); c != 200 {
		t.Fatalf("remote change through the tunnel: %d %s", c, b)
	}
	if msgs, _ := box.db.ListMessages(boxRoom.ID); len(msgs) != 1 || msgs[0].Text != "WRAP UP" {
		t.Fatalf("venue messages = %+v", msgs)
	}

	// 4b. The remote operator's live socket: joined on the cloud, served by
	// the venue's hub; a command lands at the venue.
	u, _ := url.Parse(cloud.srv.URL)
	hdr := http.Header{}
	for _, ck := range jar.Cookies(u) {
		hdr.Add("Cookie", ck.Name+"="+ck.Value)
	}
	hdr.Set("Origin", cloud.srv.URL)
	wsc, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(cloud.srv.URL, "http")+"/ws", hdr)
	if err != nil {
		t.Fatal(err)
	}
	defer wsc.Close()
	_ = wsc.WriteJSON(map[string]any{"v": 1, "t": "join", "role": "controls", "show": room.Code, "peerId": "remote-op"})
	_ = wsc.SetReadDeadline(time.Now().Add(5 * time.Second))
	joined := false
	for !joined {
		var f map[string]any
		if err := wsc.ReadJSON(&f); err != nil {
			t.Fatalf("tunnelled socket: %v", err)
		}
		if f["t"] == "err" {
			t.Fatalf("tunnelled join refused: %v", f["message"])
		}
		joined = f["t"] == "joined"
	}
	_ = wsc.WriteJSON(map[string]any{"t": "cmd", "action": "addMsg", "args": map[string]any{"text": "FIVE MINUTES"}})
	waitFor(t, "the socket's command at the venue", func() bool {
		msgs, _ := box.db.ListMessages(boxRoom.ID)
		return len(msgs) == 2
	})

	// 5. The venue's copy reaches the cloud (venue changes win).
	if _, err := box.db.CreateCue(boxRoom.ID, timerpi.Cue{Label: "Keynote", DurationMS: 1_800_000}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the venue copy on the cloud", func() bool {
		cues, _ := cloud.db.ListCues(room.ID)
		return len(cues) == 1 && cues[0].Label == "Keynote"
	})

	// 6. Release: end + 4 h passed at the venue → final copy to the cloud,
	// the cloud is home again, the box lets go and shows a code.
	if err := box.db.SetEventEnd(local.ID, time.Now().Add(-5*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if got := box.deps.ReleaseDue(); len(got) != 1 {
		t.Fatalf("release at the venue: %v", got)
	}
	e, _ := cloud.db.ResolveEvent(ev.Code)
	if e.Home != "" || e.ReleasedAt == 0 {
		t.Fatalf("cloud after release: %+v", e)
	}
	if a.Pairing().Event != "" || a.Self().Code == "" {
		t.Fatalf("box after release: %+v", a.Pairing())
	}
	if _, still := box.db.ResolveEvent(ev.Code); still {
		t.Fatal("the box kept the event after its final copy reached the cloud")
	}
	if cues, _ := cloud.db.ListCues(room.ID); len(cues) != 1 {
		t.Fatal("the final copy lost the venue's running order")
	}
}

// The link drops: phones on the cloud see "audience paused".
func TestAudiencePausedWithoutLink(t *testing.T) {
	cloud := newSide(t, "cloud")
	ev, rooms, err := cloud.db.CreateEvent("Conf", "techpass", []string{"Room A"})
	if err != nil {
		t.Fatal(err)
	}
	_ = cloud.db.SetEventHome(ev.ID, "venue")
	resp, err := http.Get(cloud.srv.URL + "/api/audience/" + rooms[0].Code)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var out struct{ Paused bool }
	_ = json.Unmarshal(b, &out)
	if !out.Paused {
		t.Fatalf("no link but not paused: %s", b)
	}
	resp2, _ := http.Post(cloud.srv.URL+"/api/audience/"+rooms[0].Code+"/vote", "application/json", bytes.NewReader([]byte(`{"pollId":1,"choice":"0"}`)))
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("vote without a link: %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

func keyValidByCode(db *timerpi.DB, code, screen, key string) bool {
	id, ok := timerpi.ResolveShowID(db, code)
	return ok && db.ScreenKeyValid(id, screen, key)
}

// A box whose clock was never set takes a trusted time; a box with a sane
// clock is never moved.
func TestClockHint(t *testing.T) {
	var set []time.Time
	oldSet, oldNow := setClock, nowFn
	t.Cleanup(func() { setClock, nowFn = oldSet, oldNow })
	setClock = func(tt time.Time) error { set = append(set, tt); return nil }
	a := &Agent{Logf: t.Logf}
	real := ClockFloor.Add(48 * time.Hour)

	nowFn = func() time.Time { return time.Date(1970, 1, 1, 0, 2, 0, 0, time.UTC) }
	a.ClockHint(time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)) // not trusted: before the floor
	a.ClockHint(real)
	if len(set) != 1 || !set[0].Equal(real) {
		t.Fatalf("unset clock: set %v", set)
	}
	nowFn = func() time.Time { return real }
	a.ClockHint(real.Add(time.Hour))
	if len(set) != 1 {
		t.Fatal("moved a clock that was already set")
	}
}
