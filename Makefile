GO ?= go

.PHONY: all build portable line-cap density vocab shape mcode-plugin test dogfood install uninstall bump-version release-check

all: build portable line-cap density vocab shape mcode-plugin test dogfood

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o bin/mcode-router ./cmd/mcode-router
	ln -sfn mcode-router bin/mcode-router-mcp

portable:
	$(GO) run ./qa/gates --gate portable

line-cap:
	$(GO) run ./qa/gates --gate line-cap

density:
	$(GO) run ./qa/gates --gate density

vocab:
	$(GO) run ./qa/gates --gate vocab

shape:
	$(GO) run ./qa/gates --gate shape

mcode-plugin:
	$(GO) run ./qa/gates --gate mcode-plugin

test: build
	$(GO) vet ./... && $(GO) test -race -count=1 ./...

dogfood: build
	@set -e; tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	export MCODE_ROUTER_QUOTA_FILE="$$tmp/quota.json" MCODE_ROUTER_DISCOVERY_FILE="$$tmp/discovery.json" \
	  OODA_NO_AUTO_PROBE=1 MCODE_ROUTER_DEPTH=0 ROUTER_DEPTH=0; \
	ver=$$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' plugins/mcode-router/.minimax-plugin/plugin.json); \
	./bin/mcode-router --version | grep -q "mcode-router v$$ver"; \
	./bin/mcode-router --status >/dev/null; \
	./bin/mcode-router --discover >/dev/null; \
	./bin/mcode-router --usage >/dev/null; \
	out=$$(MCODE_ROUTER_DEPTH=0 ROUTER_DEPTH=1 ./bin/mcode-router --recon "probe" 2>&1 || true); \
	printf '%s\n' "$$out" | grep -q "recursive mcode-router dispatch blocked"; \
	out=$$(MCODE_ROUTER_DEPTH=1 ROUTER_DEPTH=0 ./bin/mcode-router --recon "probe" 2>&1 || true); \
	printf '%s\n' "$$out" | grep -q "recursive mcode-router dispatch blocked"; \
	echo "dogfood: ok (cli version/status/discover/usage + recursion guard)"

# Not part of `all`: these mutate $HOME (skills, agent profiles, MCP entry, hooks).
install:
	./install.sh

uninstall:
	./install.sh --uninstall

# Bumps the CLI version constant, the plugin manifest and the changelog together.
bump-version:
	@test -n "$(VERSION)" || { echo "usage: make bump-version VERSION=1.0.6"; exit 2; }
	$(GO) run ./cmd/release bump $(VERSION)

# Verifies that every release file carries the same version.
release-check:
	$(GO) run ./cmd/release check
