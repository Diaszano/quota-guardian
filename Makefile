VERSION ?= dev

.PHONY: test race vet architecture fmt build

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
