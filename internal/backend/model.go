package backend

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/envcap"
)

// A vehicle with payloads is built by swarmsim: the base model is merged in
// (so its links, sensors and plugins stay where the autopilot expects them),
// and each payload is a nested model named after the payload, held to the
// base's base_link by a fixed joint. Nesting keeps payload link and sensor
// names from clashing, and scopes their topics:
//   /world/<world>/model/<drone>/model/<payload>/link/<link>/sensor/<sensor>/...

const mountLink = "base_link"

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type mount struct {
	config.Payload
	link string // the payload's link the joint holds
}

// checkVehicle validates base and payloads of a group against the gz model
// search path and returns the mounts.
func checkVehicle(env *Env, g *config.Group, base string) ([]mount, error) {
	if g.Sim != "gz" {
		return nil, fmt.Errorf("payloads need sim gz, not %q", g.Sim)
	}
	if _, err := findModel(env, base); err != nil {
		return nil, fmt.Errorf("base: %w", err)
	}
	seen := map[string]bool{}
	var mounts []mount
	for _, p := range g.Payloads {
		if p.Model == "" {
			return nil, fmt.Errorf("payload %q: no model", p.Name)
		}
		if !safeName.MatchString(p.Name) {
			return nil, fmt.Errorf("payload name %q: use letters, digits, _ and -", p.Name)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("payload name %q is used twice, give one a name", p.Name)
		}
		seen[p.Name] = true
		if len(p.Pose) != 0 && len(p.Pose) != 3 && len(p.Pose) != 6 {
			return nil, fmt.Errorf("payload %q: pose is x y z or x y z roll pitch yaw", p.Name)
		}
		dir, err := findModel(env, p.Model)
		if err != nil {
			return nil, fmt.Errorf("payload %q: %w", p.Name, err)
		}
		link, err := firstLink(dir)
		if err != nil {
			return nil, fmt.Errorf("payload %q: %w", p.Name, err)
		}
		mounts = append(mounts, mount{Payload: p, link: link})
	}
	return mounts, nil
}

// findModel looks a model up on GZ_SIM_RESOURCE_PATH, as gz does for model://.
func findModel(env *Env, name string) (string, error) {
	var tried []string
	for _, dir := range strings.Split(envcap.Lookup(env.Gz, "GZ_SIM_RESOURCE_PATH"), ":") {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if _, err := os.Stat(filepath.Join(p, "model.config")); err == nil {
			return p, nil
		}
		tried = append(tried, dir)
	}
	return "", fmt.Errorf("model %q not found in %s", name, strings.Join(tried, ", "))
}

// modelFile is the model's SDF file named in its model.config: the last
// <sdf>, the newest version.
func modelFile(dir string) string {
	var cfg struct {
		SDF []string `xml:"sdf"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "model.config")); err == nil {
		if xml.Unmarshal(data, &cfg) == nil && len(cfg.SDF) > 0 {
			return strings.TrimSpace(cfg.SDF[len(cfg.SDF)-1])
		}
	}
	return "model.sdf"
}

// firstLink returns the name of the first link of the model's SDF.
func firstLink(dir string) (string, error) {
	file := modelFile(dir)
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return "", err
	}
	var sdf struct {
		Model struct {
			Links []struct {
				Name string `xml:"name,attr"`
			} `xml:"link"`
		} `xml:"model"`
	}
	if err := xml.Unmarshal(data, &sdf); err != nil {
		return "", fmt.Errorf("%s: %w", filepath.Join(dir, file), err)
	}
	if len(sdf.Model.Links) == 0 {
		return "", fmt.Errorf("%s: no link to mount", filepath.Join(dir, file))
	}
	return sdf.Model.Links[0].Name, nil
}

// composeModel writes the vehicle SDF.
func composeModel(name, base string, mounts []mount) []byte {
	var b bytes.Buffer
	esc := func(s string) string {
		var e bytes.Buffer
		_ = xml.EscapeText(&e, []byte(s))
		return e.String()
	}
	fmt.Fprintf(&b, "<?xml version=\"1.0\"?>\n<!-- written by swarmsim, overwritten on every run -->\n")
	fmt.Fprintf(&b, "<sdf version=\"1.9\">\n  <model name=\"%s\">\n", esc(name))
	fmt.Fprintf(&b, "    <include merge=\"true\">\n      <uri>model://%s</uri>\n    </include>\n", esc(base))
	for _, m := range mounts {
		pose := make([]float64, 6)
		copy(pose, m.Pose)
		// relative to the mounting link: a base model may place it off its own frame
		fmt.Fprintf(&b, "    <include>\n      <name>%s</name>\n      <uri>model://%s</uri>\n      <pose relative_to=\"%s\">%g %g %g %g %g %g</pose>\n    </include>\n",
			esc(m.Name), esc(m.Model), mountLink, pose[0], pose[1], pose[2], pose[3], pose[4], pose[5])
		fmt.Fprintf(&b, "    <joint name=\"%s_mount\" type=\"fixed\">\n      <parent>%s</parent>\n      <child>%s::%s</child>\n    </joint>\n",
			esc(m.Name), mountLink, esc(m.Name), esc(m.link))
	}
	b.WriteString("  </model>\n</sdf>\n")
	return b.Bytes()
}

// spawnModel asks the running world to create the model from an SDF file.
func spawnModel(ctx context.Context, env *Env, sdfPath, name string, x, y float64) error {
	gz, err := envcap.LookPath(env.Gz, "gz")
	if err != nil {
		return err
	}
	req := fmt.Sprintf(`sdf_filename: %q, name: %q, allow_renaming: false, pose: {position: {x: %g, y: %g, z: 0}}`, sdfPath, name, x, y)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gz, "service", "-s", "/world/"+env.Profile.WorldName()+"/create",
		"--reqtype", "gz.msgs.EntityFactory", "--reptype", "gz.msgs.Boolean", "--timeout", "10000", "--req", req)
	cmd.Env = env.Gz
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("spawn %s from %s: %w: %s", name, sdfPath, err, bytes.TrimSpace(out))
	}
	if !bytes.Contains(out, []byte("data: true")) {
		return fmt.Errorf("spawn %s from %s: world refused it: %s", name, sdfPath, bytes.TrimSpace(out))
	}
	return nil
}
