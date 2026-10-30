package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/envcap"
	"github.com/dukobpa3/drone-swarm-sim/internal/supervisor"
)

// setup writes lab.yaml and profiles/<name>.yaml into a temp dir and loads
// them. lab is the YAML body of the lab file.
func setup(t *testing.T, lab, profile string) (*config.Lab, *config.Profile) {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "lab.yaml"), lab)
	write(t, filepath.Join(dir, "profiles", "test.yaml"), profile)
	l, err := config.LoadLab(filepath.Join(dir, "lab.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := config.LoadProfile(filepath.Join(dir, "profiles", "test.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return l, p
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeGz writes an activate script that puts a stub gz on PATH, so the gz
// code paths run without Gazebo installed. It returns the script path.
func fakeGz(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "bin", "gz"), "#!/bin/sh\nexit 0\n")
	activate := filepath.Join(dir, "activate.sh")
	write(t, activate, "export PATH=\""+filepath.Join(dir, "bin")+":$PATH\"\n")
	return activate
}

func names(specs []supervisor.Spec) string {
	var n []string
	for _, s := range specs {
		n = append(n, s.Name)
	}
	return strings.Join(n, ",")
}

func TestPlanExecWithoutWorld(t *testing.T) {
	lab, prof := setup(t, "run_dir: run\n", `
world: { sim: none }
groups:
  - { name: e, autopilot: exec, sim: none, count: 2, command: [sleep, "{{.SysID}}"] }
`)
	specs, err := Plan(lab, prof)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(specs); got != "e-0,e-1" {
		t.Fatalf("specs %s", got)
	}
	if got := specs[1].Args; len(got) != 1 || got[0] != "2" {
		t.Fatalf("template not expanded: %v", got)
	}
}

func TestPlanCollectsAllProblems(t *testing.T) {
	lab, prof := setup(t, "run_dir: run\n", `
world: { sim: bogus }
groups:
  - { name: a, autopilot: nope, sim: none, count: 1 }
  - { name: b, autopilot: exec, sim: none, count: 2 }
  - { name: c, autopilot: px4, sim: none, model: gz_x500, count: 1 }
`)
	_, err := Plan(lab, prof)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{
		`world.sim "bogus"`,
		`group "a": autopilot "nope"`,
		`group "b": exec needs a command`,
		`group "c": px4 is not set up`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
	// each group is checked once, however many drones it has
	if n := strings.Count(err.Error(), `group "b"`); n != 1 {
		t.Errorf("group b reported %d times", n)
	}
}

func TestPlanGzWorld(t *testing.T) {
	activate := fakeGz(t)
	sdf := filepath.Join(t.TempDir(), "field.sdf")
	write(t, sdf, "<sdf/>")
	lab, prof := setup(t, "run_dir: run\ngz: { activate: "+activate+" }\n", `
world: { sim: gz, name: `+sdf+`, gui: true }
groups:
  - { name: e, autopilot: exec, count: 1, command: [gz, "{{.World}}"] }
`)
	specs, err := Plan(lab, prof)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(specs); got != "gz,gz-gui,e-0" {
		t.Fatalf("specs %s", got)
	}
	if !specs[0].Critical || specs[0].Ready == nil {
		t.Fatal("gz server must be critical and gate the drones")
	}
	if got := specs[0].Args[len(specs[0].Args)-1]; got != sdf {
		t.Fatalf("world sdf %q, want %q", got, sdf)
	}
	drone := specs[2]
	if !strings.HasSuffix(drone.Path, "/bin/gz") || drone.Args[0] != "field" {
		t.Fatalf("gz drone should run with the gz env: %s %v", drone.Path, drone.Args)
	}
	if !strings.Contains(envcap.Lookup(drone.Env, "PATH"), filepath.Dir(drone.Path)) {
		t.Fatal("gz drone env lacks the activate script's PATH")
	}
}

func TestPlanGzGroupNeedsGzWorld(t *testing.T) {
	activate := fakeGz(t)
	lab, prof := setup(t, "run_dir: run\ngz: { activate: "+activate+" }\n", `
world: { sim: none }
groups:
  - { name: e, autopilot: exec, sim: gz, count: 1, command: [sleep, "1"] }
`)
	_, err := Plan(lab, prof)
	if err == nil || !strings.Contains(err.Error(), "world.sim is not gz") {
		t.Fatalf("want world.sim error, got %v", err)
	}
}

func TestPlanGzActivateMissing(t *testing.T) {
	lab, prof := setup(t, "run_dir: run\ngz: { activate: /nonexistent/activate.sh }\n", `
world: { sim: gz }
groups:
  - { name: e, autopilot: exec, count: 1, command: [sleep, "1"] }
`)
	_, err := Plan(lab, prof)
	if err == nil || !strings.Contains(err.Error(), "env script") {
		t.Fatalf("want env script error, got %v", err)
	}
}

func TestPlanGzWorldWithoutWorldsDir(t *testing.T) {
	activate := fakeGz(t)
	lab, prof := setup(t, "run_dir: run\ngz: { activate: "+activate+" }\n", `
world: { sim: gz, name: baylands }
groups:
  - { name: e, autopilot: exec, count: 1, command: [sleep, "1"] }
`)
	_, err := Plan(lab, prof)
	if err == nil || !strings.Contains(err.Error(), "no worlds directory") {
		t.Fatalf("want worlds directory error, got %v", err)
	}
}
