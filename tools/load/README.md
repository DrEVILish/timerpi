# External audience load probe

Run this on a separate Linux host with Go, against a TimerPi test instance:

```sh
go run ./tools/load -base http://192.168.10.55:18080 -clients 200
go run ./tools/load -base http://192.168.10.55:18080 -clients 1000
```

For an already-paired box that accepts only one event, supply its disposable
qualification event and a private password file instead:

```sh
go run ./tools/load -base http://192.168.10.55:18080 -clients 200 \
  -event K7QPM3XB -password-file /private/test-event-password
```

That mode signs into the supplied event, creates a fresh uniquely named load
room, and deletes only that room. Existing event/rooms are preserved. The
password file must have mode 0600 or tighter and contain at most 4096 bytes.

By default the probe creates its own disposable event and room. It starts a
timer and poll,
opens real audience WebSockets, and casts one real HTTP vote per client with
separate server-issued cookies. Initial votes are paced below the public
600/s room limit. It alternates verified operator pause/resume commands
while voting and measures a question-change fanout halfway through the votes.
It never accepts an existing room code. Normal exit and handled
SIGINT/SIGTERM delete its own fixture (event or room); a killed process or
failed connection may leave that disposable fixture behind. Use an isolated test instance.

JSON records the operator threshold and reports join duration,
join/disconnect counts, HTTP vote count and p95,
verified server vote total, audience fanout coverage/p95, operator command
count/errors/p95, cleanup, and `pass`. Event codes, passwords and cookies are
not logged. A nonzero exit means the probe failed, including failed cleanup.

Gates: all clients join within 30 seconds, no disconnects, every vote accepted
and counted, at least 99% receive the marker, marker p95 at most 1 second,
and verified operator-command p95 at most 250 ms without errors. Fanout
latency includes the API request and production poll batching; it uses only
the load host's monotonic clock, requiring no clock synchronization.

For a separately labelled experiment, `-clients 500 -operator-p95 500ms`
uses a 500 ms operator gate. The default remains 250 ms; an experimental pass
does not qualify the default gate or change product requirements.

For the intended venue topology, clients reach the serving Pi over Ethernet;
BATMAN carries inter-Pi synchronization. A client burst through an SSH/BATMAN
relay is a different experiment. Wait for the serving box to be the stable
primary before starting a probe; HTTP 409 during an election is a readiness
failure, not a latency result. Keep builds, browsers and profilers off the
load host during timed runs. Label instrumented runs separately.

This exercises actual networking and the deployed application. Running the
load host over Ethernet proves server capacity, not 200 physical phones or
Wi-Fi airtime. It is a join/vote burst with a measured fanout under load, not
a long-duration soak test.

`go test ./tools/load` launches an isolated real TimerPi to check the probe.
`TP_LOAD_N=200` or `TP_LOAD_N=1000` runs that local fixture at the larger scale;
these fixture results are not Pi measurements.
