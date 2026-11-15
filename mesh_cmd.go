package main

import (
	"fmt"
	"log"

	"timerpi/config"
	"timerpi/mesh"
	"timerpi/meshradio"
	"timerpi/venue"
)

// runMesh is `timerpi mesh` (apply the radio rule, at boot and on USB Wi-Fi
// hotplug) and `timerpi mesh status` (refresh neighbours for the network
// page, every 10 s). Both run as root from systemd/udev.
func runMesh(args []string) int {
	config.LoadConfig()
	r := meshradio.OS{}
	if len(args) > 0 && args[0] == "status" {
		st, ok := meshradio.Load(meshradio.StatusFile)
		if !ok {
			return 0 // the mesh hasn't been set up yet
		}
		if err := meshradio.Save(meshradio.StatusFile, meshradio.Refresh(r, st)); err != nil {
			log.Printf("timerpi mesh status: %v", err)
			return 1
		}
		return 0
	}
	if len(args) > 0 {
		fmt.Println("usage: timerpi mesh [status]")
		return 2
	}
	country, ch24, ch5 := config.MeshRadio()
	st := meshradio.Up(r, country, ch24, ch5)
	for _, l := range st.Legs {
		state := "on the mesh"
		if l.Error != "" {
			state = l.Error
		}
		log.Printf("timerpi mesh: %s (%s) %s channel %d: %s", l.Iface, map[bool]string{true: "USB", false: "built-in"}[l.USB], l.Band, l.Channel, state)
	}
	for _, n := range st.Notes {
		log.Printf("timerpi mesh: %s", n)
	}
	saveErr := meshradio.Save(meshradio.StatusFile, st)
	if saveErr != nil {
		log.Printf("timerpi mesh: %v", saveErr)
	}
	return meshApplyExitCode(st, saveErr)
}

// updateSources lists where the boot-time updater looks: the cloud first,
// then every TimerPi box the mesh sees (other versions included: a newer
// box is exactly where an update comes from).
func updateSources(dev *mesh.Device) []string {
	var out []string
	if u := config.CloudURL(); u != "" {
		out = append(out, u)
	}
	if dev == nil {
		return out
	}
	_, peers := dev.Status()
	for _, p := range peers {
		if u := venue.PeerURL(p.Addrs, p.Port); u != "" {
			out = append(out, u)
		}
	}
	return out
}

func meshApplyExitCode(st meshradio.Status, saveErr error) int {
	if saveErr != nil {
		return 1
	}
	for _, leg := range st.Legs {
		if leg.Error == "" {
			return 0
		}
	}
	return 1
}
