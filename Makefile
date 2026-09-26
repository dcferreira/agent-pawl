.PHONY: build test test-race install fmt fmt-check vet staticcheck test-install test-release-checks check

# Output goes to dist/, not bin/: bin/ is a committed plugin directory
# (bin/pawl is the plugin's wrapper script), so a compiled binary must not
# land there.
build:
	go build -o dist/pawl ./cmd/pawl

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

# Unit-tests install.sh's pure logic (OS/arch detection, asset naming,
# version handling, checksum parsing) by sourcing it — no network, no
# bats or other extra tooling, just plain shell assertions.
test-install:
	bash scripts/test-install.sh

# Unit-tests the release-management check scripts (scripts/release/) against
# throwaway git fixture repos — no network, no GitHub API. See
# docs/releasing.md.
test-release-checks:
	bash scripts/release/test-checks.sh

check: fmt vet test test-install test-release-checks
