.PHONY: build test install fmt fmt-check vet check

build:
	go build -o bin/pawl ./cmd/pawl

test:
	go test ./...

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

check: fmt vet test
