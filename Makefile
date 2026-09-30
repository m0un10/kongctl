VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -s -w \
  -X github.com/m0un10/kongctl/internal/cli.Version=$(VERSION) \
  -X github.com/m0un10/kongctl/internal/cli.Commit=$(COMMIT) \
  -X github.com/m0un10/kongctl/internal/cli.Date=$(DATE)

IMAGE ?= kongctl:dev
ENV   ?= dev

.PHONY: build test vet lint tidy image cmp-test clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/kongctl ./cmd/kongctl

test:
	go test ./...

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
	rm -rf bin dist
