package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Profile is one swarm setup: a world and groups of drones placed on a layout.
type Profile struct {
	Name   string  `yaml:"name"`
	World  World   `yaml:"world"`
	Layout Layout  `yaml:"layout"`
	Groups []Group `yaml:"groups"`
	// ModelPaths are directories with gz models (payloads, bases), relative
	// to the profile. Default: ../models next to the profiles directory.
	ModelPaths []string `yaml:"model_paths"`

	path string
}

type World struct {
	// Sim is the shared simulator: "gz" or "none" (every drone brings its own
	// physics, e.g. PX4 SIH or ArduPilot builtin).
	Sim string `yaml:"sim"`
	// Name is a world from PX4's gz worlds directory, or a path to an .sdf file.
	Name string `yaml:"name"`
	GUI  bool   `yaml:"gui"`
	Home *Home  `yaml:"home"`
}

type Home struct {
	Lat float64 `yaml:"lat"`
	Lon float64 `yaml:"lon"`
	Alt float64 `yaml:"alt"`
}

type Layout struct {
	// Kind is "grid" (default) or "line".
	Kind    string  `yaml:"kind"`
	Spacing float64 `yaml:"spacing"`
	// Columns of the grid; 0 means a square-ish grid.
	Columns int `yaml:"columns"`
}

type Group struct {
	// Name prefixes drone names: <name>-0, <name>-1... Defaults to the autopilot.
	Name      string `yaml:"name"`
	Autopilot string `yaml:"autopilot"` // px4 | ardupilot | exec
	Count     int    `yaml:"count"`
	// Sim overrides world.sim for this group: px4 can use "sih",
	// ardupilot "builtin".
	Sim string `yaml:"sim"`

	// px4: airframe model as in PX4's airframes, e.g. gz_x500, sihsim_quadx.
	// ardupilot: --model, e.g. quad, plane, rover.
	Model string `yaml:"model"`
	// ardupilot: copter | plane | rover (selects the binary).
	Vehicle string `yaml:"vehicle"`
	// Parameters applied on top of the autopilot defaults.
	Params map[string]float64 `yaml:"params"`

	// Base is the gz model the vehicle is built from when it has payloads.
	// px4 defaults to the model without its gz_ prefix (gz_x500 -> x500).
	Base string `yaml:"base"`
	// Payloads are gz models mounted on the base with a fixed joint.
	// With payloads, swarmsim spawns the model itself and the autopilot
	// attaches to it.
	Payloads []Payload `yaml:"payloads"`

	// exec: command and environment, expanded with Go templates over Drone,
	// e.g. "{{.Index}}", "{{.SysID}}", "{{.Name}}", "{{.X}}".
	Command []string          `yaml:"command"`
	Env     map[string]string `yaml:"env"`
}

type Payload struct {
	// Name is unique on the vehicle and scopes the payload's topics.
	// Defaults to the model name.
	Name  string `yaml:"name"`
	Model string `yaml:"model"`
	// Pose on the base: x y z [roll pitch yaw], metres and radians.
	Pose []float64 `yaml:"pose"`
}

// Drone is one vehicle after groups are expanded.
type Drone struct {
	Group *Group
	Name  string
	// Index is unique across the whole swarm. PX4 and ArduPilot derive their
	// instance ports from it, so different autopilots never share ports.
	Index int
	SysID int
	// Position on the layout in metres, x east, y north.
	X, Y float64
}

func LoadProfile(path string) (*Profile, error) {
	var p Profile
	if err := loadYAML(path, &p); err != nil {
		return nil, err
	}
	p.path = path
	if p.Name == "" {
		p.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	p.World.setDefaults()
	p.Layout.setDefaults()
	for i := range p.Groups {
		p.Groups[i].setDefaults(p.World.Sim)
	}
	p.resolveModelPaths()
	return &p, nil
}

func (w *World) setDefaults() {
	if w.Sim == "" {
		w.Sim = "gz"
	}
	if w.Name == "" {
		w.Name = "default"
	}
}

func (l *Layout) setDefaults() {
	if l.Kind == "" {
		l.Kind = "grid"
	}
	if l.Spacing == 0 {
		l.Spacing = 3
	}
}

func (g *Group) setDefaults(worldSim string) {
	if g.Name == "" {
		g.Name = g.Autopilot
	}
	if g.Sim == "" {
		g.Sim = worldSim
	}
	for j := range g.Payloads {
		if g.Payloads[j].Name == "" {
			g.Payloads[j].Name = g.Payloads[j].Model
		}
	}
}

// resolveModelPaths makes model paths absolute, relative to the profile, or
// defaults them to ../models when that exists.
func (p *Profile) resolveModelPaths() {
	base := filepath.Dir(p.path)
	if len(p.ModelPaths) == 0 {
		if def := filepath.Join(base, "..", "models"); dirExists(def) {
			p.ModelPaths = []string{filepath.Clean(def)}
		}
		return
	}
	for i, mp := range p.ModelPaths {
		p.ModelPaths[i] = expand(base, mp)
	}
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func (p *Profile) Path() string { return p.path }

// WorldPath resolves the world name against a worlds directory unless it is
// already a path to an .sdf file.
func (p *Profile) WorldPath(worldsDir string) string {
	if strings.HasSuffix(p.World.Name, ".sdf") {
		return expand(filepath.Dir(p.path), p.World.Name)
	}
	return filepath.Join(worldsDir, p.World.Name+".sdf")
}

// WorldName is the <world name> gz uses in topics. PX4's worlds name the world
// after the file, which is what this assumes.
func (p *Profile) WorldName() string {
	return strings.TrimSuffix(filepath.Base(p.World.Name), ".sdf")
}

// Drones expands groups in order and places them on the layout.
func (p *Profile) Drones() ([]Drone, error) {
	var drones []Drone
	names := map[string]bool{}
	for gi := range p.Groups {
		g := &p.Groups[gi]
		if g.Count < 1 {
			return nil, fmt.Errorf("group %q: count must be at least 1", g.Name)
		}
		for i := 0; i < g.Count; i++ {
			name := fmt.Sprintf("%s-%d", g.Name, i)
			if names[name] {
				return nil, fmt.Errorf("duplicate drone name %q: give groups distinct names", name)
			}
			names[name] = true
			drones = append(drones, Drone{Group: g, Name: name, Index: len(drones), SysID: len(drones) + 1})
		}
	}
	if len(drones) == 0 {
		return nil, fmt.Errorf("%s: no drones, add at least one group", p.path)
	}
	cols := p.Layout.Columns
	switch p.Layout.Kind {
	case "line":
		cols = len(drones)
	case "grid":
		if cols <= 0 {
			cols = int(math.Ceil(math.Sqrt(float64(len(drones)))))
		}
	default:
		return nil, fmt.Errorf("layout.kind %q: use grid or line", p.Layout.Kind)
	}
	for i := range drones {
		drones[i].X = float64(i%cols) * p.Layout.Spacing
		drones[i].Y = float64(i/cols) * p.Layout.Spacing
	}
	return drones, nil
}

// Offset returns the home moved by x east and y north metres.
func (h Home) Offset(x, y float64) Home {
	const r = 6378137.0
	return Home{
		Lat: h.Lat + (y/r)*180/math.Pi,
		Lon: h.Lon + (x/(r*math.Cos(h.Lat*math.Pi/180)))*180/math.Pi,
		Alt: h.Alt,
	}
}
