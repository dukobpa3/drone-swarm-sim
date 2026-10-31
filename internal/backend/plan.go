// Package backend turns a profile into the processes to run: the shared
// simulator first, then one or more processes per drone.
package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/envcap"
	"github.com/dukobpa3/drone-swarm-sim/internal/supervisor"
)

// DefaultHome is PX4's SITL default home, used by drones with their own
// physics (PX4 SIH, ArduPilot builtin, exec) when the profile sets no
// world.home. gz drones without world.home keep the gz world's origin.
var DefaultHome = config.Home{Lat: 47.397742, Lon: 8.545594, Alt: 488}

// Env is what backends build their processes from.
type Env struct {
	Lab     *config.Lab
	Profile *config.Profile
	Drones  []config.Drone
	// Dir is run_dir/<profile name>: drone working directories live here
	// and survive between runs.
	Dir  string
	Home config.Home
	// Gz is the environment for gz and for anything that talks to it.
	// Nil when no drone uses gz.
	Gz []string
}

// Autopilot builds the processes for one drone.
type Autopilot interface {
	// Check reports what is missing on this machine for the drone.
	Check(env *Env, d config.Drone) error
	Specs(env *Env, d config.Drone) ([]supervisor.Spec, error)
}

var autopilots = map[string]Autopilot{
	"px4":       px4{},
	"ardupilot": ardupilot{},
	"exec":      execBackend{},
}

// Plan validates the profile against the lab and returns the processes in
// start order. All problems are collected, so one run shows all of them.
func Plan(lab *config.Lab, prof *config.Profile) ([]supervisor.Spec, error) {
	drones, err := prof.Drones()
	if err != nil {
		return nil, err
	}
	env := &Env{
		Lab:     lab,
		Profile: prof,
		Drones:  drones,
		Dir:     filepath.Join(lab.RunDir, prof.Name),
		Home:    DefaultHome,
	}
	if prof.World.Home != nil {
		env.Home = *prof.World.Home
	}

	problems := append(env.checkWorld(), env.checkGroups()...)
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s:\n  - %s", prof.Path(), strings.Join(problems, "\n  - "))
	}
	return env.specs()
}

// usesGz says whether any drone runs in the shared gz world.
func (e *Env) usesGz() bool {
	for _, d := range e.Drones {
		if d.Group.Sim == "gz" {
			return true
		}
	}
	return false
}

// checkWorld validates world.sim and, when gz is used, captures its
// environment into e.Gz.
func (e *Env) checkWorld() []string {
	var problems []string
	switch e.Profile.World.Sim {
	case "gz", "none":
	default:
		problems = append(problems, fmt.Sprintf("world.sim %q: use gz or none", e.Profile.World.Sim))
	}
	if !e.usesGz() {
		return problems
	}
	if e.Profile.World.Sim != "gz" {
		problems = append(problems, "a group uses sim gz, but world.sim is not gz")
	}
	gzEnv, err := captureGz(e.Lab, e.Drones)
	if err != nil {
		return append(problems, err.Error())
	}
	e.Gz = gzEnv
	return problems
}

// checkGroups asks each group's backend what is missing, once per group.
func (e *Env) checkGroups() []string {
	var problems []string
	checked := map[*config.Group]bool{}
	for _, d := range e.Drones {
		if checked[d.Group] {
			continue
		}
		checked[d.Group] = true
		ap, ok := autopilots[d.Group.Autopilot]
		if !ok {
			problems = append(problems, fmt.Sprintf("group %q: autopilot %q: use px4, ardupilot or exec", d.Group.Name, d.Group.Autopilot))
			continue
		}
		if err := ap.Check(e, d); err != nil {
			problems = append(problems, fmt.Sprintf("group %q: %v", d.Group.Name, err))
		}
	}
	return problems
}

// specs returns the world's processes, then each drone's, in start order.
func (e *Env) specs() ([]supervisor.Spec, error) {
	var specs []supervisor.Spec
	if e.usesGz() {
		world, err := gzWorld(e)
		if err != nil {
			return nil, err
		}
		specs = append(specs, world...)
	}
	for _, d := range e.Drones {
		s, err := autopilots[d.Group.Autopilot].Specs(e, d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.Name, err)
		}
		specs = append(specs, s...)
	}
	return specs, nil
}

// captureGz builds the gz environment: the lab's activate script, plus PX4's
// gz_env.sh when PX4 drones run in gz (it adds PX4's models, worlds and
// plugins to the gz search paths).
func captureGz(lab *config.Lab, drones []config.Drone) ([]string, error) {
	var scripts []string
	if lab.Gz != nil && lab.Gz.Activate != "" {
		scripts = append(scripts, lab.Gz.Activate)
	}
	for _, d := range drones {
		if d.Group.Autopilot == "px4" && d.Group.Sim == "gz" {
			if lab.PX4 == nil {
				return nil, fmt.Errorf("px4 drones need a px4 section in %s", lab.Path())
			}
			scripts = append(scripts, filepath.Join(lab.PX4.Build, "gz_env.sh"))
			break
		}
	}
	env, err := envcap.Capture(scripts...)
	if err != nil {
		return nil, err
	}
	if _, err := envcap.LookPath(env, "gz"); err != nil {
		return nil, fmt.Errorf("gz: %w (set gz.activate in %s)", err, lab.Path())
	}
	return env, nil
}

func (e *Env) droneDir(d config.Drone) string { return filepath.Join(e.Dir, d.Name) }

func (e *Env) logPath(name string) string { return filepath.Join(e.Dir, "logs", name+".log") }

func exists(path, what string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%s not found: %s", what, path)
	}
	return nil
}
