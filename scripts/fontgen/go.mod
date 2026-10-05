// Nested module: font atlas generator for drm/. Deliberately separate so
// its font dependencies (golang.org/x/image) never enter TimerPi's go.mod.
// The committed output (../../drm/assets/*.png, metrics.json) is what the
// runtime embeds; building this module is only needed to regenerate it.
//
// See run.sh.
module timerpi.org/fontgen

go 1.25.0

require golang.org/x/image v0.18.0

require golang.org/x/text v0.16.0 // indirect
