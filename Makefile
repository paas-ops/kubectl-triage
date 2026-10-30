VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: build test vet fmt run

build:
	go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)" -o kubectl-triage ./cmd/kubectl-triage

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

run:
	go run ./cmd/kubectl-triage
