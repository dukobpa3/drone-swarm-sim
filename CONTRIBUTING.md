# Contributing

```bash
make build   # bin/swarmsim
make test
make lint    # golangci-lint v2 (brew install golangci-lint)
```

## SonarQube (optional)

The maintainer runs a self-hosted SonarQube that is only reachable from their
LAN, so there is no analysis in CI. Without the two variables below nothing
here runs, and you can ignore this section.

To use it you need `sonar-scanner` (and, for reading results, the `sonar`
CLI) and a `.env` in the repository root, which git ignores:

```
SONAR_HOST_URL=http://<host>:9000
SONAR_TOKEN=<user token>
```

Keep it private: `chmod 600 .env`.

- `make sonar` runs the tests with coverage, golangci-lint (its report is
  imported into Sonar as external issues), the scan and the quality gate;
  a red gate makes it fail. On the main branch it updates the project; on
  another branch it is analyzed as that branch's open pull request (found
  with `gh`), or as a branch when there is none. Branches and pull requests
  need the community branch plugin on the server. `make sonar SONAR_GATE=false`
  sends the report without waiting for the gate.
- `git config core.hooksPath .githooks` enables two hooks. **pre-commit** is the
  local gate: golangci-lint on the Go modules with staged files (and the secrets
  scan of the staged files, when the `sonar` CLI is installed); a finding stops
  the commit, nothing goes to the server. **pre-push** runs `make sonar` when
  code under `cmd/` or `internal/` changed: the report of the pushed commit goes
  to the server, and a red gate stops the push. Once a pull request is open, run
  `make sonar` once more so the server analyzes it as that pull request. Skip a
  hook for one commit or push with `--no-verify`.
- `sonar list issues -p drone-swarm-sim --format table` shows what the last scan found.

The same setup, and the reasons for it, are described in the organization's
[SonarQube standard](https://github.com/eggs-gd/.github/blob/main/docs/sonarqube.md).
