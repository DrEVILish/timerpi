// Package routes — showfile.go: the whole-room show file (.timerpi.json).
//
//	GET  /api/shows/:ident/file          export one room (moderator)
//	POST /api/events/:code/rooms/import  import a room into an event (events.go)
package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/boards"
	"timerpi/buildinfo"
	"timerpi/timerpi"
)

// RegisterSetup mounts the whole-room show file export (moderator); import
// lives on the event (POST /api/events/:code/rooms/import, SuperOperator).
func RegisterSetup(r gin.IRouter, d *Deps) {
	if d == nil {
		return
	}
	r.GET("/api/shows/:ident/file", d.apiShowFile)
}

// ------------------------------------------------------ whole-day show file --

// .timerpi.json bundle versions: v2 is what we export, v1 still imports.
const (
	showFileVersionV1 = 1 // cues+messages+schedule
	showFileVersionV2 = 2 // §11.9 full fidelity (polls/votes/screens/boards/presets/zone+map)
)

// appVersion is the product version string (PLAN v2 "Rooms"; §11.3 phase 7).
func appVersion() string { return buildinfo.Version }

// showFile is the whole-day export/import bundle — portable fields ONLY.
// Cue/message DB ids and show ids are dropped on the wire (re-created on
// import; stable Pos is the run order). The playhead/held state is
// intentionally non-portable: a day moving between TimerPis lands ARMED.
// Rate + DayStartTS travel in schedule (the operator's rehearsal pacing and
// day-bar anchor carry over; shownAt survives for messages).
type showFile struct {
	ManifestVersion int               `json:"manifestVersion"`
	ExportedAt      int64             `json:"exportedAt"`
	Show            showFileTitle     `json:"show"`
	Cues            []timerpi.Cue     `json:"cues"`
	Messages        []timerpi.Message `json:"messages"`
	Schedule        showFileSchedule  `json:"schedule"`
	// §11.9 full fidelity (v2, ZIP): the whole event rides the bundle.
	// MapIndex is the event's venue map (→ Assets[i-1]). Zone and
	// ZoneMapIndex are read from pre-event bundles only, never written
	// (STATUS C10): their map becomes the event map when it has none.
	MapIndex     int64              `json:"mapIndex,omitempty"`
	Zone         string             `json:"zone,omitempty"`
	ZoneMapIndex int64              `json:"zoneMapIndex,omitempty"`
	Polls        []showFilePoll     `json:"polls,omitempty"`
	Votes        []showFileVote     `json:"votes,omitempty"`
	Screens      []showFileScreen   `json:"screens,omitempty"`
	Boards       []showFileBoard    `json:"boards,omitempty"`
	Presets      []showFilePreset   `json:"presets,omitempty"`
	Assets       []showFileAssetRef `json:"assets,omitempty"`
	Version      string             `json:"generator,omitempty"`
}

// showFilePoll — one interaction item, wire-shaped for the bundle
// (Poll's db tags hide Options/Parent/Author from JSON on purpose).
type showFilePoll struct {
	ID       int64  `json:"id"` // export-local 1..N
	Kind     string `json:"kind"`
	Question string `json:"question"`
	Options  string `json:"options,omitempty"`
	Correct  int64  `json:"correct"`
	State    string `json:"state"`
	Parent   int64  `json:"parent"` // export-local
	Author   string `json:"author,omitempty"`
	Ts       int64  `json:"ts,omitempty"`
	// Push targets, Q&A spotlight (export-local child id) and auto-approve.
	ToAudience  bool  `json:"toAudience,omitempty"`
	ToPresenter bool  `json:"toPresenter,omitempty"`
	Spot        int64 `json:"spot,omitempty"`
	AutoApprove bool  `json:"autoApprove,omitempty"`
}

// showFileVote — one vote row keyed by poll export id (§11.9).
type showFileVote struct {
	PollID int64  `json:"pollId"`
	Peer   string `json:"peer"`
	Choice string `json:"choice"`
	Ts     int64  `json:"ts"`
}

// showFileScreen / Board / Preset / AssetRef — registry + layout payloads.
type showFileScreen struct {
	Name    string `json:"name"`
	Theme   string `json:"theme"`
	Room    string `json:"room"`
	BoardID int64  `json:"boardId"`
}

type showFileBoard struct {
	ID     int64           `json:"id"` // export-local id (screens reference it)
	Name   string          `json:"name"`
	Layout json.RawMessage `json:"layout"`
}

type showFilePreset struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

type showFileAssetRef struct {
	ID   int64  `json:"id"` // export-local 1..N
	Name string `json:"name"`
	Mime string `json:"mime"`
	Data string `json:"data"` // data URL (mime;base64) — ≤4 MiB by upload cap
}

type showFileTitle struct {
	Title string `json:"title"`
}

type showFileSchedule struct {
	DayStartTS int64   `json:"dayStartTS"`
	Rate       float64 `json:"rate,omitempty"`
}

// apiShowFile is GET /api/shows/:ident/file — dump the whole day as
// .timerpi.json. READS ONLY: exported store funcs (GetShow/ListCues/
// ListMessages/LoadRuntime) + the engine snapshot's show title — no db.go
// edits (gap note: cues/messages written straight from the store are the
// full rows; the PROTOCOL snapshot shape lacks message shownAt for hidden
// messages, so the store listing is what round-trips them — see
// (an old handoff note, not kept)).
func (d *Deps) apiShowFile(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	show, gerr := d.Store.GetShow(id)
	if gerr != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such show"})
		return
	}
	cues, cerr := d.Store.ListCues(id)
	if cerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": cerr.Error()})
		return
	}
	// An export that silently drops the messages or the day's schedule is
	// worse than a clear failure (BUGLOG RS25).
	msgs, merr := d.Store.ListMessages(id)
	if merr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": merr.Error()})
		return
	}
	rt, _, rerr := d.Store.LoadRuntime(id)
	if rerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": rerr.Error()})
		return
	}

	out := make([]timerpi.Cue, 0, len(cues))
	for _, cue := range cues {
		cue.ID = 0     // re-numbered on import
		cue.ShowID = 0 // re-bound to the new show
		out = append(out, cue)
	}
	msgOut := make([]timerpi.Message, 0, len(msgs))
	for _, m := range msgs {
		m.ShowID = 0
		msgOut = append(msgOut, m)
	}
	sched := showFileSchedule{DayStartTS: rt.DayStartTS}
	if rt.Rate > 0 {
		sched.Rate = rt.Rate
	}
	// §11.9: the whole event rides the bundle — polls (with moderation
	// state), their votes, the screens registry, boards, presets, the zone
	// label and its map asset (data-URL'd images; ≤4 MiB each by the
	// upload cap, so JSON stays honest) plus the generator version.
	sf := showFile{
		ManifestVersion: showFileVersionV2,
		ExportedAt:      time.Now().UnixMilli(),
		Show:            showFileTitle{Title: show.Title},
		Cues:            out,
		Messages:        msgOut,
		Schedule:        sched,
		Version:         appVersion(),
	}
	d.fillBundleExtras(id, &sf)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`,
		exportName(show.Title)+".timerpi.json"))
	c.JSON(http.StatusOK, sf)
}

// bundleBody reads the bundle bytes (multipart "file" preferred, JSON body
// fallback for scripts).
func bundleBody(c *gin.Context) ([]byte, string) {
	if fh, ferr := c.FormFile("file"); ferr == nil {
		f, oerr := fh.Open()
		if oerr != nil {
			return nil, "file unreadable"
		}
		defer f.Close()
		raw, rerr := io.ReadAll(f)
		if rerr != nil {
			return nil, "file unreadable"
		}
		return raw, ""
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil, `send the bundle as multipart field "file" (or an application/json body)`
	}
	return raw, ""
}

// importShowFile parses a bundle and creates the show (shared by
// /api/shows/import-file and the wizard's file drop). Exported store funcs
// ONLY: CreateShow / ReplaceCues / CreateMessage / ShowMessage / SaveRuntime
// / TouchShow.
func (d *Deps) importShowFile(raw []byte, fallbackTitle string, eventID int64) (timerpi.Show, int, error) {
	var sf showFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return timerpi.Show{}, 0, fmt.Errorf("not a TimerPi show file: %v", err)
	}
	if sf.ManifestVersion != showFileVersionV1 && sf.ManifestVersion != showFileVersionV2 {
		return timerpi.Show{}, 0, fmt.Errorf(
			"unsupported show file version %d (want %d or %d)", sf.ManifestVersion, showFileVersionV1, showFileVersionV2)
	}

	title := strings.TrimSpace(sf.Show.Title)
	if title == "" {
		title = strings.TrimSpace(fallbackTitle)
	}
	if title == "" {
		title = "Imported show"
	}
	show, err := d.Store.CreateRoom(eventID, title)
	if err != nil {
		return timerpi.Show{}, 0, err
	}

	// §11.9: the event's other halves ride v2 bundles — zone + map asset,
	// interaction items with moderation state, votes, screens (re-keyed to
	// imported boards), presets. v1 bundles simply lack the sections.
	v2 := sf.ManifestVersion == showFileVersionV2
	rc := roomContent{
		Cues: sf.Cues, Messages: sf.Messages,
		// Schedule anchor + rate restore as an ARMED day: no playhead travels.
		Runtime: timerpi.Runtime{Rate: sf.Schedule.Rate, DayStartTS: sf.Schedule.DayStartTS},
	}
	boardX := map[int64]int64{}
	if v2 {
		for _, b := range sf.Boards {
			layout := string(b.Layout)
			if layout == "" || layout == "null" {
				continue
			}
			if nb, berr := boards.CreateBoard(d.Store.DB, show.ID, b.Name, layout); berr == nil {
				boardX[b.ID] = nb.ID
			}
		}
		rc.Polls, rc.Votes, rc.Presets = sf.Polls, sf.Votes, sf.Presets
		for _, r := range sf.Screens {
			rc.Screens = append(rc.Screens, eventBundleScreen{Name: r.Name, Theme: r.Theme, Room: r.Room, BoardID: r.BoardID})
		}
	}
	if err := d.restoreRoom(show.ID, rc, boardX); err != nil {
		// No empty room left behind in the event (BUGLOG RW31).
		_ = d.Store.DeleteShow(show.ID)
		return timerpi.Show{}, 0, err
	}
	if err := d.Store.TouchShow(show.ID); err != nil {
		log.Printf("routes: import show stamp: %v", err)
	}

	if v2 {
		assetX := map[int64]int64{}
		for _, a := range sf.Assets {
			data, derr := dataURLBytes(a.Data)
			if derr != nil || len(data) == 0 {
				continue
			}
			mime, ok := sniffImage(data) // the bundle's declared mime is never trusted
			if !ok {
				continue
			}
			if na, aerr := d.Store.CreateAsset(eventID, a.Name, mime, data); aerr == nil {
				assetX[a.ID] = na.ID
			}
		}
		// The venue map belongs to the event: a bundle's map (or a
		// pre-event bundle's zone map) fills it only when it has none.
		idx := sf.MapIndex
		if idx == 0 {
			idx = sf.ZoneMapIndex
		}
		if aid := assetX[idx]; idx > 0 && aid > 0 {
			if ev, eerr := d.Store.GetEvent(show.EventID); eerr == nil && ev.MapAsset == 0 {
				if err := d.Store.SetEventMap(ev.ID, aid); err != nil {
					log.Printf("routes: import venue map: %v", err)
				}
			}
		}
	}

	d.notifyShow(show.ID)
	return show, len(sf.Cues), nil
}

// restoreBundlePolls recreates a room's interactions (parents before
// children, as exported), the spotlight and the votes.
func (d *Deps) restoreBundlePolls(showID int64, polls []showFilePoll, votes []showFileVote) {
	// Polls: export ids are the bundle's list positions (1..N, parents
	// before children), so each child's parent re-keys inline as it
	// lands; votes ride the same mapping afterwards.
	pollX := map[int64]int64{}
	for slot, p := range polls {
		parent := int64(0)
		if p.Parent != 0 {
			parent = pollX[p.Parent] // parents precede children
		}
		np, cerr := d.Store.CreatePollRaw(timerpi.Poll{
			ShowID: showID, Kind: p.Kind, Question: p.Question,
			Options: p.Options, Correct: p.Correct, State: p.State,
			Parent: parent, Author: p.Author, Ts: p.Ts,
			ToAudience: p.ToAudience, ToPresenter: p.ToPresenter, AutoApprove: p.AutoApprove,
		})
		if cerr == nil {
			pollX[int64(slot+1)] = np.ID
		}
	}
	for slot, p := range polls {
		if p.Spot > 0 && pollX[p.Spot] > 0 {
			if err := d.Store.SetPollSpotRaw(pollX[int64(slot+1)], pollX[p.Spot]); err != nil {
				log.Printf("routes: import spotlight: %v", err)
			}
		}
	}
	if d.Hub != nil {
		d.Hub.BroadcastPoll(showID)
	}
	d.restoreBundleVotes(pollX, votes)
}
