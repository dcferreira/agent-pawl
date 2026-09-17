.PHONY: build test install fmt vet check

build:
	go build -o bin/wf ./cmd/wf

test:
	go test ./...

install:
	go install ./cmd/wf

fmt:
	go fmt ./...

vet:
	go vet ./...

check: fmt vet test
