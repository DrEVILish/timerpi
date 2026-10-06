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
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/oscbridge"
	"timerpi/boards"
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
	// Retired surfaces land somewhere useful.
	r.GET("/super", func(c *gin.Context) { c.Redirect(http.StatusFound, "/") })
	r.GET("/setup", func(c *gin.Context) { c.Redirect(http.StatusFound, "/") })
	r.GET("/logout", func(c *gin.Context) {
		clearSessions(c)
		c.Redirect(http.StatusFound, "/")
	})
	// "Leave event" (STATUS U24): drop this browser's sessions for one
	// event (SuperOperator and every room's moderator session); sessions
	// for other events on the same browser stay.
	r.GET("/e/:code/leave", func(c *gin.Context) {
		if ev, ok := d.Store.ResolveEvent(c.Param("code")); ok {
			c.SetCookie(superCookieName(ev.Code), "", -1, "/", "", false, true)
			if rooms, err := d.Store.ListRooms(ev.ID); err == nil {
				for _, r := range rooms {
					c.SetCookie(roomCookieName(r.Code), "", -1, "/", "", false, true)
				}
			}
		}
		c.Redirect(http.StatusFound, "/")
	})

	g := r.Group("/api/events")
	g.POST("", d.apiCreateEvent)
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
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "SuperOperator sign-in required"})
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
		"Page": "event-admin", "Title": ev.Name + " · SuperOperator", "Event": vm,
		"DefaultTheme": config.DefaultTheme(), "Themes": installedThemes(),
	})
}

// ------------------------------------------------------------ sign-in API --

func (d *Deps) apiCreateEvent(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
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
	ev, rooms, err := d.Store.CreateEvent(body.Name, body.Password, body.Rooms)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	for _, r := range rooms {
		d.warmShow(r.ID)
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
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "This event has no supervisor password yet. Sign in to box settings first (/box), then set one."})
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
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "Wrong supervisor password"})
			return
		}
	}
	d.setSuperSession(c, ev)
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
		PW string `json:"pw"`
	}
	if strings.HasPrefix(c.ContentType(), "application/json") {
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
			return "", false
		}
	} else {
		body.PW = c.PostForm("pw")
	}
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
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
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
	}
	if body.Password != nil {
		if !validSuperPassword(*body.Password) {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": superPasswordRule})
			return
		}
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
	d.warmShow(room.ID)
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
	raw, name := bundleBody(c)
	if raw == nil {
		return
	}
	show, n, err := d.importShowFile(raw, name, ev.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.warmShow(show.ID)
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

// Supervisor passwords need 6+ characters (owner decision 2026-10-06,
// BUGLOG RW12); existing shorter ones keep working. Room passwords may be
// anything: they are a light gate, and sign-in is rate limited.
const (
	minSuperPasswordLen = 6
	superPasswordRule   = "The supervisor password needs at least 6 characters"
)

func validSuperPassword(pw string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(pw)) >= minSuperPasswordLen
}
