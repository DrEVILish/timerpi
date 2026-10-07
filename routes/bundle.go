// bundle.go — the §11.9 full-fidelity show bundle: what v2 exports beyond
// the v1 cues/messages/schedule (polls with moderation state, their votes,
// screens, boards, presets, zone + map asset) and how it re-keys on import.
package routes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"timerpi/boards"
	"timerpi/timerpi"
)

// fillBundleExtras loads the §11.9 sections of a v2 export. Export ids are
// the bundle's LIST POSITIONS (1..N, parents before children — the order
// bundlePolls emits), so the importer can re-key without any id table.
func (d *Deps) fillBundleExtras(id int64, sf *showFile) {
	sf.Polls, sf.Votes = d.bundlePolls(id)
	bs, boardX, screens, presets := d.exportRoomLayout(id)
	for _, b := range bs {
		sf.Boards = append(sf.Boards, showFileBoard{ID: boardX[b.ID], Name: b.Name, Layout: json.RawMessage(b.LayoutJSON())})
	}
	for _, r := range screens {
		sf.Screens = append(sf.Screens, showFileScreen{Name: r.Name, Theme: r.Theme, Room: r.Room, BoardID: boardX[r.BoardID]})
	}
	sf.Presets = presets
	// The event's venue map rides along (STATUS C10: no more zone maps).
	if sh, serr := d.Store.GetShow(id); serr == nil {
		if ev, eerr := d.Store.GetEvent(sh.EventID); eerr == nil && ev.MapAsset > 0 {
			if a, aerr := d.Store.GetAsset(ev.MapAsset); aerr == nil {
				sf.Assets = append(sf.Assets, showFileAssetRef{
					ID: int64(len(sf.Assets) + 1), Name: a.Name, Mime: a.Mime, Data: dataURL(a),
				})
				sf.MapIndex = int64(len(sf.Assets))
			}
		}
	}
}

// exportRoomLayout lists the event's boards (boardX: live id → export id
// 1..N) plus one room's screens and presets, for the show file and the
// event copy.
func (d *Deps) exportRoomLayout(id int64) (bs []boards.Board, boardX map[int64]int64, screens []timerpi.Screen, presets []showFilePreset) {
	boardX = map[int64]int64{}
	if list, err := boards.ListBoards(d.Store.DB, id); err == nil {
		bs = list
		for i, b := range list {
			boardX[b.ID] = int64(i + 1)
		}
	}
	screens, _ = d.Store.ListScreens(id)
	if ps, err := d.Store.ListPresets(id); err == nil {
		for _, p := range ps {
			presets = append(presets, showFilePreset{Name: p.Name, Data: json.RawMessage(p.Data)})
		}
	}
	return bs, boardX, screens, presets
}

// bundlePolls exports a room's interactions and votes. Export ids are the
// list positions (1..N, parents before children), so an importer re-keys
// without an id table. ListPolls order is updated-relative and would
// interleave them, hence two passes.
func (d *Deps) bundlePolls(id int64) (out []showFilePoll, votes []showFileVote) {
	polls, err := d.Store.ListPolls(id)
	if err != nil {
		return nil, nil
	}
	pollX := map[int64]int64{} // live poll id → export id
	for _, kids := range []bool{false, true} {
		for _, p := range polls {
			if (p.Parent != 0) == kids {
				pollX[p.ID] = int64(len(pollX) + 1)
			}
		}
	}
	for _, kids := range []bool{false, true} {
		for _, p := range polls {
			if (p.Parent != 0) != kids {
				continue
			}
			out = append(out, showFilePoll{
				ID: pollX[p.ID], Kind: p.Kind, Question: p.Question,
				Options: p.Options, Correct: p.Correct, State: p.State,
				Parent: pollX[p.Parent], Author: p.Author, Ts: p.Ts,
				ToAudience: p.ToAudience, ToPresenter: p.ToPresenter, Spot: pollX[p.Spot], AutoApprove: p.AutoApprove,
			})
			if vs, verr := d.Store.ListVotes(p.ID); verr == nil {
				for _, v := range vs {
					votes = append(votes, showFileVote{PollID: pollX[p.ID], Peer: v.Peer, Choice: v.Choice, Ts: v.Ts})
				}
			}
		}
	}
	return out, votes
}

// roomContent is what a show file or an event copy restores into a room.
type roomContent struct {
	Cues     []timerpi.Cue
	Messages []timerpi.Message
	Polls    []showFilePoll
	Votes    []showFileVote
	Screens  []eventBundleScreen
	Presets  []showFilePreset
	Runtime  timerpi.Runtime
}

// restoreRoom writes rc into room id (cue ids re-numbered, screens re-keyed
// through boardX). Polls broadcast only when there are any.
func (d *Deps) restoreRoom(id int64, rc roomContent, boardX map[int64]int64) error {
	cues := make([]timerpi.Cue, len(rc.Cues))
	for i, c := range rc.Cues {
		c.ID, c.ShowID = 0, 0
		cues[i] = c
	}
	if err := d.Store.ReplaceCues(id, cues); err != nil {
		return fmt.Errorf("cues: %w", err)
	}
	for _, m := range rc.Messages {
		nm, err := d.Store.CreateMessage(id, m.Text, m.Color)
		if err != nil {
			continue
		}
		if m.ShownAt > 0 {
			if err := d.Store.ShowMessage(id, nm.ID, m.ShownAt); err != nil {
				log.Printf("routes: restore message shown state: %v", err)
			}
		}
	}
	if len(rc.Polls) > 0 {
		d.restoreBundlePolls(id, rc.Polls, rc.Votes)
	}
	for _, s := range rc.Screens {
		if err := d.Store.SetScreenConfig(id, s.Name, s.Theme, boardX[s.BoardID], s.Room); err != nil {
			log.Printf("routes: restore screen %s: %v", s.Name, err)
			continue
		}
		_ = d.Store.SetScreenLook(id, s.Name, s.Kind, s.Rotation) // show files carry none: ""/0 is a fresh row's default
		if s.Template != "" {
			_ = d.Store.SetScreenTemplate(id, s.Name, s.Template)
		}
		if s.Key != "" {
			_ = d.Store.SetScreenKey(id, s.Name, s.Key)
		}
	}
	for _, p := range rc.Presets {
		if len(p.Data) > 0 && string(p.Data) != "null" {
			if _, err := d.Store.SavePreset(id, p.Name, string(p.Data)); err != nil {
				log.Printf("routes: restore preset: %v", err)
			}
		}
	}
	rt := rc.Runtime
	rt.ShowID = id
	if rt.Rate <= 0 {
		rt.Rate = timerpi.DefaultRate
	}
	if err := d.Store.SaveRuntime(rt); err != nil {
		return fmt.Errorf("runtime: %w", err)
	}
	return nil
}

// restoreBundleVotes attaches vote rows via the export-slot → db-id map
// the import loop built (DB UNIQUE absorbs a double import).
func (d *Deps) restoreBundleVotes(pollX map[int64]int64, votes []showFileVote) {
	for _, v := range votes {
		if dbID, ok := pollX[v.PollID]; ok && dbID > 0 {
			if err := d.Store.VoteRaw(dbID, v.Peer, v.Choice, v.Ts); err != nil {
				log.Printf("routes: import vote: %v", err) // RS25
			}
		}
	}
}

// dataURL encodes asset bytes as an inline data URL.
func dataURL(a timerpi.Asset) string {
	return fmt.Sprintf("data:%s;base64,%s", a.Mime, base64.StdEncoding.EncodeToString(a.Bytes))
}

// dataURLBytes reverses a data URL ("data:mime;base64,…") into raw bytes.
func dataURLBytes(du string) ([]byte, error) {
	i := strings.Index(du, ";base64,")
	if i < 0 {
		return nil, fmt.Errorf("timerpi: asset payload not a base64 data URL")
	}
	return base64.StdEncoding.DecodeString(du[i+len(";base64,"):])
}
