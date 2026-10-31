package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/envcap"
	"github.com/dukobpa3/drone-swarm-sim/internal/supervisor"
)

// PX4 gives instances 10 and up the same offboard port (14549), see
// ROMFS/px4fmu_common/init.d-posix/px4-rc.mavlink.
const px4MaxIndex = 9

const (
	px4StartedLine = "Startup script returned successfully"
	// PX4 keeps running after a failed rcS, so the log is the only signal.
	px4FailedLine = "Startup script returned with return value"
	// px4BootTimeout bounds one instance's startup script; a healthy one
	// finishes in seconds.
	px4BootTimeout = 90 * time.Second
)

type px4 struct{}

func (px4) Check(env *Env, d config.Drone) error {
	lab := env.Lab.PX4
	if lab == nil {
		return fmt.Errorf("px4 is not set up in %s (px4.build)", env.Lab.Path())
	}
	g := d.Group
	switch g.Sim {
	case "gz":
		if !strings.HasPrefix(g.Model, "gz_") {
			return fmt.Errorf("model %q: sim gz needs a gz_ airframe, e.g. gz_x500", g.Model)
		}
	case "sih":
		if !strings.HasPrefix(g.Model, "sihsim_") {
			return fmt.Errorf("model %q: sim sih needs a sihsim_ airframe, e.g. sihsim_quadx", g.Model)
		}
	default:
		return fmt.Errorf("sim %q: px4 supports gz and sih", g.Sim)
	}
	if err := exists(filepath.Join(lab.Build, "bin", "px4"), "px4 binary"); err != nil {
		return err
	}
	if _, err := px4Airframe(lab.Build, g.Model); err != nil {
		return err
	}
	for _, other := range env.Drones {
		if other.Group == g && other.Index > px4MaxIndex {
			return fmt.Errorf("%s has index %d: PX4 instances above %d share offboard port 14549, put px4 groups first or use fewer drones", other.Name, other.Index, px4MaxIndex)
		}
	}
	return nil
}

func (px4) Specs(env *Env, d config.Drone) ([]supervisor.Spec, error) {
	build := env.Lab.PX4.Build
	g := d.Group
	dir := env.droneDir(d)
	log := env.logPath(d.Name)

	base := env.Gz
	if base == nil {
		base = os.Environ()
	}
	vars := []string{"PX4_SIM_MODEL=" + g.Model}
	switch g.Sim {
	case "gz":
		vars = append(vars,
			"PX4_GZ_STANDALONE=1",
			"PX4_GZ_WORLD="+env.Profile.WorldName(),
			fmt.Sprintf("PX4_GZ_MODEL_POSE=%g,%g", d.X, d.Y),
		)
		// PX4 moves the world origin to PX4_HOME_*, so only pass it when the
		// profile asks; otherwise the world keeps its own (baylands is in
		// California, and missions planned there must stay valid).
		if env.Profile.World.Home != nil {
			vars = append(vars, px4Home(env.Home)...)
		}
	case "sih":
		// No shared world: place drones by offsetting their home.
		vars = append(vars, px4Home(env.Home.Offset(d.X, d.Y))...)
	}

	bin := filepath.Join(build, "bin")
	offset := fileSize(log) // the log is appended to; only this run counts
	booted := func(ctx context.Context) error {
		return px4Hung(waitLogLine(ctx, log, offset, px4StartedLine, px4FailedLine, px4BootTimeout))
	}
	spec := supervisor.Spec{
		Name: d.Name,
		Path: filepath.Join(bin, "px4"),
		Args: []string{"-d", "-i", strconv.Itoa(d.Index), "-w", dir, filepath.Join(build, "etc")},
		Env:  envcap.With(base, vars...),
		Dir:  dir,
		Log:  log,
		// Starting the next PX4 before this one has spawned its model makes
		// the gz create calls race, so wait for its startup script.
		Ready: booted,
	}
	if len(g.Params) > 0 {
		spec.After = func(ctx context.Context) error {
			if err := booted(ctx); err != nil {
				return err
			}
			return px4SetParams(ctx, bin, d.Index, g.Params)
		}
	}
	return []supervisor.Spec{spec}, nil
}

func px4Home(h config.Home) []string {
	return []string{
		fmt.Sprintf("PX4_HOME_LAT=%.7f", h.Lat),
		fmt.Sprintf("PX4_HOME_LON=%.7f", h.Lon),
		fmt.Sprintf("PX4_HOME_ALT=%g", h.Alt),
	}
}

// px4Airframe finds etc/init.d-posix/airframes/<id>_<model>, the same lookup
// PX4's rcS does, so a typo fails here instead of in the PX4 console.
func px4Airframe(build, model string) (string, error) {
	dir := filepath.Join(build, "etc", "init.d-posix", "airframes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("px4 airframes: %w", err)
	}
	var near []string
	for _, e := range entries {
		id, name, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		if _, err := strconv.Atoi(id); err != nil {
			continue
		}
		if name == model {
			return id, nil
		}
		if strings.HasPrefix(name, strings.SplitN(model, "_", 2)[0]+"_") {
			near = append(near, name)
		}
	}
	sort.Strings(near)
	return "", fmt.Errorf("model %q: no PX4 airframe with that name in %s (similar: %s)", model, dir, strings.Join(near, ", "))
}

// px4SetParams waits for the instance to finish booting, then sets params
// through the PX4 daemon client.
func px4SetParams(ctx context.Context, bin string, index int, params map[string]float64) error {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cmd := exec.CommandContext(ctx, filepath.Join(bin, "px4-param"), "--instance", strconv.Itoa(index), "set", k, strconv.FormatFloat(params[k], 'g', -1, 64))
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("param set %s: %w: %s", k, err, bytes.TrimSpace(out))
		}
	}
	return nil
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

// errLogTimeout means the awaited line did not appear in time: the process
// is alive but stuck.
var errLogTimeout = errors.New("timed out")

// waitLogLine polls a log file until ok appears after offset, and fails as
// soon as a line containing fail appears. On timeout the error wraps
// errLogTimeout and carries the last lines of the log, so it shows where the
// process stopped.
func waitLogLine(ctx context.Context, path string, offset int64, ok, fail string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var seen []byte
	for time.Now().Before(deadline) {
		seen = readFrom(path, offset)
		if bytes.Contains(seen, []byte(ok)) {
			return nil
		}
		if line := lineWith(seen, fail); line != "" {
			return fmt.Errorf("%s (see %s)", line, path)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("%w: no %q in %s after %v; last lines:\n%s", errLogTimeout, ok, path, timeout, lastLines(seen, 6))
}

// px4Hung explains a startup that timed out: stopping the swarm then
// interrupts PX4's startup script, which makes PX4 log
// "Startup script returned with return value: 2" after the fact.
func px4Hung(err error) error {
	if !errors.Is(err, errLogTimeout) {
		return err
	}
	return fmt.Errorf("startup hung (the %q that follows in its log comes from stopping it, not from the hang): %w", px4FailedLine+": 2", err)
}

func readFrom(path string, offset int64) []byte {
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) <= offset {
		return nil
	}
	return data[offset:]
}

// lineWith returns the first line containing s, or "".
func lineWith(data []byte, s string) string {
	i := bytes.Index(data, []byte(s))
	if i < 0 {
		return ""
	}
	start := bytes.LastIndexByte(data[:i], '\n') + 1
	end := bytes.IndexByte(data[i:], '\n')
	if end < 0 {
		return string(data[start:])
	}
	return string(data[start : i+end])
}

// lastLines returns the last n non-empty lines, indented.
func lastLines(data []byte, n int) string {
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, "    "+l)
		}
	}
	if len(lines) == 0 {
		return "    (log is empty)"
	}
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
