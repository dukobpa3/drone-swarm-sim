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
  need the community branch plugin on the server.
- `git config core.hooksPath .githooks` enables a pre-commit hook that runs
  `make sonar` when Go code under `cmd/` or `internal/` is staged. Skip it
  for one commit with `git commit --no-verify`.
- `sonar list issues --format table` shows what the last scan found.
