// swarmsim runs a simulated swarm from a profile in one console.
//
//	swarmsim [--lab lab.yaml] check <profile.yaml>   validate and print the plan
//	swarmsim [--lab lab.yaml] up <profile.yaml>      run until Ctrl+C
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/dukobpa3/drone-swarm-sim/internal/backend"
	"github.com/dukobpa3/drone-swarm-sim/internal/config"
	"github.com/dukobpa3/drone-swarm-sim/internal/supervisor"
)

func main() {
	labFlag := flag.String("lab", "", "lab file (default: $SWARM_LAB, lab.yaml in . or a parent, ~/.config/swarmsim/lab.yaml)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: swarmsim [--lab lab.yaml] check|up <profile.yaml>\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*labFlag, flag.Arg(0), flag.Arg(1)); err != nil {
		fmt.Fprintln(os.Stderr, "swarmsim:", err)
		os.Exit(1)
	}
}

func run(labFlag, command, profilePath string) error {
	if command != "check" && command != "up" {
		return fmt.Errorf("unknown command %q: use check or up", command)
	}
	labPath, err := config.FindLab(labFlag)
	if err != nil {
		return err
	}
	lab, err := config.LoadLab(labPath)
	if err != nil {
		return err
	}
	prof, err := config.LoadProfile(profilePath)
	if err != nil {
		return err
	}
	specs, err := backend.Plan(lab, prof)
	if err != nil {
		return err
	}
	drones, _ := prof.Drones()
	printPlan(lab, prof, drones, specs)
	if command == "check" {
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sup := &supervisor.Supervisor{Stagger: time.Second, StopTimeout: 10 * time.Second}
	return sup.Run(ctx, specs)
}

func printPlan(lab *config.Lab, prof *config.Profile, drones []config.Drone, specs []supervisor.Spec) {
	fmt.Printf("lab:     %s\nprofile: %s (%s)\nrun dir: %s/%s\n\n", lab.Path(), prof.Path(), prof.Name, lab.RunDir, prof.Name)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DRONE\tAUTOPILOT\tSIM\tSYSID\tPOS\tMAVLINK")
	for _, d := range drones {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%g,%g\t%s\n", d.Name, d.Group.Autopilot, d.Group.Sim, d.SysID, d.X, d.Y, ports(d))
	}
	w.Flush()
	fmt.Println()
	for _, s := range specs {
		fmt.Printf("  %-12s %s %s\n", s.Name, s.Path, strings.Join(s.Args, " "))
	}
	fmt.Println()
}

func ports(d config.Drone) string {
	i := d.Index
	switch d.Group.Autopilot {
	case "px4":
		return fmt.Sprintf("gcs udp %d->14550, api udp %d->%d", 18570+i, 14580+i, 14540+i)
	case "ardupilot":
		return fmt.Sprintf("gcs udp ->14550, api tcp %d", 5762+10*i)
	}
	return "-"
}
