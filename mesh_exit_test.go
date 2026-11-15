package main

import (
	"errors"
	"testing"
	"timerpi/meshradio"
)

func TestMeshApplyExitCode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		legs    []meshradio.Leg
		saveErr error
		want    int
	}{
		{"no usable radio", nil, nil, 1},
		{"every join failed", []meshradio.Leg{{Error: "invalid argument"}, {Error: "unsupported"}}, nil, 1},
		{"one working leg", []meshradio.Leg{{Error: "unsupported"}, {}}, nil, 0},
		{"working leg", []meshradio.Leg{{}}, nil, 0},
		{"status persistence failed", []meshradio.Leg{{}}, errors.New("read-only status path"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := meshApplyExitCode(meshradio.Status{Legs: tc.legs}, tc.saveErr); got != tc.want {
				t.Fatalf("command exit=%d, want%d", got, tc.want)
			}
		})
	}
}
