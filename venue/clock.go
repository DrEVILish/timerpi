package venue

// clock.go — a Pi has no battery-backed clock (VENUE-CLOUD §5). Normally
// systemd-timesyncd (NTP) and fake-hwclock keep it right. A box whose clock
// is clearly unset (before ClockFloor) takes the time from its primary box,
// and the primary from the Event Technician's browser when they sign in.

import (
	"syscall"
	"time"
)

// ClockFloor: any earlier time means the clock was never set.
var ClockFloor = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// setClock sets the system clock (needs CAP_SYS_TIME; tests swap it).
var setClock = func(t time.Time) error {
	tv := syscall.NsecToTimeval(t.UnixNano())
	return syscall.Settimeofday(&tv)
}

// nowFn is the clock the agent reads (tests swap it).
var nowFn = time.Now

// ClockHint sets the system clock from a trusted time when the box's own
// clock is unset. A box whose clock looks right is never moved.
func (a *Agent) ClockHint(t time.Time) {
	if !nowFn().Before(ClockFloor) || t.Before(ClockFloor) {
		return
	}
	if err := setClock(t); err != nil {
		a.logf("venue: setting the clock to %s: %v", t.UTC().Format(time.RFC3339), err)
		return
	}
	a.logf("venue: clock was unset; set to %s", t.UTC().Format(time.RFC3339))
}
