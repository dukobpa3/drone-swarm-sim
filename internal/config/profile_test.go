package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProfile(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "profiles", "swarm.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadProfileDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProfile(writeProfile(t, dir, `
groups:
  - autopilot: px4
    count: 1
    payloads: [{ model: thermal_cam }]
  - { name: ap, autopilot: ardupilot, sim: builtin, count: 1 }
`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "swarm" || p.World.Sim != "gz" || p.World.Name != "default" {
		t.Errorf("profile defaults: name %q sim %q world %q", p.Name, p.World.Sim, p.World.Name)
	}
	if p.Layout.Kind != "grid" || p.Layout.Spacing != 3 {
		t.Errorf("layout defaults: %+v", p.Layout)
	}
	g := p.Groups[0]
	if g.Name != "px4" || g.Sim != "gz" || g.Payloads[0].Name != "thermal_cam" {
		t.Errorf("group defaults: name %q sim %q payload %q", g.Name, g.Sim, g.Payloads[0].Name)
	}
	if p.Groups[1].Sim != "builtin" {
		t.Errorf("a group's own sim must win, got %q", p.Groups[1].Sim)
	}
	if want := filepath.Join(dir, "models"); len(p.ModelPaths) != 1 || p.ModelPaths[0] != want {
		t.Errorf("model paths %v, want [%s]", p.ModelPaths, want)
	}
}

func TestLoadProfileModelPaths(t *testing.T) {
	dir := t.TempDir()
	p, err := LoadProfile(writeProfile(t, dir, "model_paths: [mine, /abs/models]\ngroups: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "profiles", "mine"), "/abs/models"}
	if strings.Join(p.ModelPaths, ",") != strings.Join(want, ",") {
		t.Fatalf("model paths %v, want %v", p.ModelPaths, want)
	}

	p, err = LoadProfile(writeProfile(t, t.TempDir(), "groups: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ModelPaths) != 0 {
		t.Fatalf("no ../models, want no model paths, got %v", p.ModelPaths)
	}
}

func TestDronesLayout(t *testing.T) {
	p := &Profile{
		Layout: Layout{Kind: "grid", Spacing: 2},
		Groups: []Group{{Name: "a", Count: 3}, {Name: "b", Count: 2}},
	}
	drones, err := p.Drones()
	if err != nil {
		t.Fatal(err)
	}
	if len(drones) != 5 || drones[3].Name != "b-0" || drones[3].Index != 3 || drones[3].SysID != 4 {
		t.Fatalf("drones: %+v", drones)
	}
	// 5 drones make a 3-column grid
	if d := drones[4]; d.X != 2 || d.Y != 2 {
		t.Errorf("b-1 at %g,%g, want 2,2", d.X, d.Y)
	}

	p.Layout.Kind = "line"
	drones, err = p.Drones()
	if err != nil {
		t.Fatal(err)
	}
	if d := drones[4]; d.X != 8 || d.Y != 0 {
		t.Errorf("line: b-1 at %g,%g, want 8,0", d.X, d.Y)
	}
}

func TestDronesErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Profile
		want string
	}{
		{"count", Profile{Layout: Layout{Kind: "grid"}, Groups: []Group{{Name: "a"}}}, "count must be at least 1"},
		{"duplicate", Profile{Layout: Layout{Kind: "grid"}, Groups: []Group{{Name: "a", Count: 1}, {Name: "a", Count: 1}}}, "duplicate drone name"},
		{"empty", Profile{Layout: Layout{Kind: "grid"}}, "no drones"},
		{"layout", Profile{Layout: Layout{Kind: "ring"}, Groups: []Group{{Name: "a", Count: 1}}}, `layout.kind "ring"`},
	} {
		if _, err := tc.p.Drones(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want %q, got %v", tc.name, tc.want, err)
		}
	}
}

func TestWorldPathAndName(t *testing.T) {
	p := &Profile{World: World{Name: "baylands"}, path: "/lab/profiles/p.yaml"}
	if got := p.WorldPath("/px4/worlds"); got != "/px4/worlds/baylands.sdf" {
		t.Errorf("world path %q", got)
	}
	p.World.Name = "worlds/field.sdf"
	if got := p.WorldPath("/px4/worlds"); got != "/lab/profiles/worlds/field.sdf" {
		t.Errorf("sdf world path %q", got)
	}
	if got := p.WorldName(); got != "field" {
		t.Errorf("world name %q", got)
	}
}

func TestHomeOffset(t *testing.T) {
	h := Home{Lat: 47.397742, Lon: 8.545594, Alt: 488}
	o := h.Offset(100, 100)
	// 100 m north is about 0.0009 degrees of latitude anywhere
	if d := (o.Lat - h.Lat) * 111320; math.Abs(d-100) > 1 {
		t.Errorf("north offset %g m", d)
	}
	if d := (o.Lon - h.Lon) * 111320 * math.Cos(h.Lat*math.Pi/180); math.Abs(d-100) > 1 {
		t.Errorf("east offset %g m", d)
	}
	if o.Alt != h.Alt {
		t.Errorf("altitude changed: %g", o.Alt)
	}
}
