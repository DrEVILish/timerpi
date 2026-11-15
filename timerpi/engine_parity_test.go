package timerpi

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"
)

// The browser carries its own copy of the engine (public/src/engine.js) so a
// room keeps running while the server is unreachable. Nothing but this test
// keeps the two in step: every scenario runs through both, and the state
// after each step must match.

type parityStep struct {
	Action  string         `json:"action"`
	Args    map[string]any `json:"args,omitempty"`
	Advance int64          `json:"advance,omitempty"` // ms that pass before the step
}

type parityScenario struct {
	Name  string       `json:"name"`
	Steps []parityStep `json:"steps"`
}

type parityState struct {
	ActivePos  int64   `json:"activePos"`
	PrevPos    int64   `json:"prevPos"`
	NextPos    int64   `json:"nextPos"`
	Mode       string  `json:"mode"` // running | paused | stopped
	ElapsedMS  int64   `json:"elapsedMS"`
	Rate       float64 `json:"rate"`
	DayStartTS int64   `json:"dayStartTS"`
	Labels     string  `json:"labels"`
}

type parityCue struct {
	Label      string `json:"label"`
	DurationMS int64  `json:"durationMS"`
	Kind       string `json:"kind"`
	TimerKind  string `json:"timerKind"`
	EndAction  string `json:"endAction"`
}

const parityT0 = int64(1_700_000_000_000)

var parityCues = []parityCue{
	{"One", 600_000, KindSession, TimerCountdown, EndHold},
	{"Two", 1_200_000, KindSession, TimerCountdown, EndOvertime},
	{"Tea", 300_000, KindBreak, TimerCountdown, EndHold},
	{"Four", 60_000, KindSession, TimerCountdown, EndBlank},
	{"Five", 100_000, KindSession, TimerCountStop, EndHold},
}

func pStep(action string, advance int64, kv ...any) parityStep {
	s := parityStep{Action: action, Advance: advance}
	for i := 0; i+1 < len(kv); i += 2 {
		if s.Args == nil {
			s.Args = map[string]any{}
		}
		s.Args[kv[i].(string)] = kv[i+1]
	}
	return s
}

var parityScenarios = []parityScenario{
	{"go walks the list and stops at the end", []parityStep{
		pStep("go", 0), pStep("go", 5_000), pStep("go", 5_000), pStep("go", 5_000), pStep("go", 5_000),
		pStep("go", 5_000), // past the last cue: nothing happens
	}},
	{"go on a row starts that row", []parityStep{pStep("go", 0, "pos", 3.0), pStep("go", 1_000, "pos", 1.0)}},
	{"start with no cue chosen", []parityStep{pStep("start", 0), pStep("start", 4_000)}},
	{"pause and resume keep the elapsed time", []parityStep{
		pStep("go", 0), pStep("pause", 10_000), pStep("tick", 30_000), pStep("pause", 0), pStep("tick", 5_000),
		pStep("pause", 0), pStep("resume", 2_000), pStep("tick", 1_000),
	}},
	{"go while paused starts the next cue", []parityStep{pStep("go", 0), pStep("pause", 3_000), pStep("go", 3_000)}},
	{"next and prev arm, they never start", []parityStep{
		pStep("next", 0), pStep("next", 0), pStep("prev", 0), pStep("prev", 0), pStep("prev", 0),
		pStep("go", 0), pStep("next", 2_000), pStep("tick", 2_000), pStep("go", 0),
	}},
	{"next on the last cue stays", []parityStep{pStep("go", 0, "pos", 5.0), pStep("next", 1_000), pStep("tick", 1_000)}},
	{"cue up arms, jump with start runs", []parityStep{
		pStep("go", 0), pStep("jump", 2_000, "pos", 3.0, "start", "false"), pStep("tick", 2_000),
		pStep("jump", 0, "pos", 2.0), pStep("jump", 0, "pos", 4.0, "start", true), pStep("jump", 1_000, "pos", 9.0),
	}},
	{"rate re-anchors and is held to x0.5-x2", []parityStep{
		pStep("go", 0), pStep("rate", 10_000, "rate", 2.0), pStep("tick", 10_000),
		pStep("rate", 0, "rate", 9.0), pStep("tick", 10_000), pStep("rate", 0, "rate", 0.1), pStep("tick", 10_000),
		pStep("rate", 0, "rate", 0.0), pStep("pause", 0), pStep("rate", 5_000, "rate", 1.0), pStep("tick", 5_000),
	}},
	{"hold freezes at zero, go runs it again", []parityStep{
		pStep("go", 0), pStep("tick", 599_000), pStep("tick", 2_000), pStep("tick", 60_000),
		pStep("pause", 0), pStep("resume", 0), pStep("start", 0), pStep("go", 1_000), pStep("tick", 1_000),
	}},
	{"go on the row of a held cue runs it again", []parityStep{
		pStep("go", 0, "pos", 1.0), pStep("tick", 601_000), pStep("go", 1_000, "pos", 1.0), pStep("tick", 1_000),
		pStep("pause", 0), pStep("go", 1_000, "pos", 1.0), pStep("go", 1_000, "pos", 1.0),
	}},
	{"overtime keeps counting", []parityStep{pStep("go", 0, "pos", 2.0), pStep("tick", 1_300_000), pStep("go", 0)}},
	{"overtime resumes after zero with either transport command", []parityStep{
		pStep("go", 0, "pos", 2.0), pStep("tick", 1_300_000), pStep("pause", 0),
		pStep("pause", 5_000), pStep("tick", 1_000), pStep("pause", 0), pStep("resume", 5_000), pStep("tick", 1_000),
	}},
	{"count-up resumes past its nominal duration with either command", []parityStep{
		pStep("go", 0, "pos", 5.0), pStep("tick", 500_000), pStep("pause", 0),
		pStep("pause", 5_000), pStep("tick", 1_000), pStep("pause", 0), pStep("resume", 5_000), pStep("tick", 1_000),
	}},
	{"blank freezes at zero", []parityStep{pStep("go", 0, "pos", 4.0), pStep("tick", 61_000), pStep("go", 5_000)}},
	{"count-up never crosses", []parityStep{pStep("go", 0, "pos", 5.0), pStep("tick", 500_000)}},
	{"the day starts so the first cue run is on plan", []parityStep{pStep("go", 0, "pos", 3.0), pStep("go", 1_000)}},
	{"reorders keep the running cue", []parityStep{
		pStep("go", 0, "pos", 3.0),
		pStep("cueMove", 1_000, "pos", 4.0, "to", 1.0), pStep("cueMove", 1_000, "pos", 4.0, "to", 1.0),
		pStep("cueMove", 1_000, "pos", 1.0, "dir", "down"), pStep("cueMove", 0, "pos", 1.0, "dir", "up"),
		pStep("cueMove", 0, "pos", 2.0, "to", 99.0), pStep("cueDup", 1_000, "pos", 5.0),
		pStep("cueAdd", 0, "label", "New", "durationMS", 120_000.0), pStep("cueDel", 1_000, "pos", 1.0),
		pStep("tick", 1_000), pStep("go", 0),
	}},
	{"deleting the running cue arms its slot", []parityStep{
		pStep("go", 0, "pos", 2.0), pStep("cueDel", 5_000, "pos", 2.0), pStep("tick", 1_000), pStep("go", 0),
		pStep("cueDel", 1_000, "pos", 4.0), pStep("cueDel", 0, "pos", 3.0), pStep("go", 0),
	}},
	{"deleting the last cue while it runs", []parityStep{pStep("go", 0, "pos", 5.0), pStep("cueDel", 1_000, "pos", 5.0), pStep("go", 0)}},
	{"an empty list", []parityStep{
		pStep("cueDel", 0, "pos", 1.0), pStep("cueDel", 0, "pos", 1.0), pStep("cueDel", 0, "pos", 1.0),
		pStep("cueDel", 0, "pos", 1.0), pStep("go", 0, "pos", 1.0), pStep("cueDel", 0, "pos", 1.0),
		pStep("go", 0), pStep("next", 0), pStep("cueDel", 0, "pos", 1.0),
	}},
}

// parityGo runs one scenario through the server engine. Commands take the
// same route as over the socket (ws/commands.go): transport goes to the
// engine, cue changes go to the database and the engine is told.
func parityGo(t *testing.T, sc parityScenario) []parityState {
	t.Helper()
	db := openTestDB(t)
	show := mustCreateShow(t, db, "Parity")
	for _, c := range parityCues {
		if _, err := db.CreateCue(show.ID, Cue{Label: c.Label, DurationMS: c.DurationMS, Kind: c.Kind, TimerKind: c.TimerKind, EndAction: c.EndAction}); err != nil {
			t.Fatalf("CreateCue: %v", err)
		}
	}
	clk := &fakeClock{ms: parityT0}
	deps := db.EngineDeps(show.ID)
	deps.Now = clk.Now
	e, err := NewEngine(show.ID, deps)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	var out []parityState
	for _, st := range sc.Steps {
		clk.advance(st.Advance)
		pos := argInt(st.Args, "pos")
		isTrue := st.Args["start"] == true || st.Args["start"] == "true" // ws argBool
		// A refused command changes nothing; engine.js answers 'ignored'.
		switch st.Action {
		case "tick":
			_ = e.Tick(clk.Now())
		case "pause":
			if e.Runtime().Paused {
				_ = e.ApplyCmd("resume", st.Args)
			} else {
				_ = e.ApplyCmd("pause", st.Args)
			}
		case "jump":
			if isTrue {
				_ = e.ApplyCmd("start", st.Args)
			} else {
				_ = e.ApplyCmd("jump", st.Args)
			}
		case "cueAdd":
			_, _ = db.CreateCue(show.ID, Cue{Label: st.Args["label"].(string), DurationMS: argInt(st.Args, "durationMS")})
			_ = e.Notify()
		case "cueDel":
			_ = db.DeleteCue(show.ID, pos)
			_ = e.Notify()
		case "cueDup":
			_, _ = db.DuplicateCue(show.ID, pos)
			_ = e.Notify()
		case "cueMove":
			to, ok := ArgInt64(st.Args, "to")
			if !ok {
				to = pos + 1
				if st.Args["dir"] == "up" {
					to = pos - 1
				}
			}
			_ = db.MoveCue(show.ID, pos, to)
			_ = e.Notify()
		default:
			_ = e.ApplyCmd(st.Action, st.Args)
		}
		rt := e.Runtime()
		cues, err := db.ListCues(show.ID)
		if err != nil {
			t.Fatalf("ListCues: %v", err)
		}
		s := parityState{ActivePos: rt.ActivePos, PrevPos: rt.PrevPos, NextPos: rt.NextPos, Mode: "stopped",
			ElapsedMS: cueElapsedMS(rt, clk.Now()), Rate: rt.Rate, DayStartTS: rt.DayStartTS}
		if rt.Paused {
			s.Mode = "paused"
		} else if rt.Running {
			s.Mode = "running"
		}
		for i, c := range cues {
			if i > 0 {
				s.Labels += ","
			}
			s.Labels += c.Label
		}
		out = append(out, s)
	}
	return out
}

func TestEngineParityWithJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the engine.js half of the parity test cannot run")
	}
	in, _ := json.Marshal(map[string]any{"t0": parityT0, "cues": parityCues, "scenarios": parityScenarios})
	cmd := exec.Command(node, "testdata/engine_parity.mjs")
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("engine_parity.mjs: %v\n%s", err, stderr.String())
	}
	var js [][]parityState
	if err := json.Unmarshal(raw, &js); err != nil {
		t.Fatalf("engine_parity.mjs output: %v\n%s", err, raw)
	}
	for i, sc := range parityScenarios {
		t.Run(sc.Name, func(t *testing.T) {
			got := parityGo(t, sc)
			for k := range sc.Steps {
				if !reflect.DeepEqual(got[k], js[i][k]) {
					t.Errorf("step %d %s %v:\n  engine.go %+v\n  engine.js %+v", k+1, sc.Steps[k].Action, sc.Steps[k].Args, got[k], js[i][k])
				}
			}
		})
	}
}
