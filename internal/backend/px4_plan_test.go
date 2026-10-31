package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dukobpa3/drone-swarm-sim/internal/envcap"
)

// fakePX4 writes a PX4 SITL build with the gz_x500 airframe and a stub gz
// whose create service accepts everything. It returns the lab YAML.
func fakePX4(t *testing.T) string {
	t.Helper()
	build := t.TempDir()
	write(t, filepath.Join(build, "bin", "px4"), "#!/bin/sh\n")
	write(t, filepath.Join(build, "etc", "init.d-posix", "airframes", "4001_gz_x500"), "")
	write(t, filepath.Join(build, "gz_env.sh"), "")
	gz := t.TempDir()
	write(t, filepath.Join(gz, "bin", "gz"), "#!/bin/sh\necho \"data: true\"\n")
	write(t, filepath.Join(gz, "activate.sh"), "export PATH=\""+filepath.Join(gz, "bin")+":$PATH\"\n")
	return "run_dir: run\ngz: { activate: " + filepath.Join(gz, "activate.sh") + " }\npx4: { build: " + build + " }\n"
}

func TestPlanPX4WithPayloads(t *testing.T) {
	models := t.TempDir()
	model(t, models, "x500", `<sdf><model name="x500"><link name="base_link"/></model></sdf>`)
	model(t, models, "thermal_cam", `<sdf><model name="t"><link name="thermal_link"/></model></sdf>`)
	world := filepath.Join(t.TempDir(), "field.sdf")
	write(t, world, "<sdf/>")

	lab, prof := setup(t, fakePX4(t), `
world: { sim: gz, name: `+world+` }
model_paths: [`+models+`]
groups:
  - name: thermal
    autopilot: px4
    model: gz_x500
    count: 1
    payloads: [{ name: ir, model: thermal_cam, pose: [0.12, 0, 0.24] }]
  - { name: plain, autopilot: px4, model: gz_x500, count: 1 }
`)
	specs, err := Plan(lab, prof)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(specs); got != "gz,thermal-0,plain-0" {
		t.Fatalf("specs %s", got)
	}
	if !strings.HasPrefix(envcap.Lookup(specs[1].Env, "GZ_SIM_RESOURCE_PATH"), models) {
		t.Errorf("profile models are not on the gz resource path: %q", envcap.Lookup(specs[1].Env, "GZ_SIM_RESOURCE_PATH"))
	}

	thermal, plain := specs[1], specs[2]
	if got := envcap.Lookup(thermal.Env, "PX4_GZ_MODEL_NAME"); got != "thermal-0" {
		t.Errorf("thermal drone attaches to %q", got)
	}
	if envcap.Lookup(thermal.Env, "PX4_GZ_MODEL_POSE") != "" {
		t.Error("a drone swarmsim spawns must not ask PX4 to spawn it too")
	}
	if thermal.Before == nil {
		t.Fatal("thermal drone has no spawn step")
	}
	if err := thermal.Before(context.Background()); err != nil {
		t.Fatal(err)
	}
	sdf, err := os.ReadFile(filepath.Join(thermal.Dir, "model.sdf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sdf), "<child>ir::thermal_link</child>") {
		t.Errorf("spawned SDF lacks the payload joint:\n%s", sdf)
	}

	if plain.Before != nil || envcap.Lookup(plain.Env, "PX4_GZ_MODEL_POSE") == "" {
		t.Error("a drone without payloads is spawned by PX4 at its pose")
	}
}

func TestPlanPX4PayloadProblems(t *testing.T) {
	world := filepath.Join(t.TempDir(), "field.sdf")
	write(t, world, "<sdf/>")
	lab, prof := setup(t, fakePX4(t), `
world: { sim: gz, name: `+world+` }
model_paths: [`+t.TempDir()+`]
groups:
  - { name: t, autopilot: px4, model: gz_x500, count: 1, base: x500, payloads: [{ model: thermal_cam }] }
`)
	_, err := Plan(lab, prof)
	if err == nil || !strings.Contains(err.Error(), `group "t": base: model "x500" not found`) {
		t.Fatalf("want a missing base error, got %v", err)
	}
}
