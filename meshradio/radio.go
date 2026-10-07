// Package meshradio sets up the venue mesh radios (VENUE-CLOUD §11.1).
//
// The radio rule (owner, 2026-10-07): the built-in Wi-Fi always starts on
// 2.4 GHz; a USB radio that can do ad-hoc (IBSS) adds a second leg — on
// 5 GHz if it can, otherwise it takes 2.4 GHz and the built-in Wi-Fi moves to
// 5 GHz. Every box always keeps a 2.4 GHz leg, so boxes with and without
// adapters always reach each other. Both legs join batman-adv's bat0.
//
// `timerpi mesh` runs this at boot (timerpi-mesh.service) and on USB Wi-Fi
// hotplug (udev); `timerpi mesh status` writes neighbours for the network
// page every 10 s (timerpi-mesh-status.timer). Both need root.
package meshradio

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

// ESSID is the mesh network name (owner, 2026-10-07: hard-coded).
const ESSID = "timerpi"

// Phy is one wireless radio.
type Phy struct {
	Name  string       // phy0
	Iface string       // wlan0 ("" = no interface)
	USB   bool         // plugged in over USB (not the built-in Wi-Fi)
	IBSS  bool         // supports ad-hoc mode
	Freqs map[int]bool // MHz → usable for starting IBSS (not disabled, not no-IR)
}

// Can reports whether the radio can start IBSS on freq.
func (p Phy) Can(freq int) bool { return p.IBSS && p.Freqs[freq] }

// Leg is one radio on the mesh.
type Leg struct {
	Iface   string `json:"iface"`
	Phy     string `json:"phy"`
	USB     bool   `json:"usb"`
	Band    string `json:"band"` // "2.4 GHz" | "5 GHz"
	Channel int    `json:"channel"`
	Freq    int    `json:"freq"`
	Error   string `json:"error,omitempty"`
}

// Freq24 and Freq5 turn channel numbers into MHz.
func Freq24(ch int) int { return 2407 + 5*ch }
func Freq5(ch int) int  { return 5000 + 5*ch }

// Plan applies the radio rule. notes explain anything left out.
func Plan(phys []Phy, ch24, ch5 int) (legs []Leg, notes []string) {
	f24, f5 := Freq24(ch24), Freq5(ch5)
	var internal, usb *Phy
	for i := range phys {
		p := &phys[i]
		if p.Iface == "" {
			continue
		}
		switch {
		case !p.USB && internal == nil:
			internal = p
		case p.USB && usb == nil:
			if p.IBSS {
				usb = p
			} else {
				notes = append(notes, p.Iface+": this USB radio can't do ad-hoc mode, so it isn't used")
			}
		}
	}
	leg := func(p *Phy, band string, ch, f int) Leg {
		return Leg{Iface: p.Iface, Phy: p.Name, USB: p.USB, Band: band, Channel: ch, Freq: f}
	}
	l24 := func(p *Phy) Leg { return leg(p, "2.4 GHz", ch24, f24) }
	l5 := func(p *Phy) Leg { return leg(p, "5 GHz", ch5, f5) }

	if internal == nil {
		if usb != nil && usb.Can(f24) {
			return []Leg{l24(usb)}, append(notes, "no built-in Wi-Fi found: the USB radio carries the mesh on 2.4 GHz")
		}
		return nil, append(notes, "no radio can join the mesh")
	}
	if usb == nil {
		return []Leg{l24(internal)}, notes
	}
	switch {
	case usb.Can(f5):
		return []Leg{l24(internal), l5(usb)}, notes
	case usb.Can(f24) && internal.Can(f5):
		return []Leg{l5(internal), l24(usb)}, notes
	case usb.Can(f24):
		notes = append(notes, internal.Iface+" can't start ad-hoc on 5 GHz channel "+strconv.Itoa(ch5)+", so the USB radio isn't used")
	default:
		notes = append(notes, usb.Iface+": this USB radio can't use 2.4 GHz channel "+strconv.Itoa(ch24)+" or 5 GHz channel "+strconv.Itoa(ch5))
	}
	return []Leg{l24(internal)}, notes
}

var (
	reDevPhy   = regexp.MustCompile(`^phy#(\d+)`)
	reDevIface = regexp.MustCompile(`^\s+Interface\s+(\S+)`)
	reFreq     = regexp.MustCompile(`^\s*\*\s+(\d+)(?:\.\d+)?\s+MHz\s+\[\d+\](.*)$`)
)

// ParseDev maps phy names to interface names from `iw dev`.
func ParseDev(out string) map[string]string {
	ifaces := map[string]string{}
	phy := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if m := reDevPhy.FindStringSubmatch(line); m != nil {
			phy = "phy" + m[1]
		} else if m := reDevIface.FindStringSubmatch(line); m != nil && phy != "" {
			if _, seen := ifaces[phy]; !seen {
				ifaces[phy] = m[1]
			}
		}
	}
	return ifaces
}

// ParsePhyInfo reads modes and frequencies from `iw phy <phy> info`.
func ParsePhyInfo(out string) (ibss bool, freqs map[int]bool) {
	freqs = map[int]bool{}
	inModes := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "Supported interface modes:") {
			inModes = true
			continue
		}
		if inModes {
			if strings.HasPrefix(trim, "* ") {
				if strings.TrimSpace(trim[2:]) == "IBSS" {
					ibss = true
				}
				continue
			}
			inModes = false
		}
		if m := reFreq.FindStringSubmatch(line); m != nil {
			f, _ := strconv.Atoi(m[1])
			rest := strings.ToLower(m[2])
			freqs[f] = !strings.Contains(rest, "disabled") && !strings.Contains(rest, "no ir") &&
				!strings.Contains(rest, "passive scan") && !strings.Contains(rest, "radar")
		}
	}
	return ibss, freqs
}

// Originator is one other box batman-adv routes to.
type Originator struct {
	MAC   string `json:"mac"`
	TQ    int    `json:"tq"`    // link quality 0–255
	Via   string `json:"via"`   // next hop MAC
	Iface string `json:"iface"` // our radio that carries it
	SeenS string `json:"seen"`  // last seen, seconds
}

var reOrig = regexp.MustCompile(`^\s*\*\s*([0-9a-f:]{17})\s+([\d.]+)s\s+\(\s*(\d+)\)\s+([0-9a-f:]{17})\s+\[\s*([^\]\s]+)\s*\]`)

// ParseOriginators reads the best routes from `batctl o -H`.
func ParseOriginators(out string) []Originator {
	var list []Originator
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		if m := reOrig.FindStringSubmatch(sc.Text()); m != nil {
			tq, _ := strconv.Atoi(m[3])
			list = append(list, Originator{MAC: m[1], SeenS: m[2], TQ: tq, Via: m[4], Iface: m[5]})
		}
	}
	return list
}
