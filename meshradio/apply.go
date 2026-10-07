package meshradio

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Runner runs commands and reads sysfs; tests swap it for a fake.
type Runner interface {
	Run(name string, args ...string) (string, error)
	Readlink(path string) (string, error)
	ReadFile(path string) (string, error)
}

// OS is the real Runner.
type OS struct{}

func (OS) Run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
func (OS) Readlink(p string) (string, error) { return filepath.EvalSymlinks(p) }
func (OS) ReadFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

// Mesh is the batman-adv interface (created by systemd-networkd, bat0.netdev).
const Mesh = "bat0"

// Status is what the network page shows (/run/timerpi/mesh.json).
type Status struct {
	At          int64        `json:"at"`
	Country     string       `json:"country"`
	Legs        []Leg        `json:"legs"`
	Notes       []string     `json:"notes,omitempty"`
	Wired       bool         `json:"wired"`   // eth0 has a cable: this box bridges to the router
	Gateway     string       `json:"gateway"` // batman gw_mode: server (wired) | client
	Originators []Originator `json:"originators"`
}

// Detect lists the radios with their interface, bus and abilities.
func Detect(r Runner) ([]Phy, error) {
	dev, err := r.Run("iw", "dev")
	if err != nil {
		return nil, err
	}
	ifaces := ParseDev(dev)
	var phys []Phy
	for phy, iface := range ifaces {
		info, err := r.Run("iw", "phy", phy, "info")
		if err != nil {
			continue
		}
		p := Phy{Name: phy, Iface: iface}
		p.IBSS, p.Freqs = ParsePhyInfo(info)
		if link, err := r.Readlink("/sys/class/ieee80211/" + phy + "/device"); err == nil {
			p.USB = strings.Contains(link, "/usb")
		}
		phys = append(phys, p)
	}
	// Stable order: built-in first, then by name.
	for i := 1; i < len(phys); i++ {
		for j := i; j > 0 && less(phys[j], phys[j-1]); j-- {
			phys[j], phys[j-1] = phys[j-1], phys[j]
		}
	}
	return phys, nil
}

func less(a, b Phy) bool {
	if a.USB != b.USB {
		return !a.USB
	}
	return a.Name < b.Name
}

// Up sets the regulatory country, applies the radio rule and joins every
// leg to bat0. It returns the status written for the network page.
func Up(r Runner, country string, ch24, ch5 int) Status {
	st := Status{At: time.Now().UnixMilli(), Country: country}
	if _, err := r.Run("iw", "reg", "set", country); err != nil {
		st.Notes = append(st.Notes, "Wi-Fi country: "+err.Error())
	}
	phys, err := Detect(r)
	if err != nil {
		st.Notes = append(st.Notes, err.Error())
		return st
	}
	legs, notes := Plan(phys, ch24, ch5)
	st.Notes = append(st.Notes, notes...)
	for i := range legs {
		if err := join(r, legs[i]); err != nil {
			legs[i].Error = err.Error()
			// A 5 GHz leg that won't start leaves the box on its 2.4 GHz leg.
		}
	}
	st.Legs = legs
	gateway(r, &st)
	return st
}

// join puts one radio in ad-hoc mode on its frequency and adds it to bat0.
func join(r Runner, l Leg) error {
	steps := [][]string{
		{"ip", "link", "set", "dev", l.Iface, "down"},
		{"iw", "dev", l.Iface, "set", "type", "ibss"},
		{"ip", "link", "set", "dev", l.Iface, "up"},
		{"iw", "dev", l.Iface, "ibss", "join", ESSID, fmt.Sprint(l.Freq), "HT20", "fixed-freq"},
		{"ip", "link", "set", "dev", l.Iface, "master", Mesh},
	}
	_, _ = r.Run("iw", "dev", l.Iface, "ibss", "leave") // not joined yet is fine
	for i, s := range steps {
		if i == 2 {
			// Room for batman-adv's header so bat0 keeps a 1500 MTU; chips
			// that refuse it fall back to batman-adv fragmentation.
			_, _ = r.Run("ip", "link", "set", "dev", l.Iface, "mtu", "1532")
		}
		if _, err := r.Run(s[0], s[1:]...); err != nil {
			return err
		}
	}
	return nil
}

// gateway marks a wired box as a batman gateway (server), others clients,
// and lists the other boxes batman-adv can reach.
func gateway(r Runner, st *Status) {
	carrier, _ := r.ReadFile("/sys/class/net/eth0/carrier")
	st.Wired = strings.TrimSpace(carrier) == "1"
	st.Gateway = "client"
	if st.Wired {
		st.Gateway = "server"
	}
	_, _ = r.Run("batctl", "meshif", Mesh, "gw_mode", st.Gateway)
	if out, err := r.Run("batctl", "meshif", Mesh, "originators", "-H"); err == nil {
		st.Originators = ParseOriginators(out)
	}
}

// Refresh updates the live parts of a saved status (neighbours, cable).
func Refresh(r Runner, st Status) Status {
	st.At = time.Now().UnixMilli()
	gateway(r, &st)
	return st
}

// StatusFile is where the status lives (tmpfs, written as root).
const StatusFile = "/run/timerpi/mesh.json"

// Save writes the status atomically.
func Save(path string, st Status) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads the status ("" when the mesh never ran on this machine).
func Load(path string) (Status, bool) {
	var st Status
	b, err := os.ReadFile(path)
	if err != nil {
		return st, false
	}
	return st, json.Unmarshal(b, &st) == nil
}
