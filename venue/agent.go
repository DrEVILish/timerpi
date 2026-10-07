// Package venue is a box's side of events (VENUE-CLOUD §3–§6, STATUS
// N12–N16): pairing, holding or mirroring its event, release, and the link
// from a venue's primary box to the cloud.
//
// A box is attached to one event at a time:
//
//   - Unpaired, it shows a 6-digit code (changes every 10 minutes) and
//     registers it in the waiting room of the cloud, of every primary box it
//     can see and, once it holds an event, of itself. The Event Technician
//     types the code on the event page; the box's next poll brings its
//     screen, its key and the event with its mesh key.
//   - An event made on an unpaired box (offline, at timerpi.local) attaches
//     that box to it; its own screen still waits to be paired.
//   - The first box of an event at a venue pulls the event from the cloud
//     and holds it (the primary); the others mirror the primary every 30 s,
//     so a takeover finds the event in place.
//   - Every 30 s a box asks where its event lives whether it was released or
//     deleted; then it forgets the event and shows a code again.
package venue

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"timerpi/mdns"
	"timerpi/mesh"
	"timerpi/routes"
	"timerpi/timerpi"
)

// Pairing is what a box keeps about its event (settings "box.pairing").
type Pairing struct {
	Event     string `json:"event"`
	EventName string `json:"eventName"`
	MeshKey   string `json:"meshKey"` // hex
	EndsAt    int64  `json:"endsAt"`
	Room      string `json:"room"` // its screen's room ("" = screen not paired yet)
	Screen    string `json:"screen"`
	ScreenKey string `json:"screenKey"`
	Server    string `json:"server"` // where it was paired ("" = on this box)
}

// Server is what the agent needs from this box's routes.
type Server interface {
	ExportEvent(eventID int64) ([]byte, error)
	ImportEvent(raw []byte) (timerpi.Event, error)
	DeleteEventLocal(eventID int64) error
	LinkedRequest(r *http.Request, eventCode, as, peer string) (*http.Request, error)
	LinkedCookies(eventCode, as string) string
}

// Hub is what the link needs from the WS hub.
type Hub interface {
	BroadcastPoll(showID int64)
	SetAirHook(func(showID int64, on timerpi.OnAir))
}

// Agent runs a box's event life.
type Agent struct {
	Store    *timerpi.DB
	Srv      Server
	Handler  http.Handler // this box's router (tunnelled requests)
	Hub      Hub
	Mesh     *mesh.Device // nil = no mesh (this box is alone, so it leads)
	Port     int
	Name     string // hostname: the box's name in waiting lists
	CloudURL func() string
	Client   *http.Client
	Logf     func(string, ...any)

	mu       sync.Mutex
	code     string
	codeAt   time.Time
	cache    *Pairing
	mirrorAt time.Time
	mirrorID string // hash of the last copy imported
	checkAt  time.Time

	link linkState

	// alias: this box answers http://timerpi.local while it leads its event.
	aliasStop func()
	Alias     func(name string, port int) (stop func()) // nil = mdns.Alias
}

const settingKey = "box.pairing"

func (a *Agent) logf(f string, v ...any) {
	if a.Logf != nil {
		a.Logf(f, v...)
		return
	}
	log.Printf(f, v...)
}

func (a *Agent) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Pairing returns the box's pairing (zero = unpaired).
func (a *Agent) Pairing() Pairing {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cache == nil {
		var p Pairing
		if raw, err := a.Store.GetSetting(settingKey); err == nil && raw != "" {
			_ = json.Unmarshal([]byte(raw), &p)
		}
		a.cache = &p
	}
	return *a.cache
}

func (a *Agent) save(p Pairing) {
	b, _ := json.Marshal(p)
	if err := a.Store.SetSetting(settingKey, string(b)); err != nil {
		a.logf("venue: save pairing: %v", err)
	}
	a.mu.Lock()
	a.cache = &p
	a.mu.Unlock()
	if a.Mesh != nil {
		a.Mesh.Reannounce()
	}
}

// MeshAuth is mesh.Options.Auth: the event and its key ("" when unpaired).
func (a *Agent) MeshAuth() (string, []byte) {
	p := a.Pairing()
	key, err := hex.DecodeString(p.MeshKey)
	if p.Event == "" || err != nil {
		return "", nil
	}
	return p.Event, key
}

// token is the box's waiting-room secret (only it claims its pairing).
func (a *Agent) token() string {
	if t, err := a.Store.GetSetting("box.token"); err == nil && t != "" {
		return t
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	t := hex.EncodeToString(b[:])
	_ = a.Store.SetSetting("box.token", t)
	return t
}

// Code is the pairing code on the box's screen; a new one every 10 minutes.
func (a *Agent) Code() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.code == "" || time.Since(a.codeAt) >= 10*time.Minute {
		n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
		a.code = fmt.Sprintf("%06d", n.Int64())
		a.codeAt = time.Now()
	}
	return a.code
}

// leading: this box is its event's primary (or alone).
func (a *Agent) leading() bool {
	if a.Mesh == nil {
		return true
	}
	id, _ := a.Mesh.Status()
	return id.State == mesh.StatePrimary
}

// primaryURL is the event's primary box when it is another box.
func (a *Agent) primaryURL() string {
	if a.Mesh == nil || a.leading() {
		return ""
	}
	id, _ := a.Mesh.Status()
	return strings.TrimRight(id.PrimaryAddr, "/")
}

// SelfView is what the box's own screen shows (GET /api/pairing/self).
type SelfView struct {
	Name      string `json:"name"`
	Code      string `json:"code,omitempty"` // shown while its screen isn't paired
	Paired    bool   `json:"paired"`
	EventName string `json:"eventName,omitempty"`
	Target    string `json:"target,omitempty"` // where its screen is ("" = waiting for the primary)
}

// Self is the box screen's state.
func (a *Agent) Self() SelfView {
	p := a.Pairing()
	v := SelfView{Name: a.Name, EventName: p.EventName}
	if p.Room == "" {
		v.Code = a.Code()
		return v
	}
	v.Paired = true
	path := "/d/" + url.PathEscape(p.Room) + "?screen=" + url.QueryEscape(p.Screen) + "&key=" + url.QueryEscape(p.ScreenKey)
	switch {
	case a.leading():
		v.Target = path
	case a.primaryURL() != "":
		v.Target = a.primaryURL() + path
	}
	return v
}

// Run drives the box until ctx ends.
func (a *Agent) Run(ctx context.Context) {
	go a.linkLoop(ctx)
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		a.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// step is one pass: adopt a local event, show and register the code, keep
// the event, check for release.
func (a *Agent) step(ctx context.Context) {
	p := a.Pairing()
	if p.Event == "" {
		if ev, ok := a.localEvent(); ok {
			key, err := a.Store.EventMeshKey(ev.ID)
			if err == nil {
				p = Pairing{Event: ev.Code, EventName: ev.Name, MeshKey: key, EndsAt: ev.EndsAt}
				a.save(p)
				a.logf("venue: this box holds event %s (made here)", ev.Code)
			}
		}
	}
	if p.Room == "" {
		a.offerCode(ctx, p)
		p = a.Pairing()
	}
	if p.Event != "" {
		a.keepEvent(ctx, p)
		a.checkRelease(ctx, p)
	}
	a.keepAlias(a.Pairing().Event != "" && a.leading() && a.Mesh != nil)
}

// keepAlias publishes timerpi.local while this box leads its event, and
// withdraws it when it stops (a takeover moves it to the new primary).
func (a *Agent) keepAlias(want bool) {
	switch {
	case want && a.aliasStop == nil:
		alias := a.Alias
		if alias == nil {
			alias = mdns.Alias
		}
		a.aliasStop = alias(mdns.AliasName, a.Port)
	case !want && a.aliasStop != nil:
		a.aliasStop()
		a.aliasStop = nil
	}
}

// localEvent is the event made on this box (not a copy, not released).
// Only when there is exactly one: a box upgraded from v2 with several
// events stays unattached until one is paired (one event per box).
func (a *Agent) localEvent() (timerpi.Event, bool) {
	list, err := a.Store.ListEvents()
	if err != nil {
		return timerpi.Event{}, false
	}
	var found []timerpi.Event
	for _, ev := range list {
		if ev.ReleasedAt == 0 && ev.MeshKey == "" {
			found = append(found, ev)
		}
	}
	if len(found) != 1 {
		return timerpi.Event{}, false
	}
	return found[0], true
}

// offerCode registers the pairing code where an Event Technician can type
// it, and takes a pairing that came back.
func (a *Agent) offerCode(ctx context.Context, p Pairing) {
	var targets []string
	if u := a.cloud(); u != "" {
		targets = append(targets, u)
	}
	if a.Mesh != nil {
		_, peers := a.Mesh.Status()
		for _, pv := range peers {
			if pv.Role == "primary" {
				if u := PeerURL(pv.Addrs, pv.Port); u != "" {
					targets = append(targets, u)
				}
			}
		}
	}
	self := fmt.Sprintf("http://127.0.0.1:%d", a.Port)
	if p.Event != "" {
		targets = append(targets, self)
	}
	code, tok := a.Code(), a.token()
	for _, t := range targets {
		body, _ := json.Marshal(map[string]string{"name": a.Name, "host": a.Name, "token": tok, "code": code})
		if !a.post(ctx, t+"/api/waiting/register", body, nil) {
			continue
		}
		var mine struct {
			Assigned, Screen, Key string
			Pairing               *struct {
				Event, EventName, MeshKey, Room string
				EndsAt                          int64
			}
		}
		q := url.Values{"name": {a.Name}, "host": {a.Name}, "token": {tok}}
		if !a.get(ctx, t+"/api/waiting/mine?"+q.Encode(), nil, &mine) || mine.Assigned == "" || mine.Pairing == nil {
			continue
		}
		np := Pairing{Event: mine.Pairing.Event, EventName: mine.Pairing.EventName, MeshKey: mine.Pairing.MeshKey,
			EndsAt: mine.Pairing.EndsAt, Room: mine.Assigned, Screen: mine.Screen, ScreenKey: mine.Key, Server: t}
		if t == self {
			np.Server = ""
		}
		if np.Event != p.Event && p.Event != "" {
			a.forgetLocal(p, false) // paired into another event
		}
		a.save(np)
		a.logf("venue: paired to event %s as screen %q", np.Event, np.Screen)
		return
	}
}

// keepEvent makes sure this box has its event: the primary pulls it from
// the cloud the first time; members mirror the primary.
func (a *Agent) keepEvent(ctx context.Context, p Pairing) {
	key, _ := hex.DecodeString(p.MeshKey)
	_, have := a.Store.ResolveEvent(p.Event)
	src := ""
	switch {
	case a.primaryURL() != "":
		if have && time.Since(a.mirrorAt) < 30*time.Second {
			return
		}
		src = a.primaryURL()
	case !have && p.Server != "":
		src = p.Server // the first box at the venue: the event comes from the cloud
	default:
		return
	}
	a.mirrorAt = time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, src+"/api/link/bundle?event="+url.QueryEscape(p.Event), nil)
	req.Header.Set(routes.LinkAuthHeader, routes.LinkAuth(key, "bundle", p.Event, time.Now()))
	resp, err := a.client().Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	sum := sha256.Sum256(raw)
	if id := hex.EncodeToString(sum[:]); id == a.mirrorID && have {
		return
	} else {
		a.mirrorID = id
	}
	ev, err := a.Srv.ImportEvent(raw)
	if err != nil {
		a.logf("venue: event copy from %s: %v", src, err)
		return
	}
	_ = a.Store.SetEventMeshKey(ev.ID, p.MeshKey)
	if !have {
		a.logf("venue: event %s copied from %s", ev.Code, src)
	}
}

// checkRelease asks where the event lives whether it ended or went.
func (a *Agent) checkRelease(ctx context.Context, p Pairing) {
	if time.Since(a.checkAt) < 30*time.Second {
		return
	}
	a.checkAt = time.Now()
	if ev, ok := a.Store.ResolveEvent(p.Event); ok && ev.ReleasedAt > 0 && a.leading() {
		a.release(p, "it ended", false)
		return
	}
	src := a.primaryURL()
	if src == "" {
		src = p.Server // the cloud, when that's where the box was paired
	}
	if src == "" {
		return
	}
	var st struct {
		OK, Exists, Released bool
		Now                  int64
	}
	if !a.get(ctx, src+"/api/pairing/status?event="+url.QueryEscape(p.Event), nil, &st) || !st.OK {
		return // can't reach it: keep going (offline is normal)
	}
	if src == a.primaryURL() && st.Now > 0 {
		a.ClockHint(time.UnixMilli(st.Now)) // members take the primary's time
	}
	switch {
	case !st.Exists:
		a.release(p, "it was deleted", true)
	case st.Released:
		a.release(p, "it ended", false)
	}
}

// release forgets the event: its pairing and key, and its copy when that
// is safe (forgetLocal).
func (a *Agent) release(p Pairing, why string, deleted bool) {
	a.logf("venue: released from event %s: %s", p.Event, why)
	a.forgetLocal(p, deleted)
	a.save(Pairing{})
}

// forgetLocal drops this box's copy of the event only when nothing is lost:
// the event was deleted, this box only mirrored the primary, or the final
// copy reached the cloud. Otherwise the event stays on the box.
func (a *Agent) forgetLocal(p Pairing, deleted bool) {
	ev, ok := a.Store.ResolveEvent(p.Event)
	if !ok {
		return
	}
	if deleted || !a.leading() || a.link.uploaded(p.Event) {
		if err := a.Srv.DeleteEventLocal(ev.ID); err != nil {
			a.logf("venue: forget event %s: %v", p.Event, err)
		}
		return
	}
	a.logf("venue: event %s kept on this box (no copy reached the cloud)", p.Event)
}

// OnRelease is routes.Deps.OnRelease on the event's primary: the final copy
// goes to the cloud, then the box lets go.
func (a *Agent) OnRelease(ev timerpi.Event) {
	p := a.Pairing()
	if p.Event != ev.Code {
		return
	}
	if a.cloud() != "" {
		if err := a.sendFinal(ev); err != nil {
			a.logf("venue: final copy of %s not uploaded: %v", ev.Code, err)
		}
	}
	a.release(p, "it ended", false)
}

func (a *Agent) cloud() string {
	if a.CloudURL == nil {
		return ""
	}
	return strings.TrimRight(a.CloudURL(), "/")
}

func (a *Agent) post(ctx context.Context, u string, body []byte, hdr map[string]string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (a *Agent) get(ctx context.Context, u string, hdr map[string]string, out any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out) == nil
}
