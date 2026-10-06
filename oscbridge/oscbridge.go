// Package oscbridge — OSC wiring between TimerPi and show peers (proposal
// #8, OSC only). Two unidirectional channels over minimal OSC 1.0 UDP:
//
//   - INBOUND: a UDP listener maps /timerpi/<code>/<verb> to engine verbs
//     (go/pause/resume/reset/next/prev/blank/unblank[/start]) so QLab,
//     light desks and Companion can drive TimerPi.
//   - OUTBOUND: TimerPi cue fires and BLANK events go out as QLab-OSC
//     (/cue/<pos>/start, /panic, /go) — the CuTePi destination-display
//     channel: CuTePi listens on QLab UDP 53000 (its DESIGN §12.8) and
//     fires media cues in lockstep with the session timer.
//
// Trust boundary (same as CuTePi): neither direction authenticates — an
// OSC protocol limitation. Both are OFF until configured and meant for the
// control-room LAN. This is deliberately unlike TimerPi's WebSocket
// surface, which enforces the operator password: enabling the inbound
// listener is an explicit choice to trust that LAN (documented).
package oscbridge

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
)

const bundleAddr = "#bundle"

// Message is one parsed OSC address pattern + typed args.
type Message struct {
	Address string
	Args    []any // string | int32 | float32 | bool
}

func allZeros(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func pad4(n int) []byte {
	if r := n % 4; r != 0 {
		return make([]byte, 4-r)
	}
	return nil
}

// oscStringLen is the padded wire length of an OSC string: chars + NUL +
// padding to a 4-byte boundary.
func oscStringLen(s string) int {
	return ((len(s) + 4) / 4) * 4
}

// oscString encodes one OSC string: chars, NUL terminator, then pad until
// the field is 4-aligned. The WIP omitted the terminator as its own byte,
// leaving every field one byte short.
func oscString(s string) []byte {
	b := make([]byte, 0, oscStringLen(s))
	b = append(b, s...)
	b = append(b, 0)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// Parse decodes one OSC packet (message form only; bundles answer an error).
func Parse(raw []byte) (Message, error) {
	var m Message
	if len(raw) > 0 && raw[0] == '#' {
		return m, fmt.Errorf("oscbridge: bundle frames unsupported")
	}
	// Address: chars up to its NUL; the whole string field (chars + NUL +
	// pad) is 4-aligned and its padding is all NULs.
	nul := bytes.Index(raw, []byte{0})
	if nul < 1 {
		return m, fmt.Errorf("oscbridge: address not NUL-terminated")
	}
	field := oscStringLen(string(raw[:nul]))
	if field > len(raw) || !allZeros(raw[nul:field]) {
		return m, fmt.Errorf("oscbridge: address not NUL-4 aligned")
	}
	m.Address = string(raw[:nul])
	pos := field
	if pos >= len(raw) || raw[pos] != ',' {
		return m, fmt.Errorf("oscbridge: type-tag string missing")
	}
	tagEnd := bytes.Index(raw[pos:], []byte{0})
	if tagEnd < 1 {
		return m, fmt.Errorf("oscbridge: unterminated type tags")
	}
	tfield := oscStringLen(string(raw[pos+1 : pos+tagEnd]))
	if pos+tfield > len(raw) || !allZeros(raw[pos+tagEnd:pos+tfield]) {
		return m, fmt.Errorf("oscbridge: type tags not NUL-4 aligned")
	}
	tags := raw[pos+1 : pos+tagEnd]
	pos += tfield
	if pos > len(raw) {
		pos = len(raw)
	}
	for _, tag := range string(tags) {
		switch tag {
		case 'i':
			if pos+4 > len(raw) {
				return m, fmt.Errorf("oscbridge: truncated int arg")
			}
			m.Args = append(m.Args, int32(binary.BigEndian.Uint32(raw[pos:pos+4])))
			pos += 4
		case 'f':
			if pos+4 > len(raw) {
				return m, fmt.Errorf("oscbridge: truncated float arg")
			}
			bits := binary.BigEndian.Uint32(raw[pos : pos+4])
			m.Args = append(m.Args, math.Float32frombits(bits))
			pos += 4
		case 's':
			rest := raw[pos:]
			end := bytes.Index(rest, []byte{0})
			if end < 0 {
				return m, fmt.Errorf("oscbridge: truncated string arg")
			}
			val := string(rest[:end])
			// The padded field must fit too, or the next arg would slice
			// past the end of the packet (BUGLOG RC1).
			if pos+oscStringLen(val) > len(raw) {
				return m, fmt.Errorf("oscbridge: string arg padding truncated")
			}
			m.Args = append(m.Args, val)
			pos += oscStringLen(val)
		case 'T':
			m.Args = append(m.Args, true)
		case 'F':
			m.Args = append(m.Args, false)
		default:
			return m, fmt.Errorf("oscbridge: unsupported arg tag %q", string(tag))
		}
	}
	return m, nil
}

// Build encodes one OSC message.
func Build(address string, args ...any) ([]byte, error) {
	if !strings.HasPrefix(address, "/") {
		return nil, fmt.Errorf("oscbridge: address must start with /: %q", address)
	}
	var tags []byte
	tags = append(tags, ',')
	var body bytes.Buffer
	for _, a := range args {
		switch v := a.(type) {
		case string:
			tags = append(tags, 's')
			body.Write(oscString(v))
		case int:
			tags = append(tags, 'i')
			var w [4]byte
			binary.BigEndian.PutUint32(w[:], uint32(v))
			body.Write(w[:])
		case int32:
			tags = append(tags, 'i')
			var w [4]byte
			binary.BigEndian.PutUint32(w[:], uint32(v))
			body.Write(w[:])
		case float32:
			tags = append(tags, 'f')
			var w [4]byte
			binary.BigEndian.PutUint32(w[:], math.Float32bits(v))
			body.Write(w[:])
		case float64:
			tags = append(tags, 'f')
			var w [4]byte
			binary.BigEndian.PutUint32(w[:], math.Float32bits(float32(v)))
			body.Write(w[:])
		case bool:
			if v {
				tags = append(tags, 'T')
			} else {
				tags = append(tags, 'F')
			}
		default:
			return nil, fmt.Errorf("oscbridge: unsupported arg type %T", a)
		}
	}
	var b bytes.Buffer
	b.Write(oscString(address))
	b.Write(oscString(string(tags)))
	b.Write(body.Bytes())
	return b.Bytes(), nil
}

// ---------------------------------------------------------------------------
// Verb grammar (inbound): /timerpi/<code>/<verb> — code addresses the show
// (share code, same as /d/), verb is an engine verb. A leading numeric arg
// (int/f) on any verb becomes the "start" pos arg.

func VerbMap(m Message) (code, verb string, pos int64) {
	parts := strings.Split(strings.TrimSuffix(m.Address, "/"), "/")
	if len(parts) != 4 || parts[1] != "timerpi" {
		return "", "", 0
	}
	code, verb = parts[2], strings.ToLower(parts[3])
	for _, a := range m.Args {
		if n, ok := numberArg(a); ok && n > 0 {
			return code, verb, n
		}
	}
	return code, verb, 0
}

func numberArg(a any) (int64, bool) {
	switch v := a.(type) {
	case int32:
		return int64(v), true
	case int:
		return int64(v), true
	case float32:
		return int64(v), true
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Inbound listener

// Inbound is the UDP listener; SetInbound swaps (or stops) it atomically.
type Inbound struct {
	mu   sync.Mutex
	conn *net.UDPConn
}

// SetInbound (re)starts the listener on addr ("" stops it). Every parsed
// message goes to dispatch; parse errors go to report, never to the wire.
func (in *Inbound) SetInbound(addr string, dispatch func(m Message), report func(error)) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.conn != nil {
		_ = in.conn.Close()
		in.conn = nil
	}
	if addr == "" {
		return nil
	}
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("oscbridge: resolve %s: %w", addr, err)
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return fmt.Errorf("oscbridge: listen %s: %w", addr, err)
	}
	in.conn = conn
	go func() {
		buf := make([]byte, 4096)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return // replaced/closed
			}
			handlePacket(buf[:n], dispatch, report)
		}
	}()
	return nil
}

// ---------------------------------------------------------------------------
// Outbound: QLab-style fire-and-forget UDP (QLab's UDP mode has no replies)

// Target returns the current outbound UDP address ("" disables). Set once
// at boot from settings by main; read per event (human-rate).
var Target func() string

// FireOut routes one transport event to the configured outbound peer.
// kind: "cue" (pos = cue position), "panic", "go".
func FireOut(kind string, pos int64) {
	if Target == nil || kind == "" {
		return
	}
	host := Target()
	if host == "" {
		return
	}
	a := OutAddress(kind, pos)
	if a == "" {
		return
	}
	_ = Send(host, a)
}

// OutAddress returns the QLab grammar for a cue fire / transport event.
func OutAddress(kind string, pos int64) string {
	switch kind {
	case "cue":
		return fmt.Sprintf("/cue/%d/start", pos)
	case "panic":
		return "/panic"
	case "go":
		return "/go"
	case "stop":
		return "/stop"
	default:
		return ""
	}
}

// Send delivers one fire-and-forget OSC message; local errors are returned
// (callers log), unreachable peers cost one system call, never a hang.
func Send(addr string, address string, args ...any) error {
	raw, err := Build(address, args...)
	if err != nil {
		return err
	}
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("oscbridge: resolve %s: %w", addr, err)
	}
	conn, err := net.DialUDP("udp", nil, ua)
	if err != nil {
		return fmt.Errorf("oscbridge: dial %s: %w", addr, err)
	}
	defer conn.Close()
	// A lost datagram on a LAN fire-and-forget is the protocol's normal
	// failure mode — never block the show on it.
	_, _ = conn.Write(raw)
	return nil
}

// handlePacket parses and dispatches one datagram. A panic in either step
// is reported, not fatal: one bad packet must never take the box down.
func handlePacket(raw []byte, dispatch func(Message), report func(error)) {
	defer func() {
		if r := recover(); r != nil && report != nil {
			report(fmt.Errorf("oscbridge: packet handler panic: %v", r))
		}
	}()
	m, err := Parse(raw)
	if err != nil {
		if report != nil {
			report(err)
		}
		return
	}
	if dispatch != nil {
		dispatch(m)
	}
}
