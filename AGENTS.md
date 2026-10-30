# drone-swarm-sim

Go launcher for simulated drone swarms (`swarmsim`). See README.md.

## Before committing

- `go vet ./...` and `go build ./...` pass; `gofmt -l .` prints nothing.
- If `SONAR_HOST_URL` and `SONAR_TOKEN` are set (environment or `.env`):
  `make sonar` passes (quality gate green). See what it flagged with
  `sonar list issues --format table`. If they are not set, skip this: it is
  a check on the maintainer's machine.

## SonarQube rules

- Never print, log, commit or ask for the value of `SONAR_TOKEN`; only check
  that it is set.
- Do not change server settings or quality profiles, and do not close issues
  or mark them false positive on the server, unless asked.
- Do not run `sonar integrate` (it downloads a binary and can overwrite hooks).
- Do not commit with `--no-verify` unless asked, and say so in the message.
