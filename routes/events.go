package routes

// events.go — the event layer's HTTP surface (PRODUCT §3, §4.1).
//
// Pages
//
//	GET /e/:code          event lobby: pick your room (moderator) or sign in
//	                      as SuperOperator. Anyone holding the event code.
//	GET /e/:code/admin    SuperOperator dashboard (supervisor session).
//
// API (all under /api/events)
//
//	POST   /                          create {name, password, rooms[]} → super session
//	GET    /:code                     lobby data (rooms, who you are)
//	POST   /:code/login {pw}          supervisor sign-in
//	POST   /:code/rooms/:room/login {pw}  moderator sign-in (pw "" when the room has none)
//	— supervisor only —
//	PATCH  /:code {name?, theme?, password?}
//	DELETE /:code
//	GET    /:code/live                per-room live state
//	POST   /:code/verb {verb, room?}  transport/blackout for one room or all
//	POST   /:code/rooms {name}        add a room
//	PATCH  /:code/rooms/:room {name?, password?, clearPassword?, pos?}
//	DELETE /:code/rooms/:room
//	POST   /:code/room-password {password}   same moderator password for every room
//	POST   /:code/map {assetId}       event map (0 clears)
//	POST   /:code/rooms/import        room from a .timerpi.json bundle

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"timerpi/boards"
	"timerpi/config"
	"timerpi/oscbridge"
	"timerpi/timerpi"
)

// eventVerbs: what the SuperOperator panel fires at rooms in bulk. Content
// editing happens inside each room's dashboard (the SuperOperator holds
// moderator rights everywhere).
var eventVerbs = map[string]bool{
	"go": true, "next": true, "prev": true, "pause": true,
	"resume": true, "reset": true, "blank": true, "unblank": true,
}

func registerEvents(r *gin.Engine, d *Deps) {
	r.GET("/e/:code", d.eventLobbyPage)
	r.GET("/e/:code/admin", d.eventAdminPage)
	r.GET("/e/:code/phone", d.phoneSignIn) // phone sign-in by QR (phonelink.go)
	// Retired surfaces land somewhere useful.
	r.GET("/super", func(c *gin.Context) { c.Redirect(http.StatusFound, "/") })
	r.GET("/setup", func(c *gin.Context) { c.Redirect(http.StatusFound, "/") })
	// Sign-out and "Leave event" (STATUS U24) are POSTs from a same-origin
	// form (OriginGuard refuses cross-site posts): a plain link on another
	// site must not sign anyone out. A GET (old bookmark) just goes home.
	r.POST("/logout", func(c *gin.Context) {
		clearSessions(c)
		c.Redirect(http.StatusSeeOther, "/")
	})
	r.GET("/logout", func(c *gin.Context) { c.Redirect(http.StatusFound, "/") })
	// Leave drops this browser's sessions for one event (SuperOperator and
	// every room's moderator session); other events' sessions stay.
	r.POST("/e/:code/leave", func(c *gin.Context) {
		if ev, ok := d.Store.ResolveEvent(c.Param("code")); ok {
			c.SetCookie(superCookieName(ev.Code), "", -1, "/", "", secureCookie(c), true)
			if rooms, err := d.Store.ListRooms(ev.ID); err == nil {
				for _, r := range rooms {
					c.SetCookie(roomCookieName(r.Code), "", -1, "/", "", secureCookie(c), true)
				}
			}
		}
		c.Redirect(http.StatusSeeOther, "/")
	})
	r.GET("/e/:code/leave", func(c *gin.Context) { c.Redirect(http.StatusFound, "/") })

	g := r.Group("/api/events")
	g.POST("", d.apiCreateEvent)
	g.POST("/import", d.apiEventImportNew)   // a downloaded event file → a NEW event (PRODUCT E4)
	g.GET("/:code/export", d.apiEventExport) // download the whole event (no hashes, no screen keys)
	g.GET("/:code", d.apiEventLobby)
	g.POST("/:code/login", d.apiEventLogin)
	g.POST("/:code/rooms/:room/login", d.apiRoomLogin)
	g.PATCH("/:code", d.apiEventPatch)
	g.DELETE("/:code", d.apiEventDelete)
	g.GET("/:code/live", d.apiEventLive)
	g.POST("/:code/verb", d.apiEventVerb)
	g.POST("/:code/rooms", d.apiEventAddRoom)
	g.POST("/:code/rooms/import", d.apiEventImportRoom)
	g.PATCH("/:code/rooms/:room", d.apiEventPatchRoom)
	g.DELETE("/:code/rooms/:room", d.apiEventDeleteRoom)
	g.POST("/:code/room-password", d.apiEventRoomPasswordAll)
	g.POST("/:code/map", d.apiEventMap)
	g.POST("/:code/pair", d.apiEventPair)
	g.POST("/:code/phone-link", d.apiPhoneLink)
}

// ------------------------------------------------------------ resolution --

func (d *Deps) eventParam(c *gin.Context) (timerpi.Event, bool) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "store unavailable"})
		return timerpi.Event{}, false
	}
	ev, ok := d.Store.ResolveEvent(c.Param("code"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Unknown event code"})
		return timerpi.Event{}, false
	}
	return ev, true
}

// requireSuper resolves :code and demands a supervisor session.
func (d *Deps) requireSuper(c *gin.Context) (timerpi.Event, bool) {
	ev, ok := d.eventParam(c)
	if !ok {
		return ev, false
	}
	if !d.isSuper(c, ev) {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "Event Technician sign-in required"})
		return ev, false
	}
	return ev, true
}

// roomOf resolves :room and checks it belongs to ev.
func (d *Deps) roomOf(c *gin.Context, ev timerpi.Event) (timerpi.Show, bool) {
	id, ok := timerpi.ResolveShowID(d.Store, c.Param("room"))
	if ok {
		if sh, err := d.Store.GetShow(id); err == nil && sh.EventID == ev.ID {
			return sh, true
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Unknown room"})
	return timerpi.Show{}, false
}

// ----------------------------------------------------------------- pages --

type eventRoomVM struct {
	Code        string `json:"code"`
	CodeFmt     string `json:"codeFmt"`
	Name        string `json:"name"`
	Pos         int64  `json:"pos"`
	HasPassword bool   `json:"hasPassword"`
	CanModerate bool   `json:"canModerate"`
}

type eventVM struct {
	Code        string        `json:"code"`
	CodeFmt     string        `json:"codeFmt"`
	Name        string        `json:"name"`
	Theme       string        `json:"theme"`
	MapAsset    int64         `json:"mapAsset"`
	HasSuperPW  bool          `json:"hasSuperPassword"`
	IsSuper     bool          `json:"isSuper"`
	EndsAt      int64         `json:"endsAt"` // epoch ms, 0 = no end set
	AtVenue     bool          `json:"atVenue"`
	Rooms       []eventRoomVM `json:"rooms"`
	ScreenCount int           `json:"-"`
}

func (d *Deps) eventView(c *gin.Context, ev timerpi.Event) (eventVM, error) {
	rooms, err := d.Store.ListRooms(ev.ID)
	if err != nil {
		return eventVM{}, err
	}
	cookies := cookieMap(c.Request)
	vm := eventVM{
		Code: ev.Code, CodeFmt: timerpi.FmtCode(ev.Code), Name: ev.Name, Theme: ev.Theme,
		MapAsset: ev.MapAsset, HasSuperPW: ev.HasSuperPassword(),
		IsSuper: SuperFromCookies(d.Store, cookies, ev),
		EndsAt:  ev.EndsAt, AtVenue: ev.Home == "venue",
	}
	for _, r := range rooms {
		vm.Rooms = append(vm.Rooms, eventRoomVM{
			Code: r.Code, CodeFmt: timerpi.FmtCode(r.Code), Name: r.Title, Pos: r.RoomPos,
			HasPassword: r.RoomPW != "",
			CanModerate: vm.IsSuper || ModerateFromCookies(d.Store, cookies, r.ID),
		})
	}
	if vm.Rooms == nil {
		vm.Rooms = []eventRoomVM{}
	}
	return vm, nil
}

func (d *Deps) eventPageEvent(c *gin.Context) (timerpi.Event, bool) {
	if d.Store == nil {
		pageNotFound(c)
		return timerpi.Event{}, false
	}
	ev, ok := d.Store.ResolveEvent(c.Param("code"))
	if !ok {
		pageUnknownCode(c)
		return ev, false
	}
	// Canonical bare code in the URL (typed codes may carry dashes/typos).
	if c.Param("code") != ev.Code {
		u := *c.Request.URL
		u.Path = strings.Replace(u.Path, "/e/"+c.Param("code"), "/e/"+ev.Code, 1)
		c.Redirect(http.StatusFound, u.String())
		return ev, false
	}
	return ev, true
}

func (d *Deps) eventLobbyPage(c *gin.Context) {
	ev, ok := d.eventPageEvent(c)
	if !ok {
		return
	}
	vm, err := d.eventView(c, ev)
	if err != nil {
		pageError(c, err)
		return
	}
	d.render(c, "event", gin.H{"Page": "event", "Title": ev.Name, "Event": vm, "DefaultTheme": config.DefaultTheme()})
}

func (d *Deps) eventAdminPage(c *gin.Context) {
	ev, ok := d.eventPageEvent(c)
	if !ok {
		return
	}
	if !d.isSuper(c, ev) {
		c.Redirect(http.StatusFound, "/e/"+ev.Code+"?signin=super")
		return
	}
	vm, err := d.eventView(c, ev)
	if err != nil {
		pageError(c, err)
		return
	}
	d.render(c, "event-admin", gin.H{
		"Page": "event-admin", "Title": ev.Name + " · Event Technician", "Event": vm,
		"DefaultTheme": config.DefaultTheme(), "Themes": installedThemes(),
	})
}

// ------------------------------------------------------------ sign-in API --

func (d *Deps) apiCreateEvent(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	// A box is attached to one event at a time (VENUE-CLOUD §2).
	if d.BoxEvent != nil {
		if code := d.BoxEvent(); code != "" {
			c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "This box belongs to event " + timerpi.FmtCode(code) + ". Join that event, or create new events on the cloud."})
			return
		}
	}
	var body struct {
		Name     string   `json:"name"`
		Password string   `json:"password"`
		Rooms    []string `json:"rooms"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Give the event a name"})
		return
	}
	if !validSuperPassword(body.Password) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": superPasswordRule})
		return
	}
	if len(body.Rooms) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "At most 50 rooms"})
		return
	}
	// Anyone on the network may create an event: bound it per device and
	// per box (BUGLOG RS7).
	if !eventCreateLimit.allow(c.ClientIP(), time.Now()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "Too many new events from this device — try again in a few minutes"})
		return
	}
	if n, cerr := d.Store.CountEvents(); cerr == nil && n >= maxEventsPerBox {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "This box is full of events — delete old ones first"})
		return
	}
	ev, rooms, err := d.Store.CreateEvent(body.Name, body.Password, body.Rooms)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	for _, r := range rooms {
		d.notifyShow(r.ID)
	}
	d.setSuperSession(c, ev)
	c.JSON(http.StatusCreated, gin.H{"ok": true, "code": ev.Code, "codeFmt": timerpi.FmtCode(ev.Code), "admin": "/e/" + ev.Code + "/admin"})
}

func (d *Deps) apiEventLobby(c *gin.Context) {
	ev, ok := d.eventParam(c)
	if !ok {
		return
	}
	vm, err := d.eventView(c, ev)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "event": vm})
}

func (d *Deps) apiEventLogin(c *gin.Context) {
	ev, ok := d.eventParam(c)
	if !ok {
		return
	}
	pw, okb := readPwBody(c)
	if !okb {
		return
	}
	// Legacy events migrated without a password: only whoever holds the
	// box password may claim them (BUGLOG RW14). Every moderator has the
	// event code, so the code alone must not make anyone SuperOperator.
	if !ev.HasSuperPassword() && !d.isBoxAdmin(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "This event has no Event Technician Password yet. Sign in to box settings first (/box), then set one."})
		return
	}
	if ev.HasSuperPassword() {
		target := "ev:" + ev.Code
		if !loginAllowed(c, target) {
			return
		}
		ok := timerpi.CheckPassword(ev.SuperHash, pw)
		loginResult(c, target, ok)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "Wrong Event Technician Password"})
			return
		}
	}
	d.setSuperSession(c, ev)
	if now := c.GetInt64("clientNow"); now > 0 && d.ClockHint != nil {
		d.ClockHint(time.UnixMilli(now)) // a box without a clock takes the Event Technician's
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "admin": "/e/" + ev.Code + "/admin"})
}

func (d *Deps) apiRoomLogin(c *gin.Context) {
	ev, ok := d.eventParam(c)
	if !ok {
		return
	}
	room, ok := d.roomOf(c, ev)
	if !ok {
		return
	}
	pw, okb := readPwBody(c)
	if !okb {
		return
	}
	if room.RoomPW != "" {
		target := "rm:" + room.Code
		if !loginAllowed(c, target) {
			return
		}
		ok := timerpi.CheckPassword(room.RoomPW, pw)
		loginResult(c, target, ok)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "Wrong room password"})
			return
		}
	}
	d.setRoomSession(c, ev, room)
	c.JSON(http.StatusOK, gin.H{"ok": true, "room": "/c/" + room.Code})
}

// readPwBody reads {pw} JSON or pw= form.
func readPwBody(c *gin.Context) (string, bool) {
	var body struct {
		PW  string `json:"pw"`
		Now int64  `json:"now"` // the browser's clock (a box without one takes it, VENUE-CLOUD §5)
	}
	if strings.HasPrefix(c.ContentType(), "application/json") {
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
			return "", false
		}
	} else {
		body.PW = c.PostForm("pw")
	}
	c.Set("clientNow", body.Now)
	return body.PW, true
}

// --------------------------------------------------------------- admin API --

func (d *Deps) apiEventPatch(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	var body struct {
		Name     *string `json:"name"`
		Theme    *string `json:"theme"`
		Password *string `json:"password"`
		EndsAt   *int64  `json:"endsAt"` // epoch ms; 0 clears
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	// Check every field before writing any (BUGLOG RW42): a short password
	// used to be refused after the rename had already been saved.
	if body.Name != nil && strings.TrimSpace(*body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "The event needs a name"})
		return
	}
	if body.Password != nil && !validSuperPassword(*body.Password) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": superPasswordRule})
		return
	}
	if body.EndsAt != nil && *body.EndsAt < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Bad end date"})
		return
	}
	if body.Theme != nil && !themeKnown(*body.Theme) { // RS16
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Unknown theme"})
		return
	}
	if body.Name != nil {
		if err := d.Store.RenameEvent(ev.ID, *body.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}
	}
	if body.Theme != nil {
		t := sanitizeTheme(*body.Theme)
		if err := d.Store.SetEventTheme(ev.ID, t); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		// Screens on the event default follow live (PRODUCT S4, REPORT #10).
		for _, rid := range d.eventRoomIDs(ev.ID) {
			if scr, err := d.Store.ListScreens(rid); err == nil {
				for _, s := range scr {
					if s.Theme == "" {
						d.pushScreen(rid, s.Name)
					}
				}
			}
		}
	}
	if body.EndsAt != nil {
		if err := d.Store.SetEventEnd(ev.ID, *body.EndsAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
	}
	if body.Password != nil {
		if err := d.Store.SetEventSuperPassword(ev.ID, *body.Password); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		// Re-issue this browser's session against the new hash; every
		// other holder of the old password is signed out, open sockets
		// included.
		if ev2, err := d.Store.GetEvent(ev.ID); err == nil {
			d.setSuperSession(c, ev2)
		}
		d.recheckControls(d.eventRoomIDs(ev.ID)...)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Deps) apiEventDelete(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	rooms, err := d.Store.ListRooms(ev.ID)
	if err != nil { // without the list, live engines would outlive the event
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if err := d.Store.DeleteEvent(ev.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	for _, r := range rooms {
		d.forgetRoom(r.ID)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// liveRoom is one room card on the SuperOperator dashboard.
type liveRoom struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	HasPassword bool   `json:"hasPassword"`
	Running     bool   `json:"running"`
	Paused      bool   `json:"paused"`
	ActiveLabel string `json:"activeLabel"`
	NextLabel   string `json:"nextLabel"`
	RemainingMS int64  `json:"remainingMS"`
	Overtime    bool   `json:"overtime"`
	Blanked     bool   `json:"blanked"`
	Sessions    int    `json:"sessions"`
	Screens     int    `json:"screens"`
	Cues        int    `json:"cues"`
}

func (d *Deps) liveRooms(ev timerpi.Event) ([]liveRoom, error) {
	rooms, err := d.Store.ListRooms(ev.ID)
	if err != nil {
		return nil, err
	}
	out := make([]liveRoom, 0, len(rooms))
	for _, s := range rooms {
		lr := liveRoom{Code: s.Code, Name: s.Title, HasPassword: s.RoomPW != "", Blanked: s.Blanked}
		if eng, err := d.engineFor(s.ID); err == nil {
			if snap, serr := eng.Snapshot(); serr == nil {
				lr.Running = snap.Runtime.Running
				lr.Paused = snap.Runtime.Paused
				lr.RemainingMS = snap.Runtime.RemainingMS
				lr.Overtime = snap.Runtime.Overtime
				lr.Cues = len(snap.Cues)
				for _, cue := range snap.Cues {
					if cue.Pos == snap.Runtime.ActivePos {
						lr.ActiveLabel = cue.Label
					}
					if cue.Pos == snap.Runtime.NextPos {
						lr.NextLabel = cue.Label
					}
				}
			}
		}
		if d.Hub != nil {
			lr.Sessions = d.Hub.ShowSessions(s.ID)
			for _, n := range d.Hub.ScreenSessions(s.ID) {
				if n > 0 {
					lr.Screens++
				}
			}
		}
		out = append(out, lr)
	}
	return out, nil
}

func (d *Deps) apiEventLive(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	rooms, err := d.liveRooms(ev)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "rooms": rooms, "serverTime": d.now()})
}

// POST /:code/verb {verb, room?} — room "" = every room of the event.
func (d *Deps) apiEventVerb(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	var body struct {
		Verb string `json:"verb"`
		Room string `json:"room"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || !eventVerbs[body.Verb] {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unknown verb"})
		return
	}
	rooms, err := d.Store.ListRooms(ev.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	want := timerpi.NormalizeCode(body.Room)
	results := []gin.H{}
	for _, r := range rooms {
		if want != "" && r.Code != want {
			continue
		}
		if err := d.applyRoomVerb(r.ID, body.Verb); err != nil {
			results = append(results, gin.H{"code": r.Code, "ok": false, "error": err.Error()})
			continue
		}
		d.logAction(r.ID, "super", body.Verb)
		results = append(results, gin.H{"code": r.Code, "ok": true})
	}
	if want != "" && len(results) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Unknown room"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "results": results})
}

// applyRoomVerb runs one transport/blackout verb against one room.
func (d *Deps) applyRoomVerb(showID int64, verb string) error {
	switch verb {
	case "blank", "unblank":
		if err := d.Store.SetShowBlanked(showID, verb == "blank"); err != nil {
			return err
		}
		if verb == "blank" {
			oscbridge.FireOut("panic", 0)
		} else {
			oscbridge.FireOut("go", 0)
		}
		if eng, err := d.engineFor(showID); err == nil {
			_ = eng.Notify()
		}
		return nil
	default:
		eng, err := d.engineFor(showID)
		if err != nil {
			return err
		}
		return eng.ApplyCmd(verb, map[string]any{})
	}
}

func (d *Deps) apiEventAddRoom(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	if !d.roomRoomLeft(c, ev) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Give the room a name"})
		return
	}
	room, err := d.Store.CreateRoom(ev.ID, body.Name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.notifyShow(room.ID)
	c.JSON(http.StatusCreated, gin.H{"ok": true, "code": room.Code, "name": room.Title})
}

func (d *Deps) apiEventPatchRoom(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	room, ok := d.roomOf(c, ev)
	if !ok {
		return
	}
	var body struct {
		Name          *string `json:"name"`
		Password      *string `json:"password"`
		ClearPassword bool    `json:"clearPassword"`
		Pos           *int64  `json:"pos"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) == "" { // before any write (RW42)
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "The room needs a name"})
		return
	}
	if body.Name != nil {
		if err := d.Store.RenameShow(room.ID, *body.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}
	}
	if body.ClearPassword {
		if err := d.Store.SetRoomPassword(room.ID, ""); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		d.recheckControls(room.ID)
	} else if body.Password != nil && strings.TrimSpace(*body.Password) != "" {
		if err := d.Store.SetRoomPassword(room.ID, *body.Password); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		d.recheckControls(room.ID)
	}
	if body.Pos != nil {
		if err := d.Store.MoveRoom(ev.ID, room.ID, *body.Pos); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
	}
	if eng, err := d.engineFor(room.ID); err == nil {
		_ = eng.Notify()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Deps) apiEventDeleteRoom(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	room, ok := d.roomOf(c, ev)
	if !ok {
		return
	}
	// Event layouts this room made stay with the event (other rooms'
	// screens may use them).
	if err := boards.RehomeRoomLayouts(d.Store.DB, room.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if err := d.Store.DeleteShow(room.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.forgetRoom(room.ID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Deps) apiEventRoomPasswordAll(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	rooms, err := d.Store.ListRooms(ev.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	for _, r := range rooms {
		if err := d.Store.SetRoomPassword(r.ID, body.Password); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		d.recheckControls(r.ID)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "rooms": len(rooms), "enabled": strings.TrimSpace(body.Password) != ""})
}

func (d *Deps) apiEventMap(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	var body struct {
		AssetID int64 `json:"assetId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	if body.AssetID > 0 {
		a, err := d.Store.GetAsset(body.AssetID)
		if err != nil || (a.EventID != 0 && a.EventID != ev.ID) {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown asset"})
			return
		}
	}
	if err := d.Store.SetEventMap(ev.ID, body.AssetID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Deps) apiEventImportRoom(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	if !d.roomRoomLeft(c, ev) {
		return
	}
	raw, msg := bundleBody(c)
	if raw == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": msg})
		return
	}
	show, n, err := d.importShowFile(raw, "", ev.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.notifyShow(show.ID)
	c.JSON(http.StatusCreated, gin.H{"ok": true, "code": show.Code, "name": show.Title, "cueCount": n})
}

// sanitizeTheme keeps a theme slug to the installed-bundle charset.
func sanitizeTheme(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	var b strings.Builder
	for _, r := range t {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// recheckControls drops open operator sockets of the rooms whose cookies a
// password change just invalidated (BUGLOG RW10).
func (d *Deps) recheckControls(roomIDs ...int64) {
	h, ok := d.Hub.(interface{ RecheckControls(int64) int })
	if d.Hub == nil || !ok {
		return
	}
	for _, id := range roomIDs {
		h.RecheckControls(id)
	}
}

// eventRoomIDs lists the event's room ids (best effort: empty on error).
func (d *Deps) eventRoomIDs(eventID int64) []int64 {
	rooms, err := d.Store.ListRooms(eventID)
	if err != nil {
		return nil
	}
	ids := make([]int64, len(rooms))
	for i, r := range rooms {
		ids[i] = r.ID
	}
	return ids
}

// forgetRoom drops a deleted room's engine and its hub entry (BUGLOG
// RW52: event and room delete used to leave the hub bucket behind).
func (d *Deps) forgetRoom(id int64) {
	if d.Engines != nil {
		d.Engines.Drop(id)
	}
	if h, ok := d.Hub.(interface{ Forget(int64) }); d.Hub != nil && ok {
		h.Forget(id)
	}
}

// Limits on open creation (BUGLOG RS7).
const (
	maxEventsPerBox  = 1000
	maxRoomsPerEvent = 100
)

var eventCreateLimit = &windowLimiter{max: 60, window: 10 * time.Minute, bound: 20_000}

// roomRoomLeft answers 409 when the event already has maxRoomsPerEvent rooms.
func (d *Deps) roomRoomLeft(c *gin.Context, ev timerpi.Event) bool {
	if rooms, err := d.Store.ListRooms(ev.ID); err == nil && len(rooms) >= maxRoomsPerEvent {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "An event holds at most 100 rooms"})
		return false
	}
	return true
}

// Supervisor passwords need 6+ characters (owner decision 2026-10-06,
// BUGLOG RW12); existing shorter ones keep working. Room passwords may be
// anything: they are a light gate, and sign-in is rate limited.
const (
	minSuperPasswordLen = 6
	superPasswordRule   = "The Event Technician Password needs at least 6 characters"
)

func validSuperPassword(pw string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(pw)) >= minSuperPasswordLen
}
