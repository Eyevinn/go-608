.PHONY: all check vet lint build test coverage clean

CMDS = go608-extract go608-inject go608-clock go608-info

LDFLAGS = -X github.com/Eyevinn/go-608/internal.commitVersion=$$(git describe --tags --always HEAD) -X github.com/Eyevinn/go-608/internal.commitDate=$$(git log -1 --format=%ct)

all: check build test

check: vet lint

vet:
	go vet ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed; skipping (CI enforces it)"; \
	fi

build:
	go build ./...
	@mkdir -p out
	@for c in $(CMDS); do \
		echo "building out/$$c"; \
		go build -ldflags "$(LDFLAGS)" -o out/$$c ./cmd/$$c; \
	done

test:
	go test ./...

coverage:
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

clean:
	rm -rf out coverage.out
