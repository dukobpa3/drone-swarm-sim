---
name: sonar
description: Run, read and answer the local SonarQube analysis. Use before a commit and a push when SONAR_HOST_URL and SONAR_TOKEN are set, after opening a pull request, when the quality gate is red, or when asked about a SonarQube finding.
---

# SonarQube (the maintainer's local server)

The server is on the maintainer's LAN: there is no analysis in CI. Everything here is
for a checkout where `SONAR_HOST_URL` and `SONAR_TOKEN` are set (the environment or a
git-ignored `.env`; a worktree uses the main checkout's). **Without them, skip all of
it**: it is a check on the maintainer's machine, not a requirement for anyone else.

The standard: <https://github.com/eggs-gd/.github/blob/main/docs/sonarqube.md>.

## The flow

1. **Commit.** The pre-commit hook is the local gate: golangci-lint on the Go modules
   with staged files, and the secrets scan of the staged files when the `sonar` CLI is
   installed. A finding stops the commit. Nothing goes to the server.
2. **Push.** The pre-push hook runs `make sonar`: tests with coverage, the lint
   report, the scan of the pushed commit and the quality gate. The report goes to the
   server first; a red gate then stops the push. Read it, fix or answer, commit, push.
3. **After every push, when it has finished**, run `make sonar-publish`. The push's
   analysis was made while the commit was not on GitHub yet, so the check (the status
   on the commit) could not land on it: the plugin does not say so, the analysis just
   succeeds. `make sonar-publish` sends the scan again, on the reports `make sonar` wrote
   for this commit (no tests, no lint), now that GitHub knows the commit. Before the
   pull request is opened that analysis is of the branch; the pull request's comment
   and check come from an analysis made with its number (`scripts/sonar-scope` finds it),
   so it matters most right after the pull request is opened. It stops if the commit is
   not on the remote, if the tree has uncommitted changes, or if the reports are not of this
   commit: then commit, push, or `make sonar` first.
4. **Publish without waiting for the gate**, when a report has to reach the server
   whatever the gate says: `make sonar SONAR_GATE=false`.

## Reading what it found

`sonar list issues -p <projectKey> --format table`, the key being `sonar.projectKey` in
`sonar-project.properties`. The pull request's comment on GitHub has the gate and the counts.

## Answering a finding

- Fix it. A complexity or duplication finding is fixed by splitting the code, not by
  silencing the rule.
- A finding that is wrong for that line is answered **on the line**, with the reason:
  `// NOSONAR <why>`, or `//nolint:<linter> // <why>` for golangci-lint.
- A rule that is wrong for a whole class of files is scoped in `sonar-project.properties`
  (`sonar.issue.ignore.multicriteria`), with the reason in a comment next to it.
- The pull request's description says what the gate showed and how each remaining
  finding was answered.

## Never, unless the maintainer asks

- Print, log, commit, read aloud or ask for the value of `SONAR_TOKEN`: only check that it is set.
- Change the server's settings or quality profiles, or close an issue or mark it a
  false positive on the server.
- Run `sonar integrate` (it downloads a binary and can overwrite the hooks).
- Run `sonar-scanner` directly: without the arguments from `scripts/sonar-scope` a
  feature branch is published as the main branch. Use `make sonar`.
- Commit or push with `--no-verify`. If it is asked for, say so in the message.
