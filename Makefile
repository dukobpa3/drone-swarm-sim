.PHONY: build test lint sonar

build:
	go build -o bin/swarmsim ./cmd/swarmsim

test:
	go test ./...

lint:
	golangci-lint run ./...

# Tests with coverage, a golangci-lint report, SonarQube scan and quality
# gate; a red gate fails. Lint findings do not stop it: Sonar imports them
# and the gate decides. Needs SONAR_HOST_URL and SONAR_TOKEN, from the
# environment or .env.
sonar:
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	test -n "$$SONAR_HOST_URL" && test -n "$$SONAR_TOKEN" || { echo 'Set SONAR_HOST_URL and SONAR_TOKEN (see CONTRIBUTING.md)'; exit 1; }; \
	go test -coverprofile=coverage.out ./... && \
	{ golangci-lint run --issues-exit-code=0 --output.text.path=stderr \
	    --output.checkstyle.path=$(CURDIR)/golangci-lint.xml ./... 2> /dev/null || exit 1; } && \
	sonar-scanner -Dsonar.qualitygate.wait=true
