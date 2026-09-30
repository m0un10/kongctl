VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -s -w \
  -X github.com/m0un10/kongctl/internal/cli.Version=$(VERSION) \
  -X github.com/m0un10/kongctl/internal/cli.Commit=$(COMMIT) \
  -X github.com/m0un10/kongctl/internal/cli.Date=$(DATE)

IMAGE ?= kongctl:dev
ENV   ?= dev

.PHONY: build test test-coverage vet lint tidy image cmp-test clean snapshot release

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/kongctl ./cmd/kongctl

test:
	go test ./...

# Coverage profile in the format the CI coverage report consumes.
test-coverage:
	go test -cover -coverprofile=coverage.txt ./... -count=1
	go tool cover -func=coverage.txt | tail -1

vet:
	go vet ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

image:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

# Simulates the Argo CD repo-server: the fixture repo is mounted read-only
# at the plugin's working directory and ARGOCD_ENV_* carry the parameters.
cmp-test: image
	docker run --rm -i \
	  -v "$(CURDIR)/testdata/repo:/tmp/repo:ro" -w /tmp/repo \
	  -e ARGOCD_ENV_ENV_NAME=$(ENV) \
	  -e ARGOCD_ENV_KIND=ExternalSecret \
	  -e ARGOCD_APP_NAME=example-$(ENV) \
	  $(IMAGE) cmp generate

clean:
	rm -rf bin dist coverage.txt

# Local, unpublished GoReleaser run to check the release build.
snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=docker,publish

# Cut a release: make release VERSION=v1.2.3. Tags main and pushes the tag;
# the release workflow does the rest.
release:
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=vX.Y.Z"; exit 2; }
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$$' || { echo "VERSION must be semver, e.g. v1.2.3"; exit 2; }
	@test -z "$$(git status --porcelain)" || { echo "working tree is dirty"; exit 2; }
	@test "$$(git rev-parse --abbrev-ref HEAD)" = main || { echo "release from main"; exit 2; }
	git fetch -q origin main && git diff --quiet HEAD origin/main || { echo "main is not in sync with origin"; exit 2; }
	git tag -a "$(VERSION)" -m "$(VERSION)"
	git push origin "$(VERSION)"
