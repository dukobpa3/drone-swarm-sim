package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/supervisor"
)

// ArduPilot SITL with its builtin physics, started directly (no sim_vehicle.py,
// no MAVProxy). Ports follow --instance: SERIAL1 is TCP 5762+10*instance.
// SERIAL0 is sent to the GCS port, the way PX4 talks to QGC.
//
// Not run end to end yet: written against libraries/AP_HAL_SITL/SITL_cmdline.cpp.
type ardupilot struct{}

const ardupilotGCS = "udpclient:127.0.0.1:14550"

var ardupilotVehicles = map[string]struct{ bin, model, defaults string }{
	"copter": {"arducopter", "quad", "copter.parm"},
	"plane":  {"arduplane", "plane", "plane.parm"},
	"rover":  {"ardurover", "rover", "rover.parm"},
}

func (ardupilot) Check(env *Env, d config.Drone) error {
	lab := env.Lab.ArduPilot
	if lab == nil {
		return fmt.Errorf("ardupilot is not set up in %s (ardupilot.root, ardupilot.bin)", env.Lab.Path())
	}
	g := d.Group
	if g.Sim == "gz" {
		return fmt.Errorf("sim gz: ardupilot in gz needs ardupilot_gazebo, not supported yet; use sim: builtin")
	}
	if g.Sim != "builtin" {
		return fmt.Errorf("sim %q: ardupilot supports builtin", g.Sim)
	}
	v, ok := ardupilotVehicles[ardupilotVehicle(g)]
	if !ok {
		return fmt.Errorf("vehicle %q: use copter, plane or rover", g.Vehicle)
	}
	if err := exists(filepath.Join(lab.Bin, v.bin), "ardupilot binary"); err != nil {
		return err
	}
	return exists(ardupilotDefaults(lab.Root, v.defaults), "ardupilot defaults")
}

func (ardupilot) Specs(env *Env, d config.Drone) ([]supervisor.Spec, error) {
	lab := env.Lab.ArduPilot
	g := d.Group
	v := ardupilotVehicles[ardupilotVehicle(g)]
	dir := env.droneDir(d)
	model := g.Model
	if model == "" {
		model = v.model
	}

	defaults := []string{ardupilotDefaults(lab.Root, v.defaults)}
	if len(g.Params) > 0 {
		p, err := writeParmFile(dir, g.Params)
		if err != nil {
			return nil, err
		}
		defaults = append(defaults, p)
	}
	h := env.Home.Offset(d.X, d.Y)
	return []supervisor.Spec{{
		Name: d.Name,
		Path: filepath.Join(lab.Bin, v.bin),
		Args: []string{
			"--model", model,
			"--speedup", "1",
			"--instance", strconv.Itoa(d.Index),
			"--sysid", strconv.Itoa(d.SysID),
			"--home", fmt.Sprintf("%.7f,%.7f,%g,0", h.Lat, h.Lon, h.Alt),
			"--defaults", strings.Join(defaults, ","),
			"--serial0", ardupilotGCS,
		},
		Env: os.Environ(),
		Dir: dir,
		Log: env.logPath(d.Name),
	}}, nil
}

func ardupilotVehicle(g *config.Group) string {
	if g.Vehicle == "" {
		return "copter"
	}
	return g.Vehicle
}

func ardupilotDefaults(root, file string) string {
	return filepath.Join(root, "Tools", "autotest", "default_params", file)
}

// writeParmFile writes the group params as an extra defaults file. ArduPilot
// applies defaults files in order, so these win over the vehicle defaults.
func writeParmFile(dir string, params map[string]float64) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("# written by swarmsim from the profile, overwritten on every run\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s %s\n", k, strconv.FormatFloat(params[k], 'g', -1, 64))
	}
	p := filepath.Join(dir, "swarmsim.parm")
	return p, os.WriteFile(p, []byte(sb.String()), 0o644)
}
