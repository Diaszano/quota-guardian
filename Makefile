VERSION ?= dev

.PHONY: check test race vet architecture fmt build

check:
	test -z "$(shell gofmt -l cmd internal tests)"
	go vet ./...
	go test ./...
	go test -race ./...
	go test ./tests -run TestArchitecture
	pre-commit run --all-files

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

architecture:
	go test ./tests -run TestArchitecture

fmt:
	gofmt -w cmd internal tests

build:
	mkdir -p dist
	go build -ldflags "-X main.version=$(VERSION)" -o dist/quota-guardian ./cmd/quota-guardian
