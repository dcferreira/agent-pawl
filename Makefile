.PHONY: build test test-race install fmt fmt-check vet staticcheck check

build:
	go build -o bin/pawl ./cmd/pawl

test:
	go test ./...

test-race:
	go test -race ./...

install:
	go install ./cmd/pawl

fmt:
	go fmt ./...

fmt-check:
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "not gofmt'd:"; echo "$$files"; exit 1; \
	fi

vet:
	go vet ./...

# Pinned here rather than assumed on PATH, so a fresh clone needs only Go.
staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...

check: fmt vet test
