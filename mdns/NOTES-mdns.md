# NOTES-mdns — integration contract for mesh / routes-network agents

The appliance layer package `timerpi/mdns` (Agent E) exposes what
PLAN.md §5 needs. Use it; do not duplicate zeroconf wiring.

```go
// Announce: never fails the caller; containers without multicast get a
// no-op announcer (false/stop-nothing) plus a log line.
collides, stop := mdns.Announce(hostname, port, mdns.ServiceMeta{
    Host:  hostname,          // machine hostname ("<host>.local" identity)
    Role:  "primary",         // primary | display | idle
    Ver:   version,           // short app version
    Epoch: bootEpochMS,       // bump on re-announce → peers drop stale state
})
defer stop()                  // goodbye + listener teardown

if collides() { /* same instance name claimed by another host → rename + re-announce */ }

// Discover (dedupe by host, self excluded):
peers, _ := mdns.FindPeers(ctx, 3*time.Second) // []Peer{Host, Addrs, Port, TXT, LastSeen}
```

Details you can rely on:

* Service type is always `mdns.ServiceType``_timerpi._tcp`, domain
  `local.`. TXT keys exactly `host role ver epoch` (ordered encoding —
  `mdns.ServiceMeta.EncodeTXT` is deterministic).
* `Peer.Host` is lowercase, `.local`-suffix stripped — compare with
  `normalizeHost`-equivalent semantics when matching.
* TXT decode: `mdns.MetadataFromTXT`—`MetaFromTXT(lines)` returns
  `(ServiceMeta, ok)`; ok is false when `epoch` is missing/unparsable.
* The collision watcher is a self-browse loop (zeroconf v1.0.0 ships no
  conflict callback): one sighting flags it. `collides()` stays true
  until you stop this registration.
* On failure modes (no interface/IPs, register error) Announce returns
  no-ops — no crash, no panic. `FindPeers` likewise returns an empty
  list, not an error path, in a degraded host.
* Loopback-only announce/browse (tests) works: zeroconf's Register()
  cannot announce loopback IPs, so the package uses RegisterProxy with
  addresses it collects itself (loopback only when explicitly picked,
  which is the test case).
