// Package config loads the two files swarmsim works from: the lab file, which
// says where things are installed on this machine, and a swarm profile, which
// says what to run. Profiles never contain paths into the lab.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const LabFileName = "lab.yaml"

// Lab describes one machine. Every section is optional; a profile that needs a
// missing section fails validation with an error naming it.
type Lab struct {
	// RunDir holds per-drone working directories (params, logs) between runs.
	RunDir    string        `yaml:"run_dir"`
	Gz        *LabGz        `yaml:"gz"`
	PX4       *LabPX4       `yaml:"px4"`
	ArduPilot *LabArduPilot `yaml:"ardupilot"`

	path string
}

type LabGz struct {
	// Activate is a shell script that puts gz on PATH (drone-lab/gz/activate.sh).
	// Empty means gz is already on PATH.
	Activate string `yaml:"activate"`
}

type LabPX4 struct {
	// Build is a PX4 SITL build directory, e.g. PX4-Autopilot/build/px4_sitl_default.
	Build string `yaml:"build"`
}

type LabArduPilot struct {
	// Root is the ardupilot checkout, used for default parameter files.
	Root string `yaml:"root"`
	// Bin is the directory with arducopter, arduplane, ardurover...
	Bin string `yaml:"bin"`
}

func (l *Lab) Path() string { return l.path }

// FindLab resolves the lab file: explicit path, then $SWARM_LAB, then lab.yaml
// in the current directory or any parent, then ~/.config/swarmsim/lab.yaml.
func FindLab(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if p := os.Getenv("SWARM_LAB"); p != "" {
		return p, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, LabFileName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".config", "swarmsim", LabFileName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no lab file: pass --lab, set SWARM_LAB, or put lab.yaml in this directory or a parent")
}

func LoadLab(path string) (*Lab, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var lab Lab
	if err := loadYAML(path, &lab); err != nil {
		return nil, err
	}
	lab.path = path
	base := filepath.Dir(path)
	lab.RunDir = expand(base, lab.RunDir)
	if lab.RunDir == "" {
		lab.RunDir = filepath.Join(base, "run")
	}
	if lab.Gz != nil {
		lab.Gz.Activate = expand(base, lab.Gz.Activate)
	}
	if lab.PX4 != nil {
		lab.PX4.Build = expand(base, lab.PX4.Build)
	}
	if lab.ArduPilot != nil {
		lab.ArduPilot.Root = expand(base, lab.ArduPilot.Root)
		lab.ArduPilot.Bin = expand(base, lab.ArduPilot.Bin)
	}
	return &lab, nil
}

func loadYAML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// expand resolves ~ and paths relative to the file that mentions them.
func expand(base, p string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}
