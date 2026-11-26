# Socratex cross-build Makefile
# Usage:
#   make              # build for current OS/arch
#   make all          # build all common targets into dist/
#   make linux-amd64  # single target
#   make release      # all targets + checksums

APP       := socratex
CMD       := ./cmd/socratex
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "2.0.0")
LDFLAGS   := -s -w -X main.version=$(VERSION)
DIST      := dist

PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	windows/amd64 \
	windows/386 \
	darwin/arm64 \
	darwin/amd64

.PHONY: build all release clean linux-amd64 linux-arm64 windows-amd64 windows-386 darwin-arm64 darwin-amd64

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(APP) $(CMD)

linux-amd64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP)-linux-amd64 $(CMD)

linux-arm64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP)-linux-arm64 $(CMD)

windows-amd64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP)-windows-amd64.exe $(CMD)

windows-386:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP)-windows-386.exe $(CMD)

darwin-arm64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP)-darwin-arm64 $(CMD)

darwin-amd64:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(DIST)/$(APP)-darwin-amd64 $(CMD)

all: linux-amd64 linux-arm64 windows-amd64 windows-386 darwin-arm64 darwin-amd64
	@echo ""
	@echo "Built binaries in $(DIST)/:"
	@ls -lh $(DIST)/

release: all
	@cd $(DIST) && sha256sum * > SHA256SUMS 2>/dev/null || shasum -a 256 * > SHA256SUMS
	@echo "Checksums written to $(DIST)/SHA256SUMS"
	@cat $(DIST)/SHA256SUMS

clean:
	rm -rf $(DIST) $(APP) $(APP).exe
