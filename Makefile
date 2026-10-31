.PHONY: build test lint sonar

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
	@env=.env; [ -f $$env ] || env="$$(git rev-parse --path-format=absolute --git-common-dir)/../.env"; \
	set -a; [ ! -f "$$env" ] || . "$$env"; set +a; \
	test -n "$$SONAR_HOST_URL" && test -n "$$SONAR_TOKEN" || { echo 'sonar: set SONAR_HOST_URL and SONAR_TOKEN (.env)' >&2; exit 1; }; \
	scope=$$(scripts/sonar-scope) || exit 1; \
	echo "sonar: $${scope:-main branch}"; \
	go test -coverpkg=./... -coverprofile=coverage.out ./... && \
	{ golangci-lint run --issues-exit-code=0 --output.text.path=stderr \
	    --output.checkstyle.path=$(CURDIR)/golangci-lint.xml ./... 2> /dev/null || exit 1; } && \
	sonar-scanner -Dsonar.qualitygate.wait=$(SONAR_GATE) $$scope
