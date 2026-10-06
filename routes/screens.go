// screens.go — F1/F2: the operator's screens system.
//
// Every display tab self-registers a STABLE screen identity (?screen= URL
// name, else localStorage, else generated — see public/src/mesh.js
// screenName()) carried on the WS join. The registry (SQLite `screens`)
// survives reconnects; the operator's dashboard panel (GET /screens) lists
// all screens with live presence, lets them assign a theme + board per
// screen (or Match one screen to all), rename/forget entries, and save the
// whole configuration as named presets (SQLite `display_presets`) that
// export/import as plain JSON files.
//
// Assignment reaches a live screen by targeted push over its own session
// ({t:"display",theme} reuses the B7 theme frame; {t:"screen-board"} is
// board.js's navigate signal; {t:"screen-rename"} makes a renamed tab
// re-adopt + rejoin). Registry changes refresh every controls tab with
// {t:"screens", …}. Display pages also fetch their own config on boot
// (screens/self, ungated beyond the show itself — theme names and board
// ids are not credentials; /d/ stays login-free by contract).
package routes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"timerpi/boards"
	"timerpi/timerpi"
	"timerpi/views"
)

var screenThemeRe = regexp.MustCompile(`^[a-z0-9_-]{0,40}$`)

// screenSessionView is one live tab (the gallery/panel disconnect button).
type screenSessionView struct {
	PeerID string `json:"peerId"`
	Role   string `json:"role"`
}

// screenBoxView is one layout tile for the gallery preview.
type screenBoxView struct {
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	Type string `json:"type"`
}

type screenView struct {
	Name      string `json:"name"`
	Theme     string `json:"theme"`
	BoardID   int64  `json:"boardId"`
	Template  string `json:"template"` // built-in shown directly ("" = none)
	Room      string `json:"room"`
	LastSeen  int64  `json:"lastSeen"`
	Sessions  int    `json:"sessions"` // live tabs under this name
	Connected bool   `json:"connected"`

	BoardName    string              `json:"boardName,omitempty"`
	Widgets      []screenBoxView     `json:"widgets,omitempty"`
	Peers        []screenSessionView `json:"peers,omitempty"`
	PreviewLabel string              `json:"previewLabel,omitempty"`
	PreviewClock string              `json:"previewClock,omitempty"`
	PreviewPct   int                 `json:"previewPct,omitempty"`

	Kind        string `json:"kind"`        // audience | walkin | presenter | ""
	Rotation    int    `json:"rotation"`    // 0/90/180/270
	Rows        int    `json:"rows"`        // layout canvas rows (preview geometry)
	Orientation string `json:"orientation"` // layout orientation
}

// registerScreens mounts the F1/F2 endpoints into the /api/shows group.
func registerScreens(g *gin.RouterGroup, d *Deps) {
	g.GET("/shows/:ident/screens", d.apiScreens)
	g.GET("/shows/:ident/screens/self", d.apiScreenSelf)
	g.POST("/shows/:ident/screens/config", d.apiScreenConfig)
	g.POST("/shows/:ident/screens/match", d.apiScreenMatch)
	g.POST("/shows/:ident/screens/rename", d.apiScreenRename)
	g.POST("/shows/:ident/screens/forget", d.apiScreenForget)
	g.POST("/shows/:ident/screens/link", d.apiScreenLink)
	g.POST("/shows/:ident/screens/template", d.apiScreenTemplate)

	g.GET("/shows/:ident/presets", d.apiPresetsList)
	g.POST("/shows/:ident/presets", d.apiPresetsSave)
	g.POST("/shows/:ident/presets/:pid/apply", d.apiPresetApply)
	g.DELETE("/shows/:ident/presets/:pid", d.apiPresetDelete)
	g.GET("/shows/:ident/presets/:pid/export", d.apiPresetExport)
	g.POST("/shows/:ident/presets/import", d.apiPresetImport)
}

// screensPayload builds the panel/gallery view: registry rows merged with
// live session presence, enriched with the assigned board's layout boxes and
// current-snapshot preview values (the gallery shows what each screen IS
// showing; the JS re-polls to keep it live).
func (d *Deps) screensPayload(id int64) ([]screenView, error) {
	rows, err := d.Store.ListScreens(id)
	if err != nil {
		return nil, err
	}
	live := map[string]int{}
	peers := map[string][][2]string{}
	if d.Hub != nil {
		live = d.Hub.ScreenSessions(id)
		peers = d.Hub.ScreenPeers(id)
	}
	boardNames := map[int64]string{}
	var defaultBoardID int64
	if d.Store != nil {
		if list, berr := boards.ListBoards(d.Store.DB, id); berr == nil && len(list) > 0 {
			defaultBoardID = list[0].ID // the seeded show default leads the list
			for _, b := range list {
				boardNames[b.ID] = b.Name
			}
		}
	}
	var activeLabel, activeClock string
	var activePct int
	if d.Engines != nil {
		if eng, gerr := d.Engines.Get(id); gerr == nil {
			if snap, serr := eng.Snapshot(); serr == nil {
				for _, c := range snap.Cues {
					if c.Pos == snap.Runtime.ActivePos {
						activeLabel = c.Label
						if c.DurationMS > 0 {
							p := (c.DurationMS - snap.Runtime.RemainingMS) * 100 / c.DurationMS
							if p < 0 {
								p = 0
							}
							if p > 100 {
								p = 100
							}
							activePct = int(p)
						}
					}
				}
				if snap.Runtime.Running && !snap.Runtime.Paused && snap.Runtime.RemainingMS > 0 {
					activeClock = views.FmtDur(snap.Runtime.RemainingMS)
				} else {
					activeClock = "idle"
				}
			}
		}
	}

	out := make([]screenView, 0, len(rows)+len(live))
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Name] = true
		out = append(out, d.screenCard(r, live, peers,
			boardNames, defaultBoardID, id, activeLabel, activeClock, activePct))
	}
	for name := range live { // tabs that joined before their registry row was read
		if seen[name] {
			continue
		}
		out = append(out, d.screenCard(timerpi.Screen{Name: name}, live, peers, boardNames, defaultBoardID,
			id, activeLabel, activeClock, activePct))
	}
	return out, nil
}

// screenCard assembles one panel/gallery entry with its preview boxes.
func (d *Deps) screenCard(r timerpi.Screen,
	live map[string]int, peers map[string][][2]string,
	boardNames map[int64]string, defaultBoardID, showID int64,
	activeLabel, activeClock string, activePct int) screenView {
	name, boardID := r.Name, r.BoardID
	v := screenView{Name: name, Theme: r.Theme, BoardID: boardID, Room: r.Room, LastSeen: r.LastSeen,
		Sessions: live[name], Connected: live[name] > 0, Kind: r.Kind, Rotation: r.Rotation,
		PreviewLabel: activeLabel, PreviewClock: activeClock, PreviewPct: activePct}
	for _, pr := range peers[name] {
		v.Peers = append(v.Peers, screenSessionView{PeerID: pr[0], Role: pr[1]})
	}
	// An unassigned screen shows the plain stage timer (display.go only
	// redirects to a board when one is assigned), so it previews as that,
	// never as the room's first board (BUGLOG RW36).
	bid := boardID
	if n, ok := boardNames[bid]; ok {
		v.BoardName = n
	}
	v.Template = r.Template
	if bid == 0 && r.Template != "" {
		if tl, ok := boards.TemplateLayouts()[r.Template]; ok {
			v.BoardName = templateName(r.Template)
			l := boards.NormalizeLayout(tl)
			v.Rows, v.Orientation = l.Rows, l.Orientation
			for _, w := range l.Widgets {
				v.Widgets = append(v.Widgets, screenBoxView{X: w.X, Y: w.Y, W: w.W, H: w.H, Type: w.Type})
			}
		}
	}
	if d.Store != nil && bid != 0 {
		if b, err := boards.GetBoard(d.Store.DB, showID, bid); err == nil {
			l := b.Parsed()
			v.Rows, v.Orientation = l.Rows, l.Orientation
			for _, w := range l.Widgets {
				v.Widgets = append(v.Widgets, screenBoxView{X: w.X, Y: w.Y, W: w.W, H: w.H, Type: w.Type})
			}
		}
	}
	return v
}

// matchRotation turns a screen to suit its new layout, as the capture
// dialog does: a portrait layout on an unrotated screen gets 90°, a
// landscape one on a screen turned 90°/270° goes back to 0° (BUGLOG RS19:
// a portrait template used to stretch across a landscape TV). The
// operator's own rotation choice stays: this runs only when a layout is
// picked. Phones and tablets ignore it anyway (they report their own).
func (d *Deps) matchRotation(id int64, name, orientation string) {
	cur, err := d.Store.GetScreenByName(id, name)
	if err != nil {
		return
	}
	rot := cur.Rotation
	switch {
	case orientation == "portrait" && rot == 0:
		rot = 90
	case orientation != "portrait" && (rot == 90 || rot == 270):
		rot = 0
	default:
		return
	}
	_ = d.Store.SetScreenLook(id, name, cur.Kind, rot)
}

// pushScreen applies one screen's stored config to its live tabs (theme +
// board assignment frames).
func (d *Deps) pushScreen(id int64, name string) {
	if d.Hub == nil {
		return
	}
	s, err := d.Store.GetScreenByName(id, name)
	if err != nil {
		return
	}
	// Always push the resolved theme and board: resetting a screen to the
	// event default theme or to "no layout" must reach the live TV too
	// (BUGLOG RW35; boardId 0 sends it back to the plain timer).
	var frames [][]byte
	theme := s.Theme
	if theme == "" {
		theme = d.roomTheme(id)
	}
	if b, jerr := json.Marshal(map[string]any{"t": "display", "theme": theme}); jerr == nil {
		frames = append(frames, b)
	}
	if b, jerr := json.Marshal(map[string]any{"t": "screen-board", "boardId": s.BoardID, "template": s.Template}); jerr == nil {
		frames = append(frames, b)
	}
	if b, jerr := json.Marshal(map[string]any{"t": "screen-look", "kind": s.Kind, "rotation": s.Rotation}); jerr == nil {
		frames = append(frames, b)
	}
	if len(frames) > 0 {
		d.Hub.SendToScreen(id, name, frames...)
	}
}

// notifyControls refreshes every operator panel with the new registry.
func (d *Deps) notifyControls(id int64) {
	if d.Hub == nil {
		return
	}
	screens, err := d.screensPayload(id)
	if err != nil {
		return
	}
	if b, jerr := json.Marshal(map[string]any{"t": "screens", "screens": screens}); jerr == nil {
		d.Hub.SendToRole(id, "controls", b)
	}
}

// boardKnown reports whether bid names a board of this show (assignments
// must not strand a locked TV on a 404 ?board= page).
func (d *Deps) boardKnown(showID, bid int64) bool {
	if bid <= 0 {
		return bid == 0
	}
	_, err := boards.GetBoard(d.Store.DB, showID, bid)
	return err == nil
}

// GET /api/shows/:ident/screens — the operator panel view (show-gated).
func (d *Deps) apiScreens(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	screens, err := d.screensPayload(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "screens": screens})
}

// GET /api/shows/:ident/screens/self?name=… — a display's own assignment
// (plain requireShow: not content, /d/ must stay login-free).
func (d *Deps) apiScreenSelf(c *gin.Context) {
	id, ok := d.requireShow(c)
	if !ok {
		return
	}
	name := timerpi.SanitizeScreenName(c.Query("name"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "name required"})
		return
	}
	s, err := d.Store.GetScreenByName(id, name)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": true, "known": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "known": true, "theme": s.Theme, "boardId": s.BoardID, "kind": s.Kind, "rotation": s.Rotation})
}

// POST /api/shows/:ident/screens/config {name,theme,boardId} — assign one
// screen (empty theme / 0 board = follow defaults). Persisted + pushed.
func (d *Deps) apiScreenConfig(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	var body struct {
		Name     string  `json:"name"`
		Theme    string  `json:"theme"`
		BoardID  int64   `json:"boardId"`
		Room     string  `json:"room"`
		Kind     *string `json:"kind"`
		Rotation *int    `json:"rotation"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "body must be {name, theme?, boardId?, kind?, rotation?}"})
		return
	}
	name := timerpi.SanitizeScreenName(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad screen name"})
		return
	}
	if !screenThemeRe.MatchString(body.Theme) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad theme name"})
		return
	}
	if body.BoardID < 0 || !d.boardKnown(id, body.BoardID) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unknown board"})
		return
	}
	// Check every field before writing any (BUGLOG RW42): a bad rotation
	// used to answer 400 with the theme/board/room already saved.
	look := body.Kind != nil || body.Rotation != nil
	var kind string
	var rot int
	if look {
		cur, _ := d.Store.GetScreenByName(id, name)
		kind, rot = cur.Kind, cur.Rotation
		if body.Kind != nil {
			kind = *body.Kind
		}
		if body.Rotation != nil {
			rot = *body.Rotation
		}
		if !timerpi.ValidScreenKind(kind) {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unknown display type " + strconv.Quote(kind)})
			return
		}
		if !timerpi.ValidRotation(rot) {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "rotation must be 0, 90, 180 or 270"})
			return
		}
	}
	if err := d.Store.SetScreenConfig(id, name, body.Theme, body.BoardID, body.Room); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if look {
		if err := d.Store.SetScreenLook(id, name, kind, rot); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": strings.TrimPrefix(err.Error(), "timerpi: ")})
			return
		}
	}
	if body.Rotation == nil && body.BoardID > 0 {
		if b, err := boards.GetBoard(d.Store.DB, id, body.BoardID); err == nil {
			d.matchRotation(id, name, b.Parsed().Orientation)
		}
	}
	d.pushScreen(id, name)
	d.notifyControls(id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/shows/:ident/screens/match {from} — copy one screen's config
// onto EVERY registered screen (the "make them all look like this one"
// button) and push it live.
func (d *Deps) apiScreenMatch(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	var body struct {
		From string `json:"from"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "body must be {from}"})
		return
	}
	from := timerpi.SanitizeScreenName(body.From)
	src, err := d.Store.GetScreenByName(id, from)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown screen"})
		return
	}
	rows, err := d.Store.ListScreens(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	// Match copies the look: theme, layout (a board or a built-in) and
	// display type. Room and rotation belong to where each screen is and
	// how it is mounted, so they stay (BUGLOG RW41).
	matched, failed := 0, 0
	for _, r := range rows {
		if r.Name == from {
			continue
		}
		err := d.Store.SetScreenConfig(id, r.Name, src.Theme, src.BoardID, r.Room)
		if err == nil && src.BoardID == 0 {
			err = d.Store.SetScreenTemplate(id, r.Name, src.Template)
		}
		if err == nil {
			err = d.Store.SetScreenLook(id, r.Name, src.Kind, r.Rotation)
		}
		if err != nil {
			failed++
			continue
		}
		d.pushScreen(id, r.Name)
		matched++
	}
	d.notifyControls(id)
	c.JSON(http.StatusOK, gin.H{"ok": true, "matched": matched, "failed": failed})
}

// POST /api/shows/:ident/screens/rename {from,to} — move the registry row
// and tell the live tab to adopt the new identity (it persists + rejoins).
func (d *Deps) apiScreenRename(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "body must be {from,to}"})
		return
	}
	to := timerpi.SanitizeScreenName(body.To)
	if to == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad screen name"})
		return
	}
	if err := d.Store.RenameScreen(id, body.From, to); err != nil {
		if errors.Is(err, timerpi.ErrScreenNameTaken) {
			c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "Another screen is already called " + to})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if d.Hub != nil {
		if b, jerr := json.Marshal(map[string]any{"t": "screen-rename", "name": to}); jerr == nil {
			d.Hub.SendToScreen(id, timerpi.SanitizeScreenName(body.From), b)
		}
	}
	d.notifyControls(id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/shows/:ident/screens/forget {name} — drop a screen from the
// registry (a still-open tab simply re-registers on its next join).
// POST /api/shows/:ident/screens/link {name} → {link}: the screen's own
// URL with its key (BUGLOG RW9), for opening a screen by hand (a kiosk's
// start page, a TV browser bookmark) instead of capturing it. Moderators
// only; the key is created on first use.
func (d *Deps) apiScreenLink(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "body must be {name}"})
		return
	}
	name := timerpi.SanitizeScreenName(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad screen name"})
		return
	}
	sh, err := d.Store.GetShow(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "no such room"})
		return
	}
	key, err := d.Store.ScreenKey(id, name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	link := fmt.Sprintf("%s/d/%s?screen=%s&key=%s", requestOrigin(c), sh.Code, url.QueryEscape(name), key)
	c.JSON(http.StatusOK, gin.H{"ok": true, "link": link})
}

func (d *Deps) apiScreenForget(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "body must be {name}"})
		return
	}
	name := timerpi.SanitizeScreenName(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad screen name"})
		return
	}
	if err := d.Store.DeleteScreen(id, name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	// A screen still open is released too: its tabs go back to the ready
	// screen (waiting to be captured) instead of re-appearing here as a
	// blank card, which made Forget look like a no-op (BUGLOG RS17).
	if d.Hub != nil {
		for _, pr := range d.Hub.ScreenPeers(id)[name] {
			d.Hub.KickSession(id, pr[0])
		}
	}
	d.notifyControls(id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------------------------------------------------------------------
// Presets (F2). A preset bundles the whole screen configuration:
// {"screens":[{"name":…,"theme":…,"boardId":…}]}. It lives in SQLite (so
// it survives server restarts and any browser sees it after a refresh),
// and every preset exports as a plain JSON file that imports back.

type presetData struct {
	Screens []struct {
		Name    string `json:"name"`
		Theme   string `json:"theme"`
		BoardID int64  `json:"boardId"`
	} `json:"screens"`
}

const presetFileKind = "timerpi-display-preset"
const presetFileVersion = 1

func (d *Deps) presetSnapshotData(id int64) (string, error) {
	rows, err := d.Store.ListScreens(id)
	if err != nil {
		return "", err
	}
	var pd presetData
	for _, r := range rows {
		el := struct {
			Name    string `json:"name"`
			Theme   string `json:"theme"`
			BoardID int64  `json:"boardId"`
		}{Name: r.Name, Theme: r.Theme, BoardID: r.BoardID}
		pd.Screens = append(pd.Screens, el)
	}
	b, jerr := json.Marshal(pd)
	return string(b), jerr
}

// GET /api/shows/:ident/presets — list (id, name, updatedAt, parsed data).
func (d *Deps) apiPresetsList(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	rows, err := d.Store.ListPresets(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, p := range rows {
		var pd presetData
		_ = json.Unmarshal([]byte(p.Data), &pd)
		out = append(out, gin.H{"id": p.ID, "name": p.Name, "updatedAt": p.UpdatedAt, "data": pd})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "presets": out})
}

// POST /api/shows/:ident/presets {name, data?} — save. Without data the
// CURRENT registry is snapshotted (the "Save current" button).
func (d *Deps) apiPresetsSave(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	var body struct {
		Name string          `json:"name"`
		Data json.RawMessage `json:"data"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "body must be {name, data?}"})
		return
	}
	name := timerpi.ClipUTF8(body.Name, 80)
	data := string(body.Data)
	if data == "" {
		snap, serr := d.presetSnapshotData(id)
		if serr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": serr.Error()})
			return
		}
		data = snap
	}
	p, err := d.Store.SavePreset(id, name, data)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.logAction(id, "presetSave", name)
	c.JSON(http.StatusCreated, gin.H{"ok": true, "id": p.ID, "name": p.Name})
}

// POST /api/shows/:ident/presets/:pid/apply — write the preset onto every
// screen it names (registry + live pushes).
func (d *Deps) apiPresetApply(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	pid, perr := strconv.ParseInt(c.Param("pid"), 10, 64)
	if perr != nil || pid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad preset id"})
		return
	}
	p, err := d.Store.GetPreset(id, pid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown preset"})
		return
	}
	var pd presetData
	if jerr := json.Unmarshal([]byte(p.Data), &pd); jerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "preset data corrupt"})
		return
	}
	applied := 0
	for _, sc := range pd.Screens {
		name := timerpi.SanitizeScreenName(sc.Name)
		if name == "" || !screenThemeRe.MatchString(sc.Theme) ||
			sc.BoardID < 0 || !d.boardKnown(id, sc.BoardID) {
			continue // entries from another show's file never land here
		}
		// A preset carries looks, not rooms: keep each screen's room
		// (BUGLOG RW41; it used to be wiped to "").
		room := ""
		if cur, cerr := d.Store.GetScreenByName(id, name); cerr == nil {
			room = cur.Room
		}
		if err := d.Store.SetScreenConfig(id, name, sc.Theme, sc.BoardID, room); err != nil {
			continue
		}
		d.pushScreen(id, name)
		applied++
	}
	d.notifyControls(id)
	d.logAction(id, "presetApply", p.Name)
	c.JSON(http.StatusOK, gin.H{"ok": true, "screens": applied})
}

// DELETE /api/shows/:ident/presets/:pid — drop a preset.
func (d *Deps) apiPresetDelete(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	pid, perr := strconv.ParseInt(c.Param("pid"), 10, 64)
	if perr != nil || pid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad preset id"})
		return
	}
	if err := d.Store.DeletePreset(id, pid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /api/shows/:ident/presets/:pid/export — download the preset as a
// JSON file (the same shape apiPresetImport accepts).
func (d *Deps) apiPresetExport(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	pid, perr := strconv.ParseInt(c.Param("pid"), 10, 64)
	if perr != nil || pid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad preset id"})
		return
	}
	p, err := d.Store.GetPreset(id, pid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown preset"})
		return
	}
	file := gin.H{
		"kind":    presetFileKind,
		"version": presetFileVersion,
		"name":    p.Name,
		"data":    json.RawMessage(p.Data),
	}
	b, jerr := json.MarshalIndent(file, "", "  ")
	if jerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": jerr.Error()})
		return
	}
	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, strings.ToLower(p.Name))
	slug = strings.Trim(strings.ReplaceAll(slug, "--", "-"), "-")
	if slug == "" {
		slug = "preset"
	}
	c.Header("Content-Disposition", `attachment; filename="timerpi-`+slug+`.json"`)
	c.Data(http.StatusOK, "application/json; charset=utf-8", b)
}

// POST /api/shows/:ident/presets/import — upload an exported JSON preset
// (raw body or multipart field "file"); stored under its own name.
func (d *Deps) apiPresetImport(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	raw, ferr := bundleBody(c)
	if ferr != "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": ferr})
		return
	}
	var file struct {
		Kind    string          `json:"kind"`
		Version int             `json:"version"`
		Name    string          `json:"name"`
		Data    json.RawMessage `json:"data"`
	}
	if jerr := json.Unmarshal(raw, &file); jerr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "not a TimerPi preset file"})
		return
	}
	if file.Kind != presetFileKind || file.Version != presetFileVersion {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false,
			"error": "not a TimerPi display preset (or unsupported version)"})
		return
	}
	var pd presetData
	if jerr := json.Unmarshal(file.Data, &pd); jerr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "preset data invalid"})
		return
	}
	name := file.Name
	if name == "" {
		name = "Imported preset"
	}
	p, err := d.Store.SavePreset(id, name, string(file.Data))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.logAction(id, "presetImport", p.Name)
	c.JSON(http.StatusCreated, gin.H{"ok": true, "id": p.ID, "name": p.Name})
}

// templateKey normalises a built-in key; ok=false when unknown.
func templateKey(raw string) (string, bool) {
	k := strings.ToLower(strings.TrimSpace(raw))
	_, ok := boards.TemplateLayouts()[k]
	return k, ok
}

// templateName is a built-in's display name, "[built-in] Room walk-in".
func templateName(key string) string {
	for _, t := range boards.Templates() {
		if t.Key == key {
			return "[built-in] " + t.Name
		}
	}
	return "[built-in] " + key
}

// POST /api/shows/:ident/screens/template {name, template} — the screen
// shows a built-in layout directly ("" = plain timer). Built-ins are never
// copied or edited here: "Edit layout" makes a named event layout from one
// (POST …/layouts/copy, STATUS U11). A screen with no display type yet
// takes the template's.
func (d *Deps) apiScreenTemplate(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	var body struct {
		Name     string `json:"name"`
		Template string `json:"template"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "body must be {name, template}"})
		return
	}
	name := timerpi.SanitizeScreenName(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad screen name"})
		return
	}
	key := ""
	if strings.TrimSpace(body.Template) != "" {
		k, known := templateKey(body.Template)
		if !known {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unknown template"})
			return
		}
		key = k
	}
	if err := d.Store.SetScreenTemplate(id, name, key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if cur, err := d.Store.GetScreenByName(id, name); err == nil && cur.Kind == "" {
		for _, t := range boards.Templates() {
			if t.Key == key {
				_ = d.Store.SetScreenLook(id, name, t.Kind, cur.Rotation)
			}
		}
	}
	if tl, ok := boards.TemplateLayouts()[key]; ok {
		d.matchRotation(id, name, boards.NormalizeLayout(tl).Orientation)
	}
	d.pushScreen(id, name)
	d.notifyControls(id)
	c.JSON(http.StatusOK, gin.H{"ok": true, "template": key})
}
