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

// model writes a gz model directory with a model.config and an SDF whose
// body is sdf.
func model(t *testing.T, root, name, sdf string) {
	t.Helper()
	write(t, filepath.Join(root, name, "model.config"), "<model><name>"+name+"</name><sdf version=\"1.9\">model.sdf</sdf></model>")
	write(t, filepath.Join(root, name, "model.sdf"), sdf)
}

// gzBin writes a stub gz whose body is a shell script and returns an env
// that finds it and the given models.
func gzBin(t *testing.T, body, models string) []string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "gz"), "#!/bin/sh\n"+body+"\n")
	return envcap.With(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "GZ_SIM_RESOURCE_PATH="+models)
}

func TestFirstLink(t *testing.T) {
	root := t.TempDir()
	model(t, root, "cam", `<sdf><model name="cam"><link name="lens"/><link name="body"/></model></sdf>`)
	if got, err := firstLink(filepath.Join(root, "cam")); err != nil || got != "lens" {
		t.Fatalf("first link %q, %v", got, err)
	}

	// model.config names the SDF file; without it model.sdf is used
	write(t, filepath.Join(root, "other", "model.config"), "<model><sdf>other.sdf</sdf></model>")
	write(t, filepath.Join(root, "other", "other.sdf"), `<sdf><model name="o"><link name="l"/></model></sdf>`)
	if got, err := firstLink(filepath.Join(root, "other")); err != nil || got != "l" {
		t.Fatalf("named sdf: %q, %v", got, err)
	}

	model(t, root, "empty", `<sdf><model name="e"/></sdf>`)
	if _, err := firstLink(filepath.Join(root, "empty")); err == nil || !strings.Contains(err.Error(), "no link to mount") {
		t.Fatalf("want no link error, got %v", err)
	}
	model(t, root, "broken", `<sdf><model`)
	if _, err := firstLink(filepath.Join(root, "broken")); err == nil {
		t.Fatal("want a parse error")
	}
	if _, err := firstLink(filepath.Join(root, "missing")); err == nil {
		t.Fatal("want a read error")
	}
}

func TestCheckVehicle(t *testing.T) {
	root := t.TempDir()
	model(t, root, "x500", `<sdf><model name="x500"><link name="base_link"/></model></sdf>`)
	model(t, root, "cam", `<sdf><model name="cam"><link name="lens"/></model></sdf>`)
	env := &Env{Gz: envcap.With(nil, "GZ_SIM_RESOURCE_PATH=:"+root)}

	g := &config.Group{Sim: "gz", Payloads: []config.Payload{{Name: "ir", Model: "cam", Pose: []float64{0.1, 0, 0.2}}}}
	mounts, err := checkVehicle(env, g, "x500")
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 || mounts[0].link != "lens" {
		t.Fatalf("mounts %+v", mounts)
	}

	for _, tc := range []struct {
		name string
		g    config.Group
		base string
		want string
	}{
		{"sim", config.Group{Sim: "sih"}, "x500", "payloads need sim gz"},
		{"base", config.Group{Sim: "gz"}, "x600", `base: model "x600" not found in ` + root},
		{"no model", config.Group{Sim: "gz", Payloads: []config.Payload{{Name: "a"}}}, "x500", "no model"},
		{"name", config.Group{Sim: "gz", Payloads: []config.Payload{{Name: "a b", Model: "cam"}}}, "x500", "use letters, digits"},
		{"twice", config.Group{Sim: "gz", Payloads: []config.Payload{{Name: "a", Model: "cam"}, {Name: "a", Model: "cam"}}}, "x500", "used twice"},
		{"pose", config.Group{Sim: "gz", Payloads: []config.Payload{{Name: "a", Model: "cam", Pose: []float64{1, 2}}}}, "x500", "pose is x y z"},
		{"payload model", config.Group{Sim: "gz", Payloads: []config.Payload{{Name: "a", Model: "nope"}}}, "x500", `payload "a": model "nope" not found`},
	} {
		if _, err := checkVehicle(env, &tc.g, tc.base); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want %q, got %v", tc.name, tc.want, err)
		}
	}

	model(t, root, "nolink", `<sdf><model name="n"/></sdf>`)
	g = &config.Group{Sim: "gz", Payloads: []config.Payload{{Name: "a", Model: "nolink"}}}
	if _, err := checkVehicle(env, g, "x500"); err == nil || !strings.Contains(err.Error(), `payload "a"`) {
		t.Errorf("want a payload link error, got %v", err)
	}
}

func TestComposeModel(t *testing.T) {
	sdf := string(composeModel("thermal-0", "x500", []mount{
		{Payload: config.Payload{Name: "ir", Model: "thermal_cam", Pose: []float64{0.12, 0, 0.24}}, link: "thermal_link"},
	}))
	for _, want := range []string{
		`<model name="thermal-0">`,
		`<include merge="true">`,
		`<uri>model://x500</uri>`,
		`<name>ir</name>`,
		`<pose relative_to="base_link">0.12 0 0.24 0 0 0</pose>`,
		`<joint name="ir_mount" type="fixed">`,
		`<parent>base_link</parent>`,
		`<child>ir::thermal_link</child>`,
	} {
		if !strings.Contains(sdf, want) {
			t.Errorf("composed SDF lacks %q:\n%s", want, sdf)
		}
	}
	if s := string(composeModel(`a"<b`, "x500", nil)); !strings.Contains(s, `<model name="a&#34;&lt;b">`) {
		t.Errorf("name not escaped:\n%s", s)
	}
}

func TestSpawnModel(t *testing.T) {
	prof := &config.Profile{World: config.World{Name: "field"}}
	ctx := context.Background()

	// the stub echoes its arguments so the request can be checked
	env := &Env{Profile: prof, Gz: gzBin(t, `echo "$@"; echo "data: true"`, "")}
	if err := spawnModel(ctx, env, "/run/d/model.sdf", "d", 1, 2); err != nil {
		t.Fatal(err)
	}

	env.Gz = gzBin(t, `echo "data: false"`, "")
	if err := spawnModel(ctx, env, "/run/d/model.sdf", "d", 1, 2); err == nil || !strings.Contains(err.Error(), "world refused it") {
		t.Fatalf("want refused, got %v", err)
	}

	env.Gz = gzBin(t, `echo "no service" >&2; exit 1`, "")
	if err := spawnModel(ctx, env, "/run/d/model.sdf", "d", 1, 2); err == nil || !strings.Contains(err.Error(), "no service") {
		t.Fatalf("want the gz output in the error, got %v", err)
	}

	env.Gz = envcap.With(nil, "PATH=/nonexistent")
	if err := spawnModel(ctx, env, "/run/d/model.sdf", "d", 1, 2); err == nil {
		t.Fatal("want an error without gz")
	}
}
