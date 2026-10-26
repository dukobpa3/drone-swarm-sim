package backend

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/template"

	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/envcap"
	"github.com/dukobpa3/drone-swarm-sim/internal/supervisor"
)

// execBackend runs any command per drone, for vehicles swarmsim has no
// backend for. Command and env values are Go templates over execData.
type execBackend struct{}

type execData struct {
	Name          string
	Index, SysID  int
	X, Y          float64
	Lat, Lon, Alt float64
	// Dir is the drone's working directory, World the gz world name.
	Dir, World string
}

func (execBackend) Check(env *Env, d config.Drone) error {
	g := d.Group
	if len(g.Command) == 0 {
		return fmt.Errorf("exec needs a command")
	}
	args, _, err := execExpand(env, d)
	if err != nil {
		return err
	}
	_, err = envcap.LookPath(execBase(env, g), args[0])
	return err
}

func (execBackend) Specs(env *Env, d config.Drone) ([]supervisor.Spec, error) {
	args, vars, err := execExpand(env, d)
	if err != nil {
		return nil, err
	}
	base := execBase(env, d.Group)
	path, err := envcap.LookPath(base, args[0])
	if err != nil {
		return nil, err
	}
	return []supervisor.Spec{{
		Name: d.Name,
		Path: path,
		Args: args[1:],
		Env:  envcap.With(base, vars...),
		Dir:  env.droneDir(d),
		Log:  env.logPath(d.Name),
	}}, nil
}

// execBase gives gz drones the gz environment, everything else the launcher's.
func execBase(env *Env, g *config.Group) []string {
	if g.Sim == "gz" && env.Gz != nil {
		return env.Gz
	}
	return os.Environ()
}

func execExpand(env *Env, d config.Drone) (args, vars []string, err error) {
	h := env.Home.Offset(d.X, d.Y)
	data := execData{
		Name: d.Name, Index: d.Index, SysID: d.SysID, X: d.X, Y: d.Y,
		Lat: h.Lat, Lon: h.Lon, Alt: h.Alt,
		Dir: env.droneDir(d), World: env.Profile.WorldName(),
	}
	expand := func(s string) (string, error) {
		t, err := template.New("").Option("missingkey=error").Parse(s)
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		if err := t.Execute(&sb, data); err != nil {
			return "", err
		}
		return sb.String(), nil
	}
	for _, a := range d.Group.Command {
		s, err := expand(a)
		if err != nil {
			return nil, nil, fmt.Errorf("command %q: %w", a, err)
		}
		args = append(args, s)
	}
	keys := make([]string, 0, len(d.Group.Env))
	for k := range d.Group.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s, err := expand(d.Group.Env[k])
		if err != nil {
			return nil, nil, fmt.Errorf("env %s: %w", k, err)
		}
		vars = append(vars, k+"="+s)
	}
	return args, vars, nil
}
