GO ?= go
VERSION ?= dev
COMMIT ?= none
DATE ?= unknown
LDFLAGS := -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.Date=$(DATE)

.PHONY: build test vet fmt-check cross-build clean

build:
	@mkdir -p build
	$(GO) build -ldflags "$(LDFLAGS)" -o build/local-ai ./cmd/local-ai

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$(find cmd internal -name '*.go' -type f -exec gofmt -l {} +)"

cross-build:
	@mkdir -p build
	@set -eu; \
	for target in \
		windows/amd64 windows/arm64 \
		darwin/amd64 darwin/arm64 \
		linux/amd64 linux/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; suffix=; \
		if [ "$$os" = windows ]; then suffix=.exe; fi; \
		echo "building $$target"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build \
			-ldflags "$(LDFLAGS)" \
			-o "build/local-ai_$${os}_$${arch}$${suffix}" ./cmd/local-ai; \
	done

clean:
	rm -rf build
