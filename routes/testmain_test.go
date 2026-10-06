package routes_test

import (
	"net/http"
	"net/http/cookiejar"

	"timerpi/routes"
	"timerpi/timerpi"
)

// Every test request goes through one cookie-carrying client, like a real
// browser: sign-ins (newAPITest signs in as the event's SuperOperator)
// stick for the requests that follow. Session cookies are named per event
// or room code, so tests never collide on the shared jar.
func init() {
	jar, _ := cookiejar.New(nil)
	http.DefaultClient = &http.Client{Jar: jar}
	// Every test runs from 127.0.0.1, and many phones are simulated by
	// fresh cookie-less clients; lift the per-IP device mint budget
	// (TestAudienceDeviceMintBudget checks the real one).
	routes.SetAudienceMintBudget(1 << 30)
	routes.SetOpenEndpointBudgets(1<<30, 1<<30)
	// Production hashes cost ~0.5 s on a Pi; tests hash hundreds of times.
	timerpi.SetPasswordHashIterations(1000)
}
