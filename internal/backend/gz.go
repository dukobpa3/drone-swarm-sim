package backend

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/envcap"
	"github.com/dukobpa3/drone-swarm-sim/internal/supervisor"
)

const gzReadyTimeout = 90 * time.Second

// gzWorld runs the gz server (and GUI) that all gz drones join. The launcher
// owns it, so no drone is special and any of them can be restarted.
func gzWorld(env *Env) ([]supervisor.Spec, error) {
	gz, err := envcap.LookPath(env.Gz, "gz")
	if err != nil {
		return nil, err
	}
	sdf, err := worldSDF(env)
	if err != nil {
		return nil, err
	}
	name := env.Profile.WorldName()
	ready := func(ctx context.Context) error { return waitWorld(ctx, gz, env.Gz, name) }
	if inGz(env.Drones, "ardupilot") {
		// ArduPilot runs one loop per physics step and refuses to arm below
		// 400 Hz; PX4's worlds step 4 ms. PX4 follows gz time at any step.
		ready = func(ctx context.Context) error {
			if err := waitWorld(ctx, gz, env.Gz, name); err != nil {
				return err
			}
			return setStep(ctx, gz, env.Gz, name, ardupilotGzStep)
		}
	}

	specs := []supervisor.Spec{{
		Name:     "gz",
		Path:     gz,
		Args:     []string{"sim", "--verbose=1", "-r", "-s", sdf},
		Env:      env.Gz,
		Dir:      env.Dir,
		Log:      env.logPath("gz"),
		Critical: true,
		Ready:    ready,
	}}
	if env.Profile.World.GUI {
		specs = append(specs, supervisor.Spec{
			Name: "gz-gui",
			Path: gz,
			Args: []string{"sim", "-g"},
			Env:  env.Gz,
			Dir:  env.Dir,
			Log:  env.logPath("gz-gui"),
		})
	}
	return specs, nil
}

// worldSDF resolves the profile's world against PX4's worlds directory.
func worldSDF(env *Env) (string, error) {
	worldsDir := envcap.Lookup(env.Gz, "PX4_GZ_WORLDS")
	if worldsDir == "" && !strings.HasSuffix(env.Profile.World.Name, ".sdf") {
		return "", fmt.Errorf("world %q: no worlds directory (needs px4 in the lab), give world.name as a path to an .sdf file", env.Profile.World.Name)
	}
	sdf := env.Profile.WorldPath(worldsDir)
	return sdf, exists(sdf, "world")
}

// worldOrigin reads the world's spherical coordinates: where gz puts x=0,
// y=0, z=0 on Earth.
func worldOrigin(sdf string) (config.Home, error) {
	data, err := os.ReadFile(sdf)
	if err != nil {
		return config.Home{}, err
	}
	var w struct {
		World struct {
			Coords *struct {
				Lat float64 `xml:"latitude_deg"`
				Lon float64 `xml:"longitude_deg"`
				Alt float64 `xml:"elevation"`
			} `xml:"spherical_coordinates"`
		} `xml:"world"`
	}
	if err := xml.Unmarshal(data, &w); err != nil {
		return config.Home{}, fmt.Errorf("%s: %w", sdf, err)
	}
	if w.World.Coords == nil {
		return config.Home{}, fmt.Errorf("%s has no spherical_coordinates, set world.home", sdf)
	}
	return config.Home{Lat: w.World.Coords.Lat, Lon: w.World.Coords.Lon, Alt: w.World.Coords.Alt}, nil
}

// setStep changes the running world's physics step.
func setStep(ctx context.Context, gz string, env []string, world string, step float64) error {
	cmd := exec.CommandContext(ctx, gz, "service", "-s", "/world/"+world+"/set_physics",
		"--reqtype", "gz.msgs.Physics", "--reptype", "gz.msgs.Boolean", "--timeout", "5000",
		"--req", fmt.Sprintf("max_step_size: %g, real_time_factor: 1.0", step))
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set the physics step of %q to %gs: %w: %s", world, step, err, bytes.TrimSpace(out))
	}
	if !bytes.Contains(out, []byte("data: true")) {
		return fmt.Errorf("set the physics step of %q to %gs: world refused it: %s", world, step, bytes.TrimSpace(out))
	}
	return nil
}

// waitWorld polls gz topics until the world publishes its clock.
func waitWorld(ctx context.Context, gz string, env []string, world string) error {
	want := "/world/" + world + "/clock"
	deadline := time.Now().Add(gzReadyTimeout)
	for time.Now().Before(deadline) {
		cmd := exec.CommandContext(ctx, gz, "topic", "-l")
		cmd.Env = env
		out, _ := cmd.Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) == want {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("world %q not up after %v (no %s topic)", world, gzReadyTimeout, want)
}
