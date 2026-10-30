package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/supervisor"
)

func planFixture() (*config.Lab, *config.Profile, []config.Drone, []supervisor.Spec) {
	px4 := &config.Group{Name: "x", Autopilot: "px4", Sim: "gz"}
	ap := &config.Group{Name: "a", Autopilot: "ardupilot", Sim: "builtin"}
	drones := []config.Drone{
		{Group: px4, Name: "x-0", Index: 0, SysID: 1},
		{Group: ap, Name: "a-0", Index: 1, SysID: 2, X: 3},
	}
	specs := []supervisor.Spec{{Name: "x-0", Path: "/bin/px4", Args: []string{"-i", "0"}}}
	return &config.Lab{RunDir: "/run"}, &config.Profile{Name: "p"}, drones, specs
}

func TestPrintPlan(t *testing.T) {
	var out bytes.Buffer
	lab, prof, drones, specs := planFixture()
	if err := printPlan(&out, lab, prof, drones, specs); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"run dir: /run/p",
		"a-0    ardupilot  builtin  2",
		"api udp 14580->14540",
		"api tcp 5772",
		"/bin/px4 -i 0",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, out.String())
		}
	}
}

type failAfter struct{ n int }

func (f *failAfter) Write(p []byte) (int, error) {
	if f.n == 0 {
		return 0, errors.New("disk full")
	}
	f.n--
	return len(p), nil
}

func TestPrintPlanReportsWriteErrors(t *testing.T) {
	lab, prof, drones, specs := planFixture()
	for n := 0; n < 4; n++ {
		if err := printPlan(&failAfter{n: n}, lab, prof, drones, specs); err == nil {
			t.Errorf("write failing after %d writes: no error", n)
		}
	}
}

func TestPrinterStopsAtFirstError(t *testing.T) {
	w := &failAfter{n: 1}
	p := &printer{w: w}
	p.printf("one")
	p.printf("two")
	p.printf("three")
	if p.err == nil || w.n != 0 {
		t.Fatalf("err %v, writes left %d", p.err, w.n)
	}
}

func TestPorts(t *testing.T) {
	g := &config.Group{Autopilot: "exec"}
	if got := ports(config.Drone{Group: g}); got != "-" {
		t.Fatalf("exec ports %q", got)
	}
}
