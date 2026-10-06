package routes

// walkin.go — the event walk-in feed (PRODUCT S8): every room of the
// event with what is on now, what is next, and the full-day schedule,
// computed from each room's LIVE engine (not just the plan).
//
//	GET /api/shows/:ident/walkin   open (screens never sign in)
//
// Addressed by any room code of the event, so a walk-in screen captured
// into one room shows the whole event. Signage data only: room names and
// session titles, never codes of other rooms or passwords.

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/timerpi"
)

type walkinRow struct {
	Label    string `json:"label"`
	Speaker  string `json:"speaker,omitempty"`
	Location string `json:"location,omitempty"` // a break's place (U14)
	StartTS  int64  `json:"startTS,omitempty"`  // 0 = the day has no start time yet
	EndTS    int64  `json:"endTS,omitempty"`
	Break    bool   `json:"break,omitempty"`
	State    string `json:"state"` // done | now | next | later
}

type walkinRoom struct {
	Name        string      `json:"name"`
	Label       string      `json:"label"` // how screens print it: "Room: Name" when the event has several rooms (U6)
	Here        bool        `json:"here"`  // the room this screen belongs to
	Running     bool        `json:"running"`
	Paused      bool        `json:"paused"`
	RemainingMS int64       `json:"remainingMS"`
	Now         *walkinRow  `json:"now,omitempty"`
	Next        *walkinRow  `json:"next,omitempty"`
	Schedule    []walkinRow `json:"schedule"`
	id          int64       // the room's show id (sets Here per request)
}

// walkinCache holds each event's feed for walkinTTL. Every walk-in screen
// polls every 5 s and each build snapshots every room of the event, so
// 20 screens x 10 rooms used to cost ~150 queries a second on a Pi
// (BUGLOG RW57). One build per event at a time: concurrent requests wait
// for it and share the result.
const walkinTTL = 2 * time.Second

type walkinFeed struct {
	mu    sync.Mutex
	at    time.Time
	now   int64
	ev    timerpi.Event
	rooms []walkinRoom
}

type walkinCache struct {
	mu sync.Mutex
	m  map[int64]*walkinFeed
}

// walkinFeedOf returns the event's cached feed, rebuilding it when older
// than walkinTTL.
func (d *Deps) walkinFeedOf(eventID int64) (*walkinFeed, error) {
	d.walkin.mu.Lock()
	if d.walkin.m == nil {
		d.walkin.m = map[int64]*walkinFeed{}
	}
	f := d.walkin.m[eventID]
	if f == nil {
		f = &walkinFeed{}
		d.walkin.m[eventID] = f
	}
	d.walkin.mu.Unlock()

	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.at.IsZero() && time.Since(f.at) < walkinTTL {
		return f, nil
	}
	ev, err := d.Store.GetEvent(eventID)
	if err != nil {
		return nil, err
	}
	rooms, err := d.Store.ListRooms(ev.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	out := make([]walkinRoom, 0, len(rooms))
	for _, r := range rooms {
		wr := d.walkinRoomOf(r, false, now)
		wr.id = r.ID
		wr.Label = wr.Name
		if len(rooms) > 1 {
			wr.Label = "Room: " + wr.Name
		}
		out = append(out, wr)
	}
	f.ev, f.rooms, f.now, f.at = ev, out, now, time.Now()
	return f, nil
}

func (d *Deps) apiWalkin(c *gin.Context) {
	id, ok := d.requireShow(c)
	if !ok {
		return
	}
	here, err := d.Store.GetShow(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false})
		return
	}
	f, err := d.walkinFeedOf(here.EventID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false})
		return
	}
	f.mu.Lock()
	ev, now := f.ev, f.now
	out := make([]walkinRoom, len(f.rooms))
	copy(out, f.rooms)
	f.mu.Unlock()
	for i := range out {
		out[i].Here = out[i].id == id
	}
	mapURL := ""
	if ev.MapAsset > 0 {
		mapURL = assetURL(ev.MapAsset)
	}
	// serverTime is when the feed was built, so remainingMS stays exact.
	c.JSON(http.StatusOK, gin.H{"ok": true, "serverTime": now,
		"event": gin.H{"name": ev.Name, "map": mapURL}, "rooms": out})
}

func (d *Deps) walkinRoomOf(r timerpi.Show, here bool, now int64) walkinRoom {
	wr := walkinRoom{Name: r.Title, Here: here, Schedule: []walkinRow{}}
	eng, err := d.engineFor(r.ID)
	if err != nil {
		return wr
	}
	snap, err := eng.Snapshot()
	if err != nil {
		return wr
	}
	rt := snap.Runtime
	wr.Running, wr.Paused, wr.RemainingMS = rt.Running, rt.Paused, rt.RemainingMS
	dayStart := rt.DayStartTS
	if dayStart == 0 && r.DayStart != "" {
		dayStart = timerpi.DayStartTSFrom(r.DayStart, now)
	}
	sched := timerpi.ComputeSchedule(snap.Cues, dayStart, 1)
	// The live playhead decides done/now/next; the plan only supplies times.
	active := int64(0)
	if rt.Running || rt.Paused {
		active = rt.ActivePos
	}
	nextPos := rt.NextPos
	if active == 0 && nextPos == 0 && rt.ActivePos == 0 && len(sched.Rows) > 0 {
		nextPos = sched.Rows[0].Pos // nothing started yet: the first session is next
	}
	for _, row := range sched.Rows {
		w := walkinRow{Label: row.Label, Speaker: row.Speaker, Location: row.Location, Break: row.Break, State: "later"}
		if dayStart > 0 {
			w.StartTS, w.EndTS = row.StartTS, row.EndTS
		}
		switch {
		case active > 0 && row.Pos == active:
			w.State = "now"
		case row.Pos == nextPos && row.Pos != active:
			w.State = "next"
		case (active > 0 && row.Pos < active) || (active == 0 && rt.ActivePos > 0 && row.Pos <= rt.ActivePos):
			w.State = "done"
		}
		wr.Schedule = append(wr.Schedule, w)
		ww := w
		if w.State == "now" {
			wr.Now = &ww
		}
		if w.State == "next" && wr.Next == nil {
			wr.Next = &ww
		}
	}
	return wr
}
