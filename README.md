# drone-swarm-sim

`swarmsim` runs a simulated drone swarm from one profile in one console: the
gz world, then every drone, with all output merged and prefixed per process.
Ctrl+C stops everything; if the gz server dies, everything stops with an error.
If a PX4 startup hangs (90 s), swarmsim stops the swarm and prints the last
lines of that drone's log; the `Startup script returned with return value: 2`
PX4 logs afterwards comes from that stop, not from the hang.

```bash
go build -o bin/swarmsim ./cmd/swarmsim
./bin/swarmsim check profiles/px4-4x-gz.yaml   # validate, print drones, ports, commands
./bin/swarmsim up profiles/px4-4x-gz.yaml
```

or `go install github.com/dukobpa3/drone-swarm-sim/cmd/swarmsim@latest`.

## Two files

- **lab.yaml** says what is installed on this machine (gz, PX4 build,
  ArduPilot build). It is not in this repo: copy `lab.example.yaml` next to
  your checkouts. swarmsim looks for `--lab`, then `$SWARM_LAB`, then
  `lab.yaml` in the current directory or a parent, then
  `~/.config/swarmsim/lab.yaml`.
- **profiles/*.yaml** say what to run, with no paths into the lab.

swarmsim does not install or build autopilots or simulators. It checks that
what `lab.yaml` points at exists and fails with an error naming what is
missing.

## Backends

| autopilot | sim | notes |
|---|---|---|
| `px4` | `gz` | joins the shared world (`PX4_GZ_STANDALONE`), `model: gz_x500` etc. |
| `px4` | `sih` | PX4's own physics, `model: sihsim_quadx` |
| `ardupilot` | `builtin` | `vehicle: copter\|plane\|rover`; not run end to end yet |
| `exec` | any | any command per drone, Go templates over `.Name .Index .SysID .X .Y .Lat .Lon .Alt .Dir .World` |

The gz server is started by swarmsim, not by the first PX4, so no drone is
special. gz drones keep the world's own origin unless the profile sets
`world.home`.

Each drone gets a swarm-wide index; MAVLink sysid is index + 1. PX4 ports come
from the index (GCS 18570+i → 14550, API 14580+i → 14540+i), ArduPilot sends
SERIAL0 to 14550 and serves SERIAL1 on TCP 5762+10·i. `check` prints them.
PX4 indexes above 9 are refused: PX4 gives them all offboard port 14549.

Per-drone working directories (params, missions, logs) live in
`<run_dir>/<profile>/<drone>` and survive between runs; console output is also
in `<run_dir>/<profile>/logs/`.

`params` in a group are set after boot for PX4 (`px4-param set`) and as an
extra defaults file for ArduPilot, which means values saved in a drone's
eeprom win over them.

## Where it is developed

In `sim/` of [dukobpa3/drone-swarm](https://github.com/dukobpa3/drone-swarm).
github.com/dukobpa3/drone-swarm-sim is a mirror of that directory, updated with
`make sim-push` from the monorepo; pull requests go to the monorepo.
