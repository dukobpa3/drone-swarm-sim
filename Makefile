.PHONY: build test lint sonar sonar-publish

build:
	go build -o bin/swarmsim ./cmd/swarmsim

test:
	go test ./...

lint:
	golangci-lint run ./...

# Tests with coverage, golangci-lint's report, the scan of this checkout and the
# quality gate (red: a non-zero exit). Lint findings do not stop it: Sonar imports
# them and the gate decides. SONAR_HOST_URL and SONAR_TOKEN come from the environment
# or .env (a worktree uses the main checkout's). scripts/sonar-scope says whether this
# is the main branch, a pull request or another branch. SONAR_GATE=false sends the
# report without waiting for the gate.
SONAR_GATE ?= true

sonar:
	@env=./.env; [ -f $$env ] || env="$$(git rev-parse --path-format=absolute --git-common-dir)/../.env"; \
	set -a; [ ! -f "$$env" ] || . "$$env"; set +a; \
	test -n "$$SONAR_HOST_URL" && test -n "$$SONAR_TOKEN" || { echo 'sonar: set SONAR_HOST_URL and SONAR_TOKEN (.env)' >&2; exit 1; }; \
	scope=$$(scripts/sonar-scope) || exit 1; \
	echo "sonar: $${scope:-main branch}"; \
	go test -coverpkg=./... -coverprofile=coverage.out ./... && \
	{ golangci-lint run --issues-exit-code=0 --output.text.path=stderr \
	    --output.checkstyle.path=$(CURDIR)/golangci-lint.xml ./... 2> /dev/null || exit 1; } && \
	{ if [ -z "$$(git status --porcelain --untracked-files=no)" ]; then git rev-parse HEAD > "$$(git rev-parse --git-path sonar-reports-head)"; else rm -f "$$(git rev-parse --git-path sonar-reports-head)"; fi; } && \
	sonar-scanner -Dsonar.qualitygate.wait=$(SONAR_GATE) $$scope

# The scan again, on the reports `make sonar` wrote for this commit, once the commit is on GitHub:
# the pull request's check is made for a commit GitHub knows, and at pre-push it does not yet.
sonar-publish:
	@env=./.env; [ -f $$env ] || env="$$(git rev-parse --path-format=absolute --git-common-dir)/../.env"; \
	set -a; [ ! -f "$$env" ] || . "$$env"; set +a; \
	test -n "$$SONAR_HOST_URL" && test -n "$$SONAR_TOKEN" || { echo 'sonar-publish: set SONAR_HOST_URL and SONAR_TOKEN (.env)' >&2; exit 1; }; \
	[ -z "$$(git status --porcelain --untracked-files=no)" ] || { echo 'sonar-publish: uncommitted changes: the scan would not match the reports of the commit' >&2; exit 1; }; \
	[ "$$(cat "$$(git rev-parse --git-path sonar-reports-head)" 2>/dev/null)" = "$$(git rev-parse HEAD)" ] || { echo 'sonar-publish: no reports of this commit: run make sonar first' >&2; exit 1; }; \
	git ls-remote origin "refs/heads/$$(git rev-parse --abbrev-ref HEAD)" | grep -q "^$$(git rev-parse HEAD)" || { echo 'sonar-publish: this commit is not on the remote yet: push it first' >&2; exit 1; }; \
	scope=$$(scripts/sonar-scope) || exit 1; \
	echo "sonar: $${scope:-main branch} (the reports of this commit, sent again)"; \
	sonar-scanner -Dsonar.qualitygate.wait=false $$scope
