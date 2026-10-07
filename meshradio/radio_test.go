package meshradio

import (
	"fmt"
	"strings"
	"testing"
)

const iwDev = `phy#1
	Interface wlan1
		ifindex 4
		type managed
phy#0
	Unnamed/non-netdev interface
		wdev 0x2
	Interface wlan0
		ifindex 3
		type managed
`

// Shaped like a Pi's brcmfmac: IBSS, 2.4 GHz 1–13, 5 GHz with 36 usable
// and 52 radar-only.
const phyBuiltin = `Wiphy phy0
	Band 1:
		Frequencies:
			* 2412.0 MHz [1] (20.0 dBm)
			* 2472.0 MHz [13] (20.0 dBm)
			* 2484.0 MHz [14] (disabled)
	Band 2:
		Frequencies:
			* 5180.0 MHz [36] (20.0 dBm)
			* 5260.0 MHz [52] (20.0 dBm) (no IR, radar detection)
	Supported interface modes:
		 * IBSS
		 * managed
		 * AP
	software interface modes (can always be added):
		 * monitor
`

const phyUSB24 = `Wiphy phy1
	Band 1:
		Frequencies:
			* 2472 MHz [13] (20.0 dBm)
	Supported interface modes:
		 * IBSS
		 * managed
`

func phy(name, iface string, usb, ibss bool, freqs ...int) Phy {
	p := Phy{Name: name, Iface: iface, USB: usb, IBSS: ibss, Freqs: map[int]bool{}}
	for _, f := range freqs {
		p.Freqs[f] = true
	}
	return p
}

func legsOf(legs []Leg) string {
	var s []string
	for _, l := range legs {
		s = append(s, fmt.Sprintf("%s@%d", l.Iface, l.Channel))
	}
	return strings.Join(s, " ")
}

// The owner's radio rule (VENUE-CLOUD §11.1).
func TestPlanRadioRule(t *testing.T) {
	in := phy("phy0", "wlan0", false, true, 2472, 5180)
	in24 := phy("phy0", "wlan0", false, true, 2472)
	for _, c := range []struct {
		name string
		phys []Phy
		want string
	}{
		{"built-in only", []Phy{in}, "wlan0@13"},
		{"USB with 5 GHz", []Phy{in, phy("phy1", "wlan1", true, true, 2472, 5180)}, "wlan0@13 wlan1@36"},
		{"USB 5 GHz only", []Phy{in, phy("phy1", "wlan1", true, true, 5180)}, "wlan0@13 wlan1@36"},
		{"USB 2.4 GHz only", []Phy{in, phy("phy1", "wlan1", true, true, 2472)}, "wlan0@36 wlan1@13"},
		{"USB 2.4 only, built-in can't do 5", []Phy{in24, phy("phy1", "wlan1", true, true, 2472)}, "wlan0@13"},
		{"USB without ad-hoc", []Phy{in, phy("phy1", "wlan1", true, false, 2472, 5180)}, "wlan0@13"},
		{"no built-in", []Phy{phy("phy1", "wlan1", true, true, 2472, 5180)}, "wlan1@13"},
		{"nothing", nil, ""},
	} {
		legs, _ := Plan(c.phys, 13, 36)
		if got := legsOf(legs); got != c.want {
			t.Errorf("%s: legs %q, want %q", c.name, got, c.want)
		}
		// The invariant: always a 2.4 GHz leg whenever there is a leg.
		has24 := false
		for _, l := range legs {
			has24 = has24 || l.Band == "2.4 GHz"
		}
		if len(legs) > 0 && !has24 {
			t.Errorf("%s: no 2.4 GHz leg", c.name)
		}
	}
}

func TestParsers(t *testing.T) {
	if m := ParseDev(iwDev); m["phy0"] != "wlan0" || m["phy1"] != "wlan1" {
		t.Errorf("ParseDev = %v", m)
	}
	ibss, f := ParsePhyInfo(phyBuiltin)
	if !ibss || !f[2472] || !f[5180] || f[2484] || f[5260] {
		t.Errorf("ParsePhyInfo: ibss=%v freqs=%v", ibss, f)
	}
	if ibss, f := ParsePhyInfo(phyUSB24); !ibss || !f[2472] || f[5180] {
		t.Errorf("USB phy: ibss=%v freqs=%v", ibss, f)
	}
	orig := ParseOriginators(`[B.A.T.M.A.N. adv 2024.2, MainIF/MAC: wlan0/dc:a6:32:00:00:01 (bat0/aa:bb:cc:dd:ee:ff BATMAN_IV)]
   Originator        last-seen (#/255) Nexthop           [outgoingIF]
 * dc:a6:32:00:00:02    0.520s   (251) dc:a6:32:00:00:02 [     wlan0]
   dc:a6:32:00:00:02    0.600s   (120) dc:a6:32:00:00:03 [     wlan1]
 * dc:a6:32:00:00:03    1.020s   (180) dc:a6:32:00:00:02 [     wlan1]
`)
	if len(orig) != 2 || orig[0].TQ != 251 || orig[1].Iface != "wlan1" || orig[1].Via != "dc:a6:32:00:00:02" {
		t.Errorf("ParseOriginators = %+v", orig)
	}
}

type fakeRunner struct {
	out   map[string]string
	fail  map[string]bool
	links map[string]string
	files map[string]string
	ran   []string
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	f.ran = append(f.ran, cmd)
	for k := range f.fail {
		if strings.HasPrefix(cmd, k) {
			return "", fmt.Errorf("%s: refused", cmd)
		}
	}
	return f.out[cmd], nil
}
func (f *fakeRunner) Readlink(p string) (string, error) { return f.links[p], nil }
func (f *fakeRunner) ReadFile(p string) (string, error) { return f.files[p], nil }

func TestUpJoinsBothRadios(t *testing.T) {
	r := &fakeRunner{
		out: map[string]string{"iw dev": iwDev, "iw phy phy0 info": phyBuiltin, "iw phy phy1 info": phyUSB24},
		links: map[string]string{
			"/sys/class/ieee80211/phy0/device": "/sys/devices/platform/soc/fe300000.mmcnr/mmc_host/mmc1/mmc1:0001/mmc1:0001:1",
			"/sys/class/ieee80211/phy1/device": "/sys/devices/platform/scb/fd500000.pcie/pci0000:00/0000:01:00.0/usb1/1-1/1-1.3/1-1.3:1.0",
		},
		files: map[string]string{"/sys/class/net/eth0/carrier": "1\n"},
	}
	st := Up(r, "GB", 13, 36)
	if got := legsOf(st.Legs); got != "wlan0@36 wlan1@13" {
		t.Fatalf("legs %q (notes %v)", got, st.Notes)
	}
	all := strings.Join(r.ran, "\n")
	for _, want := range []string{
		"iw reg set GB",
		"iw dev wlan0 ibss join timerpi 5180 HT20 fixed-freq",
		"iw dev wlan1 ibss join timerpi 2472 HT20 fixed-freq",
		"ip link set dev wlan0 master bat0",
		"ip link set dev wlan1 master bat0",
		"batctl meshif bat0 gw_mode server",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if !st.Wired || st.Gateway != "server" {
		t.Errorf("wired=%v gateway=%q", st.Wired, st.Gateway)
	}
}

// A radio that refuses to start a leg reports it; the other leg still joins.
func TestUpReportsFailedLeg(t *testing.T) {
	r := &fakeRunner{
		out:  map[string]string{"iw dev": iwDev, "iw phy phy0 info": phyBuiltin, "iw phy phy1 info": phyUSB24},
		fail: map[string]bool{"iw dev wlan0 ibss join": true},
		links: map[string]string{
			"/sys/class/ieee80211/phy1/device": "/sys/devices/x/usb1/1-1",
		},
	}
	st := Up(r, "GB", 13, 36)
	if len(st.Legs) != 2 || st.Legs[0].Error == "" || st.Legs[1].Error != "" {
		t.Fatalf("legs = %+v", st.Legs)
	}
	if st.Gateway != "client" {
		t.Errorf("unwired box is a %q", st.Gateway)
	}
}
