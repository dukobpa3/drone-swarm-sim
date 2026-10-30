.PHONY: build test sonar

build:
	go build -o bin/swarmsim ./cmd/swarmsim

test:
	go test ./...

# Tests with coverage, SonarQube scan and quality gate; a red gate fails.
# Needs SONAR_HOST_URL and SONAR_TOKEN, from the environment or .env.
sonar:
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	test -n "$$SONAR_HOST_URL" && test -n "$$SONAR_TOKEN" || { echo 'Set SONAR_HOST_URL and SONAR_TOKEN (see CONTRIBUTING.md)'; exit 1; }; \
	go test -coverprofile=coverage.out ./... && \
	sonar-scanner -Dsonar.qualitygate.wait=true
