// eventbundle.go — the whole event as one copy (VENUE-CLOUD §4, §6). Used
// box-to-box and box-to-cloud, never offered as a download: it carries the
// password hashes and the screens' keys so a copy works like the original.
//
//   - A box paired from the cloud pulls its event and becomes its home.
//   - Member boxes mirror the primary, so a takeover has the event.
//   - The primary streams its copy to the cloud (the venue's copy wins).
//
// Codes are kept (the audience QR codes must work on both sides), and an
// existing room keeps its id (live sessions are keyed by it).
package routes

import (
	"encoding/json"
	"fmt"
	"time"

	"timerpi/boards"
	"timerpi/buildinfo"
	"timerpi/timerpi"
)

const eventBundleVersion = 1

type eventBundle struct {
	Version    int                `json:"version"`
	ExportedAt int64              `json:"exportedAt"`
	Generator  string             `json:"generator"`
	Event      eventBundleEvent   `json:"event"`
	Boards     []eventBundleBoard `json:"boards,omitempty"`
	Map        *showFileAssetRef  `json:"map,omitempty"`
	Rooms      []eventBundleRoom  `json:"rooms"`
}

type eventBundleEvent struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	SuperHash string `json:"superHash"`
	Theme     string `json:"theme"`
	EndsAt    int64  `json:"endsAt"`
}

type eventBundleBoard struct {
	ID     int64           `json:"id"`   // bundle-local (screens reference it)
	Room   string          `json:"room"` // owning room's code
	Name   string          `json:"name"`
	Layout json.RawMessage `json:"layout"`
}

type eventBundleRoom struct {
	Code     string              `json:"code"`
	Title    string              `json:"title"`
	Pos      int64               `json:"pos"`
	RoomPW   string              `json:"roomPW"` // the stored hash
	Notes    string              `json:"notes"`
	DayStart string              `json:"dayStart"`
	Runtime  timerpi.Runtime     `json:"runtime"`
	Cues     []timerpi.Cue       `json:"cues"`
	Messages []timerpi.Message   `json:"messages"`
	Polls    []showFilePoll      `json:"polls,omitempty"`
	Votes    []showFileVote      `json:"votes,omitempty"`
	Screens  []eventBundleScreen `json:"screens,omitempty"`
	Presets  []showFilePreset    `json:"presets,omitempty"`
}

type eventBundleScreen struct {
	Name     string `json:"name"`
	Theme    string `json:"theme"`
	Room     string `json:"room"`
	BoardID  int64  `json:"boardId"`
	Kind     string `json:"kind"`
	Rotation int    `json:"rotation"`
	Template string `json:"template"`
	Key      string `json:"key"`
}

// ExportEvent builds the event's copy.
func (d *Deps) ExportEvent(eventID int64) ([]byte, error) {
	ev, err := d.Store.GetEvent(eventID)
	if err != nil {
		return nil, err
	}
	rooms, err := d.Store.ListRooms(eventID)
	if err != nil {
		return nil, err
	}
	b := eventBundle{
		Version: eventBundleVersion, ExportedAt: time.Now().UnixMilli(), Generator: buildinfo.Version,
		Event: eventBundleEvent{Code: ev.Code, Name: ev.Name, SuperHash: ev.SuperHash, Theme: ev.Theme, EndsAt: ev.EndsAt},
		Rooms: []eventBundleRoom{},
	}
	if ev.MapAsset > 0 {
		if a, aerr := d.Store.GetAsset(ev.MapAsset); aerr == nil {
			b.Map = &showFileAssetRef{ID: 1, Name: a.Name, Mime: a.Mime, Data: dataURL(a)}
		}
	}
	roomCode := map[int64]string{}
	for _, r := range rooms {
		roomCode[r.ID] = r.Code
	}
	var boardX map[int64]int64
	for i, r := range rooms {
		room := eventBundleRoom{Code: r.Code, Title: r.Title, Pos: r.RoomPos, RoomPW: r.RoomPW, Notes: r.Notes, DayStart: r.DayStart}
		if room.Cues, err = d.Store.ListCues(r.ID); err != nil {
			return nil, err
		}
		if room.Messages, err = d.Store.ListMessages(r.ID); err != nil {
			return nil, err
		}
		if rt, _, rerr := d.Store.LoadRuntime(r.ID); rerr == nil {
			room.Runtime = rt
		}
		room.Polls, room.Votes = d.bundlePolls(r.ID)
		bs, bx, screens, presets := d.exportRoomLayout(r.ID)
		if i == 0 { // boards belong to the event: one list, keyed once
			boardX = bx
			for _, bd := range bs {
				b.Boards = append(b.Boards, eventBundleBoard{ID: boardX[bd.ID], Room: roomCode[bd.ShowID], Name: bd.Name, Layout: json.RawMessage(bd.LayoutJSON())})
			}
		}
		keys, _ := d.Store.ScreenKeys(r.ID)
		for _, s := range screens {
			room.Screens = append(room.Screens, eventBundleScreen{Name: s.Name, Theme: s.Theme, Room: s.Room, BoardID: boardX[s.BoardID],
				Kind: s.Kind, Rotation: s.Rotation, Template: s.Template, Key: keys[s.Name]})
		}
		room.Presets = presets
		b.Rooms = append(b.Rooms, room)
	}
	return json.Marshal(b)
}

// ImportEvent makes this box's (or the cloud's) copy of the event match the
// bundle: the event is created under its code if new, rooms are matched by
// code (kept ids), refilled, added or removed.
func (d *Deps) ImportEvent(raw []byte) (timerpi.Event, error) {
	var b eventBundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return timerpi.Event{}, fmt.Errorf("not an event copy: %v", err)
	}
	if b.Version != eventBundleVersion {
		return timerpi.Event{}, fmt.Errorf("unsupported event copy version %d", b.Version)
	}
	ev, ok := d.Store.ResolveEvent(b.Event.Code)
	if !ok {
		var err error
		if ev, err = d.Store.CreateEventWithCode(b.Event.Code, b.Event.Name, b.Event.SuperHash); err != nil {
			return timerpi.Event{}, err
		}
	}
	if err := d.Store.UpdateEventCopy(ev.ID, b.Event.Name, b.Event.SuperHash, b.Event.Theme, b.Event.EndsAt); err != nil {
		return timerpi.Event{}, err
	}
	existing, err := d.Store.ListRooms(ev.ID)
	if err != nil {
		return timerpi.Event{}, err
	}
	byCode := map[string]timerpi.Show{}
	for _, r := range existing {
		byCode[r.Code] = r
	}
	if err := d.Store.ClearEventBoards(ev.ID); err != nil {
		return timerpi.Event{}, err
	}
	ids := map[string]int64{}
	keep := map[int64]bool{}
	for _, r := range b.Rooms {
		sh, ok := byCode[timerpi.NormalizeCode(r.Code)]
		if !ok {
			if sh, err = d.Store.CreateRoomWithCode(ev.ID, r.Code, r.Title); err != nil {
				return timerpi.Event{}, err
			}
		}
		ids[sh.Code] = sh.ID
		keep[sh.ID] = true
	}
	for _, r := range existing {
		if !keep[r.ID] {
			if err := d.Store.DeleteShow(r.ID); err != nil {
				return timerpi.Event{}, err
			}
			d.forgetRoom(r.ID)
		}
	}
	boardX := map[int64]int64{}
	for _, bd := range b.Boards {
		owner := ids[timerpi.NormalizeCode(bd.Room)]
		if owner == 0 && len(b.Rooms) > 0 {
			owner = ids[timerpi.NormalizeCode(b.Rooms[0].Code)]
		}
		if nb, berr := boards.CreateBoard(d.Store.DB, owner, bd.Name, string(bd.Layout)); berr == nil {
			boardX[bd.ID] = nb.ID
		}
	}
	for _, r := range b.Rooms {
		id := ids[timerpi.NormalizeCode(r.Code)]
		if err := d.fillRoomCopy(id, r, boardX); err != nil {
			return timerpi.Event{}, fmt.Errorf("room %s: %w", r.Code, err)
		}
	}
	if b.Map != nil {
		if data, derr := dataURLBytes(b.Map.Data); derr == nil {
			if mime, ok := sniffImage(data); ok {
				if cur, cerr := d.Store.GetAsset(ev.MapAsset); cerr != nil || string(cur.Bytes) != string(data) {
					if na, aerr := d.Store.CreateAsset(ev.ID, b.Map.Name, mime, data); aerr == nil {
						_ = d.Store.SetEventMap(ev.ID, na.ID)
					}
				}
			}
		}
	}
	return d.Store.GetEvent(ev.ID)
}

// fillRoomCopy replaces one room's content with the copy's.
func (d *Deps) fillRoomCopy(id int64, r eventBundleRoom, boardX map[int64]int64) error {
	if err := d.Store.UpdateRoomCopy(id, r.Title, r.Pos, r.RoomPW, r.Notes, r.DayStart); err != nil {
		return err
	}
	if err := d.Store.ClearRoomContent(id); err != nil {
		return err
	}
	rc := roomContent{Cues: r.Cues, Messages: r.Messages, Polls: r.Polls, Votes: r.Votes,
		Screens: r.Screens, Presets: r.Presets, Runtime: r.Runtime}
	if err := d.restoreRoom(id, rc, boardX); err != nil {
		return err
	}
	// The cached engine still holds the old running order: reload it, and
	// tell connected pages.
	if d.Engines != nil {
		d.Engines.Drop(id)
	}
	d.notifyShow(id)
	if d.Hub != nil {
		d.Hub.BroadcastPoll(id)
	}
	return nil
}
