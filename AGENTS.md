# drone-swarm-sim

Go launcher for simulated drone swarms (`swarmsim`). See README.md.

## Before committing

- `go vet ./...` and `go build ./...` pass; `gofmt -l .` prints nothing.
- `make lint` (golangci-lint, `.golangci.yml`) adds nothing new; the pre-commit hook runs it.
  Answer a false positive on its line with `//nolint:<linter> // <why>` or
  `// NOSONAR <why>`, never by closing the issue on the server.
- SonarQube, when `SONAR_HOST_URL` and `SONAR_TOKEN` are set: the `sonar` skill
  ([`.agents/skills/sonar`](.agents/skills/sonar/SKILL.md)) says how. Without them, skip it:
  it is a check on the maintainer's machine. Never print the token.
