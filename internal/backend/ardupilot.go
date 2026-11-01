package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/supervisor"
)

// ArduPilot SITL, started directly (no sim_vehicle.py, no MAVProxy), with its
// builtin physics or in the shared gz world. Ports follow --instance: SERIAL1
// is TCP 5762+10*instance. SERIAL0 is sent to the GCS port, the way PX4 talks
// to QGC.
//
// In gz, ArduPilot does not spawn its model as PX4 does: ardupilot_gazebo's
// plugin inside the model exchanges JSON with SITL over UDP, on port
// 9002+10*instance. swarmsim writes each drone a copy of the model with its
// port and spawns it before SITL starts.
type ardupilot struct{}

const (
	ardupilotGCS = "udpclient:127.0.0.1:14550"
	// ardupilotGzStep is the physics step ArduPilot needs: one loop per step,
	// and copter refuses to arm below 400 Hz.
	ardupilotGzStep = 0.001
	// ardupilotGzModel is ardupilot_gazebo's quad with the plugin.
	ardupilotGzModel = "iris_with_ardupilot"
	// ardupilotGzLine is logged once gz answers SITL.
	ardupilotGzLine    = "JSON received:"
	ardupilotGzTimeout = 60 * time.Second
)

var ardupilotVehicles = map[string]struct{ bin, model, defaults string }{
	"copter": {"arducopter", "quad", "copter.parm"},
	"plane":  {"arduplane", "plane", "plane.parm"},
	"rover":  {"ardurover", "rover", "rover.parm"},
}

// ardupilotGzFrames are the gz models with the plugin that swarmsim knows, and
// the frame parameters each needs: SITL takes its frame from --model, which is
// JSON in gz.
var ardupilotGzFrames = map[string]struct{ vehicle, defaults string }{
	"iris_with_ardupilot":   {"copter", "gazebo-iris.parm"},
	"iris_with_gimbal":      {"copter", "gazebo-iris.parm"},
	"zephyr_with_ardupilot": {"plane", "gazebo-zephyr.parm"},
}

var fdmPort = regexp.MustCompile(`<fdm_port_in>\s*\d+\s*</fdm_port_in>`)

func (ardupilot) Check(env *Env, d config.Drone) error {
	lab := env.Lab.ArduPilot
	if lab == nil {
		return fmt.Errorf("ardupilot is not set up in %s (ardupilot.root, ardupilot.bin)", env.Lab.Path())
	}
	g := d.Group
	v, ok := ardupilotVehicles[ardupilotVehicle(g)]
	if !ok {
		return fmt.Errorf("vehicle %q: use copter, plane or rover", g.Vehicle)
	}
	if err := exists(filepath.Join(lab.Bin, v.bin), "ardupilot binary"); err != nil {
		return err
	}
	if err := exists(ardupilotDefaults(lab.Root, v.defaults), "ardupilot defaults"); err != nil {
		return err
	}
	switch g.Sim {
	case "builtin":
		return nil
	case "gz":
		return checkArdupilotGz(env, g)
	}
	return fmt.Errorf("sim %q: ardupilot supports builtin and gz", g.Sim)
}

func checkArdupilotGz(env *Env, g *config.Group) error {
	if len(g.Payloads) > 0 || g.Base != "" {
		return fmt.Errorf("payloads and base: not supported for ardupilot yet")
	}
	frame, ok := ardupilotGzFrames[ardupilotGzBase(g)]
	if !ok {
		return fmt.Errorf("model %q: swarmsim knows the frame parameters of %s", g.Model, strings.Join(sortedKeys(ardupilotGzFrames), ", "))
	}
	if frame.vehicle != ardupilotVehicle(g) {
		return fmt.Errorf("model %q is a %s, set vehicle: %s", ardupilotGzBase(g), frame.vehicle, frame.vehicle)
	}
	if err := exists(ardupilotDefaults(env.Lab.ArduPilot.Root, frame.defaults), "ardupilot frame defaults"); err != nil {
		return err
	}
	if _, err := ardupilotGzSDF(env, g); err != nil {
		return err
	}
	if env.Profile.World.Home == nil {
		sdf, err := worldSDF(env)
		if err != nil {
			return err
		}
		if _, err := worldOrigin(sdf); err != nil {
			return err
		}
	}
	return nil
}

func (ardupilot) Specs(env *Env, d config.Drone) ([]supervisor.Spec, error) {
	lab := env.Lab.ArduPilot
	g := d.Group
	v := ardupilotVehicles[ardupilotVehicle(g)]
	dir := env.droneDir(d)
	log := env.logPath(d.Name)

	model := g.Model
	if model == "" {
		model = v.model
	}
	defaults := []string{ardupilotDefaults(lab.Root, v.defaults)}
	home := env.Home.Offset(d.X, d.Y)
	var spawn, ready func(context.Context) error
	if g.Sim == "gz" {
		var err error
		model = "JSON"
		defaults = append(defaults, ardupilotDefaults(lab.Root, ardupilotGzFrames[ardupilotGzBase(g)].defaults))
		// SITL puts gz's x=0, y=0 at its home: the drone's place comes from gz.
		if home, err = ardupilotGzHome(env); err != nil {
			return nil, err
		}
		if spawn, err = ardupilotGzSpawn(env, d); err != nil {
			return nil, err
		}
		offset := fileSize(log) // the log is appended to; only this run counts
		// Start the next drone once gz answers this one.
		ready = func(ctx context.Context) error {
			return waitLogLine(ctx, log, offset, ardupilotGzLine, "", ardupilotGzTimeout)
		}
	}
	if len(g.Params) > 0 {
		p, err := writeParmFile(dir, g.Params)
		if err != nil {
			return nil, err
		}
		defaults = append(defaults, p)
	}
	args := []string{
		"--model", model,
		"--speedup", "1",
		"--instance", strconv.Itoa(d.Index),
		"--sysid", strconv.Itoa(d.SysID),
		"--home", fmt.Sprintf("%.7f,%.7f,%g,0", home.Lat, home.Lon, home.Alt),
		"--defaults", strings.Join(defaults, ","),
		"--serial0", ardupilotGCS,
	}
	if g.Sim == "gz" {
		args = append(args, "--sim-address", "127.0.0.1")
	}
	return []supervisor.Spec{{
		Name:   d.Name,
		Path:   filepath.Join(lab.Bin, v.bin),
		Args:   args,
		Env:    os.Environ(),
		Dir:    dir,
		Log:    log,
		Before: spawn,
		Ready:  ready,
	}}, nil
}

func ardupilotGzBase(g *config.Group) string {
	if g.Model == "" {
		return ardupilotGzModel
	}
	return g.Model
}

// ardupilotGzSDF finds the model's SDF file on the gz search path.
func ardupilotGzSDF(env *Env, g *config.Group) (string, error) {
	dir, err := findModel(env, ardupilotGzBase(g))
	if err != nil {
		return "", err
	}
	sdf := filepath.Join(dir, modelFile(dir))
	data, err := os.ReadFile(sdf)
	if err != nil {
		return "", err
	}
	if n := len(fdmPort.FindAll(data, -1)); n != 1 {
		return "", fmt.Errorf("%s: want one ArduPilotPlugin with fdm_port_in, found %d", sdf, n)
	}
	return sdf, nil
}

// ardupilotGzHome is where SITL puts the gz world's origin: world.home when
// the profile sets it (PX4 moves the world there too), else the world's own.
func ardupilotGzHome(env *Env) (config.Home, error) {
	if env.Profile.World.Home != nil {
		return *env.Profile.World.Home, nil
	}
	sdf, err := worldSDF(env)
	if err != nil {
		return config.Home{}, err
	}
	return worldOrigin(sdf)
}

// ardupilotGzSpawn returns the hook that writes the drone's copy of the model,
// listening on its instance's port, and spawns it at its place.
func ardupilotGzSpawn(env *Env, d config.Drone) (func(context.Context) error, error) {
	src, err := ardupilotGzSDF(env, d.Group)
	if err != nil {
		return nil, err
	}
	dir := env.droneDir(d)
	sdf := filepath.Join(dir, "model.sdf")
	return func(ctx context.Context) error {
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		data = fdmPort.ReplaceAll(data, []byte(fmt.Sprintf("<fdm_port_in>%d</fdm_port_in>", 9002+10*d.Index)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(sdf, data, 0o644); err != nil {
			return err
		}
		return spawnModel(ctx, env, sdf, d.Name, d.X, d.Y)
	}, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
