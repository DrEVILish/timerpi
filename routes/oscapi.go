// oscapi.go — OSC bridge settings + listener lifecycle (proposal #8:
// OSC only). Global kv settings (settings table): osc.in.enabled/port,
// osc.out.enabled/host/port. POST /api/osc saves and re-syncs the UDP
// listener; outbound fires read the target per event (cheap, human-rate).
package routes

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"timerpi/oscbridge"
	"timerpi/timerpi"
)

// OscInbound is the process-wide OSC UDP listener (nil until first use).
var OscInbound = &oscbridge.Inbound{}

func registerOscRoutes(r gin.IRouter, d *Deps) {
	r.GET("/api/osc", d.apiOscGet)
	r.POST("/api/osc", d.apiOscSet)
	r.POST("/api/osc/test", d.apiOscTest)
}

// POST /api/osc/test — send one probe packet to the configured CuTePi/QLab
// peer. UDP has no ack (QLab's channel is fire-and-forget), so "ok" means
// the packet left this host without an OS error — never that CuTePi acted
// on it. The address is one CuTePi's QLab listener ignores.
func (d *Deps) apiOscTest(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	kv, err := d.oscSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if kv["osc.out.enabled"] != "1" || kv["osc.out.host"] == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "outbound bridge not configured"})
		return
	}
	host := kv["osc.out.host"]
	port := kv["osc.out.port"]
	if port == "" {
		port = "53000"
	}
	if err := oscbridge.Send(host+":"+port, "/timerpi/test"); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "sent": host + ":" + port})
}

func (d *Deps) oscSettings() (map[string]string, error) {
	return d.Store.AllSettings()
}

// oscSync applies the kv settings to the live listener; returns the error
// for the API response (bad port etc.).
func (d *Deps) oscSync() error {
	if d.Store == nil {
		return nil
	}
	kv, err := d.oscSettings()
	if err != nil {
		return err
	}
	addr := ""
	if kv["osc.in.enabled"] == "1" {
		port := kv["osc.in.port"]
		if port == "" {
			port = "9000"
		}
		addr = "0.0.0.0:" + port
	}
	if err := OscInbound.SetInbound(addr, d.oscDispatch, func(perr error) {
		log.Printf("osc: inbound packet: %v", perr)
	}); err != nil {
		return err
	}
	return nil
}

// oscDispatch turns one inbound message into an engine verb (same verb set
// the WS carries; blank/unblank are store verbs like their WS branch).
func (d *Deps) oscDispatch(m oscbridge.Message) {
	code, verb, pos := oscbridge.VerbMap(m)
	if code == "" || verb == "" {
		return
	}
	id, ok := timerpi.ResolveShowID(d.Store, code)
	if !ok {
		log.Printf("osc: unknown show code %q", code)
		return
	}
	args := map[string]any{}
	if pos > 0 {
		args["pos"] = pos
	}
	switch verb {
	case "go", "next", "prev", "start", "pause", "resume", "reset":
		eng, err := d.engineFor(id)
		if err != nil {
			log.Printf("osc: %s engine: %v", verb, err)
			return
		}
		if err := eng.ApplyCmd(verb, args); err != nil {
			log.Printf("osc: %s refused: %v", verb, err)
			return
		}
		d.logAction(id, "osc", verb+" "+code)
	case "blank", "unblank":
		if err := d.Store.SetShowBlanked(id, verb == "blank"); err != nil {
			log.Printf("osc: blank: %v", err)
			return
		}
		d.logAction(id, "osc", verb+" "+code)
		if eng, err := d.engineFor(id); err == nil {
			_ = eng.Notify()
		}
		kind := "go"
		if verb == "blank" {
			kind = "panic"
		}
		oscbridge.FireOut(kind, 0)
	default:
		log.Printf("osc: unsupported verb %q", verb)
	}
}

func (d *Deps) apiOscGet(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	kv, _ := d.oscSettings()
	c.JSON(http.StatusOK, gin.H{"ok": true, "config": gin.H{
		"in": gin.H{
			"enabled": kv["osc.in.enabled"] == "1",
			"port":    kv["osc.in.port"],
		},
		"out": gin.H{
			"enabled": kv["osc.out.enabled"] == "1",
			"host":    kv["osc.out.host"],
			"port":    kv["osc.out.port"],
		},
	}})
}

// POST /api/osc {in:{enabled,port}, out:{enabled,host,port}} — save kv +
// resync listener.
func (d *Deps) apiOscSet(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	var body struct {
		In struct {
			Enabled bool   `json:"enabled"`
			Port    string `json:"port"`
		} `json:"in"`
		Out struct {
			Enabled bool   `json:"enabled"`
			Host    string `json:"host"`
			Port    string `json:"port"`
		} `json:"out"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	if body.In.Port != "" {
		if n, perr := strconv.Atoi(body.In.Port); perr != nil || n <= 0 || n > 65535 {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad inbound port"})
			return
		}
	}
	if body.Out.Host != "" && body.Out.Port == "" {
		body.Out.Port = "53000" // QLab/CuTePi default
	}
	if body.Out.Port != "" {
		if n, perr := strconv.Atoi(body.Out.Port); perr != nil || n <= 0 || n > 65535 {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad outbound port"})
			return
		}
	}
	sv := func(k, v string) {
		if serr := d.Store.SetSetting(k, v); serr != nil {
			log.Printf("routes: osc setting %s: %v", k, serr)
		}
	}
	sv("osc.in.enabled", b01(body.In.Enabled))
	sv("osc.in.port", body.In.Port)
	sv("osc.out.enabled", b01(body.Out.Enabled))
	sv("osc.out.host", body.Out.Host)
	sv("osc.out.port", body.Out.Port)
	if err := d.oscSync(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return ""
}
