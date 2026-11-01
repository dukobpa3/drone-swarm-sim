package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/envcap"
)

const irisSDF = `<?xml version="1.0"?>
<sdf version="1.9"><model name="iris_with_ardupilot">
  <plugin name="ArduPilotPlugin" filename="ArduPilotPlugin">
    <fdm_port_in>9002</fdm_port_in>
  </plugin>
</model></sdf>`

const fieldWorld = `<sdf version="1.9"><world name="field">
  <spherical_coordinates>
    <latitude_deg>37.5</latitude_deg>
    <longitude_deg>-122.25</longitude_deg>
    <elevation>38</elevation>
  </spherical_coordinates>
</world></sdf>`

// apLab writes an ArduPilot checkout, its build and an ardupilot_gazebo with
// the iris model, and a gz whose services succeed. It returns the lab YAML
// and the path of a world with spherical coordinates.
func apLab(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{"copter.parm", "gazebo-iris.parm", "plane.parm"} {
		write(t, filepath.Join(root, "ardupilot", "Tools", "autotest", "default_params", f), "")
	}
	for _, bin := range []string{"arducopter", "arduplane"} {
		write(t, filepath.Join(root, "ardupilot", "build", "sitl", "bin", bin), "")
	}
	model(t, filepath.Join(root, "ardupilot_gazebo", "models"), "iris_with_ardupilot", irisSDF)
	write(t, filepath.Join(root, "gz", "bin", "gz"), "#!/bin/sh\necho 'data: true'\n")
	write(t, filepath.Join(root, "gz", "activate.sh"), "export PATH=\""+filepath.Join(root, "gz", "bin")+":$PATH\"\n")
	world := filepath.Join(root, "field.sdf")
	write(t, world, fieldWorld)
	return "run_dir: run\ngz: { activate: " + filepath.Join(root, "gz", "activate.sh") + " }\n" +
		"ardupilot: { root: " + filepath.Join(root, "ardupilot") + ", bin: " + filepath.Join(root, "ardupilot", "build", "sitl", "bin") +
		", gazebo: " + filepath.Join(root, "ardupilot_gazebo") + " }\n", world
}

func TestPlanArdupilotGz(t *testing.T) {
	labYAML, world := apLab(t)
	lab, prof := setup(t, labYAML, `
world: { sim: gz, name: `+world+` }
layout: { spacing: 2 }
groups:
  - { name: ap, autopilot: ardupilot, count: 2 }
`)
	specs, err := Plan(lab, prof)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(specs); got != "gz,ap-0,ap-1" {
		t.Fatalf("specs %s", got)
	}
	ap := specs[2]
	args := strings.Join(ap.Args, " ")
	for _, want := range []string{
		"--model JSON",
		"--instance 1",
		"--sysid 2",
		"--sim-address 127.0.0.1",
		// gz places the drone; SITL's home is the world's origin for all of them
		"--home 37.5000000,-122.2500000,38,0",
		"default_params/copter.parm,",
		"default_params/gazebo-iris.parm",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %s", want, args)
		}
	}
	if ap.Before == nil || ap.Ready == nil {
		t.Fatal("an ardupilot drone in gz spawns its model and gates the next drone")
	}

	if err := ap.Before(context.Background()); err != nil {
		t.Fatal(err)
	}
	sdf, err := os.ReadFile(filepath.Join(lab.RunDir, prof.Name, "ap-1", "model.sdf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sdf), "<fdm_port_in>9012</fdm_port_in>") || strings.Contains(string(sdf), "9002") {
		t.Fatalf("ap-1's model must listen on its instance's port 9012:\n%s", sdf)
	}
}

func TestPlanArdupilotGzHomeFromProfile(t *testing.T) {
	labYAML, _ := apLab(t)
	world := filepath.Join(t.TempDir(), "flat.sdf")
	write(t, world, `<sdf version="1.9"><world name="flat"/></sdf>`)
	lab, prof := setup(t, labYAML, `
world: { sim: gz, name: `+world+`, home: { lat: 47.4, lon: 8.5, alt: 488 } }
groups:
  - { name: ap, autopilot: ardupilot, count: 1 }
`)
	specs, err := Plan(lab, prof)
	if err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(specs[1].Args, " "); !strings.Contains(args, "--home 47.4000000,8.5000000,488,0") {
		t.Fatalf("world.home must win over the world: %s", args)
	}
}

func TestPlanArdupilotGzProblems(t *testing.T) {
	labYAML, world := apLab(t)
	flat := filepath.Join(t.TempDir(), "flat.sdf")
	write(t, flat, `<sdf version="1.9"><world name="flat"/></sdf>`)
	noGazebo, _, _ := strings.Cut(labYAML, ", gazebo:")
	noGazebo += " }\n"
	cases := []struct{ name, lab, profile, want string }{
		{"no gazebo in the lab", noGazebo, "", ""},
		{"unknown model", labYAML, `
world: { sim: gz, name: ` + world + ` }
groups: [ { name: ap, autopilot: ardupilot, model: x500, count: 1 } ]
`, `model "x500": swarmsim knows the frame parameters of iris_with_ardupilot`},
		{"vehicle does not match", labYAML, `
world: { sim: gz, name: ` + world + ` }
groups: [ { name: ap, autopilot: ardupilot, vehicle: plane, model: iris_with_ardupilot, count: 1 } ]
`, `is a copter, set vehicle: copter`},
		{"no origin", labYAML, `
world: { sim: gz, name: ` + flat + ` }
groups: [ { name: ap, autopilot: ardupilot, count: 1 } ]
`, `has no spherical_coordinates, set world.home`},
		{"payloads", labYAML, `
world: { sim: gz, name: ` + world + ` }
groups: [ { name: ap, autopilot: ardupilot, count: 1, payloads: [ { model: cam } ] } ]
`, `not supported for ardupilot yet`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			profile, want := c.profile, c.want
			if profile == "" {
				profile = "\nworld: { sim: gz, name: " + world + " }\ngroups: [ { name: ap, autopilot: ardupilot, count: 1 } ]\n"
				want = "ardupilot drones in gz need ardupilot.gazebo"
			}
			lab, prof := setup(t, c.lab, profile)
			if _, err := Plan(lab, prof); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want %q, got %v", want, err)
			}
		})
	}
}

func TestArdupilotGzModelNeedsOnePort(t *testing.T) {
	labYAML, world := apLab(t)
	lab, prof := setup(t, labYAML, "\nworld: { sim: gz, name: "+world+" }\ngroups: [ { name: ap, autopilot: ardupilot, count: 1 } ]\n")
	sdf := filepath.Join(lab.ArduPilot.Gazebo, "models", "iris_with_ardupilot", "model.sdf")
	write(t, sdf, `<sdf><model name="iris"/></sdf>`)
	if _, err := Plan(lab, prof); err == nil || !strings.Contains(err.Error(), "want one ArduPilotPlugin with fdm_port_in, found 0") {
		t.Fatalf("want a port error, got %v", err)
	}
}

func TestWorldOrigin(t *testing.T) {
	sdf := filepath.Join(t.TempDir(), "w.sdf")
	write(t, sdf, fieldWorld)
	got, err := worldOrigin(sdf)
	if err != nil || got != (config.Home{Lat: 37.5, Lon: -122.25, Alt: 38}) {
		t.Fatalf("origin %+v, %v", got, err)
	}
	write(t, sdf, "<sdf")
	if _, err := worldOrigin(sdf); err == nil {
		t.Fatal("want a parse error")
	}
}

func TestSetStep(t *testing.T) {
	step := func(body string) error {
		env := gzBin(t, body, "")
		gz, err := envcap.LookPath(env, "gz")
		if err != nil {
			t.Fatal(err)
		}
		return setStep(context.Background(), gz, env, "w", 0.001)
	}
	if err := step("echo 'data: true'"); err != nil {
		t.Fatal(err)
	}
	if err := step("echo 'data: false'"); err == nil || !strings.Contains(err.Error(), `physics step of "w"`) {
		t.Fatalf("want a refusal, got %v", err)
	}
}
