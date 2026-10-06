// bundle.go — the §11.9 full-fidelity show bundle: what v2 exports beyond
// the v1 cues/messages/schedule (polls with moderation state, their votes,
// screens, boards, presets, zone + map asset) and how it re-keys on import.
package routes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"timerpi/boards"
	"timerpi/timerpi"
)

// fillBundleExtras loads the §11.9 sections of a v2 export. Export ids are
// the bundle's LIST POSITIONS (1..N, parents before children — the order
// this function emits), so the importer can re-key without any id table:
// parents always precede their children in the stream.
func (d *Deps) fillBundleExtras(id int64, sf *showFile) {
	pollX := map[int64]int64{} // live poll id → export id
	polls, err := d.Store.ListPolls(id)
	if err == nil {
		eID := int64(0)
		for _, p := range polls { // parents take 1..P in list order
			if p.Parent == 0 {
				eID++
				pollX[p.ID] = eID
			}
		}
		for _, p := range polls { // children take P+1..N
			if p.Parent != 0 {
				eID++
				pollX[p.ID] = eID
			}
		}
		// Emit in EXPORT-ID order (the contract: parents precede their
		// children in the stream) — ListPolls order is updated-releative
		// and would interleave them.
		for _, p := range polls {
			if p.Parent != 0 {
				continue
			}
			sf.Polls = append(sf.Polls, showFilePoll{
				ID: pollX[p.ID], Kind: p.Kind, Question: p.Question,
				Options: p.Options, Correct: p.Correct, State: p.State,
				Parent: pollX[p.Parent], Author: p.Author, Ts: p.Ts,
				ToAudience: p.ToAudience, ToPresenter: p.ToPresenter, Spot: pollX[p.Spot], AutoApprove: p.AutoApprove,
			})
			if vs, verr := d.Store.ListVotes(p.ID); verr == nil {
				for _, v := range vs {
					sf.Votes = append(sf.Votes, showFileVote{PollID: pollX[p.ID], Peer: v.Peer, Choice: v.Choice, Ts: v.Ts})
				}
			}
		}
		for _, p := range polls {
			if p.Parent == 0 {
				continue
			}
			sf.Polls = append(sf.Polls, showFilePoll{
				ID: pollX[p.ID], Kind: p.Kind, Question: p.Question,
				Options: p.Options, Correct: p.Correct, State: p.State,
				Parent: pollX[p.Parent], Author: p.Author, Ts: p.Ts,
				ToAudience: p.ToAudience, ToPresenter: p.ToPresenter, Spot: pollX[p.Spot], AutoApprove: p.AutoApprove,
			})
			if vs, verr := d.Store.ListVotes(p.ID); verr == nil {
				for _, v := range vs {
					sf.Votes = append(sf.Votes, showFileVote{PollID: pollX[p.ID], Peer: v.Peer, Choice: v.Choice, Ts: v.Ts})
				}
			}
		}
	}
	boardX := map[int64]int64{}
	if list, berr := boards.ListBoards(d.Store.DB, id); berr == nil {
		for _, b := range list {
			boardX[b.ID] = int64(len(sf.Boards) + 1)
			sf.Boards = append(sf.Boards, showFileBoard{
				ID: boardX[b.ID], Name: b.Name, Layout: json.RawMessage([]byte(b.LayoutJSON())),
			})
		}
	}
	if rows, rerr := d.Store.ListScreens(id); rerr == nil {
		for _, r := range rows {
			sf.Screens = append(sf.Screens, showFileScreen{Name: r.Name, Theme: r.Theme, Room: r.Room, BoardID: boardX[r.BoardID]})
		}
	}
	if ps, perr := d.Store.ListPresets(id); perr == nil {
		for _, p := range ps {
			sf.Presets = append(sf.Presets, showFilePreset{Name: p.Name, Data: json.RawMessage([]byte(p.Data))})
		}
	}
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

// restoreBundleVotes attaches vote rows via the export-slot → db-id map
// the import loop built (DB UNIQUE absorbs a double import).
func (d *Deps) restoreBundleVotes(pollX map[int64]int64, votes []showFileVote) {
	for _, v := range votes {
		if dbID, ok := pollX[v.PollID]; ok && dbID > 0 {
			_ = d.Store.VoteRaw(dbID, v.Peer, v.Choice, v.Ts)
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
