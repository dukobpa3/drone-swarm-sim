package backend

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

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
	worldsDir := envcap.Lookup(env.Gz, "PX4_GZ_WORLDS")
	if worldsDir == "" && !strings.HasSuffix(env.Profile.World.Name, ".sdf") {
		return nil, fmt.Errorf("world %q: no worlds directory (needs px4 drones in gz), give world.name as a path to an .sdf file", env.Profile.World.Name)
	}
	sdf := env.Profile.WorldPath(worldsDir)
	if err := exists(sdf, "world"); err != nil {
		return nil, err
	}
	name := env.Profile.WorldName()

	specs := []supervisor.Spec{{
		Name:     "gz",
		Path:     gz,
		Args:     []string{"sim", "--verbose=1", "-r", "-s", sdf},
		Env:      env.Gz,
		Dir:      env.Dir,
		Log:      env.logPath("gz"),
		Critical: true,
		Ready:    func(ctx context.Context) error { return waitWorld(ctx, gz, env.Gz, name) },
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
