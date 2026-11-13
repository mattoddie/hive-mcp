VERSION ?= 0.0.0-dev
LDFLAGS := -s -w -X github.com/mattoddie/hive-mcp/internal/tools.Version=$(VERSION)
export CGO_ENABLED := 0

# Platforms for the MCP Bundles (one .mcpb each), as GOOS/GOARCH.
MCPB_PLATFORMS := darwin/arm64 darwin/amd64 windows/amd64 windows/arm64 linux/amd64 linux/arm64

.PHONY: build test lint docs docs-check image mcpb dist clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/hive-mcp ./cmd/hive-mcp

test:
	go test ./...

lint:
	gofmt -l . | (! grep .) || (echo "Run gofmt -w ." && exit 1)
	go vet ./...

docs:
	go run ./cmd/gen-tool-docs
	go run ./cmd/mcpb manifest

docs-check:
	go run ./cmd/gen-tool-docs -check
	go run ./cmd/mcpb manifest -check
	go run ./cmd/mcpb registry

image:
	docker build --build-arg VERSION=$(VERSION) -t hive-mcp:$(VERSION) .

# MCP Bundles for Claude Desktop: dist/hive-mcp-$(VERSION)-<os>-<arch>.mcpb.
mcpb:
	rm -rf .build/mcpb dist/*.mcpb
	go build -o .build/mcpb/pack ./cmd/mcpb
	set -e; for p in $(MCPB_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o .build/mcpb/$$os-$$arch/hive-mcp$$ext ./cmd/hive-mcp; \
		.build/mcpb/pack pack -version $(VERSION) -goos $$os -binary .build/mcpb/$$os-$$arch/hive-mcp$$ext \
			-o dist/hive-mcp-$(VERSION)-$$os-$$arch.mcpb; \
		done

# Release assets: the bundles, plus the same binaries as plain archives with the licence
# (dist/hive-mcp-$(VERSION)-<os>-<arch>.tar.gz, or .zip for Windows) and dist/SHA256SUMS.
dist: mcpb
	rm -f dist/*.tar.gz dist/*.zip dist/SHA256SUMS
	set -e; for p in $(MCPB_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; dir=.build/mcpb/$$os-$$arch; name=hive-mcp-$(VERSION)-$$os-$$arch; \
		cp LICENSE $$dir/; \
		if [ "$$os" = windows ]; then \
			(cd $$dir && zip -q -X ../../../dist/$$name.zip hive-mcp.exe LICENSE); \
		else \
			tar -czf dist/$$name.tar.gz -C $$dir hive-mcp LICENSE; \
		fi; \
	done
	cd dist && if command -v sha256sum >/dev/null; then sha256sum *.mcpb *.tar.gz *.zip; \
		else shasum -a 256 *.mcpb *.tar.gz *.zip; fi > SHA256SUMS

clean:
	rm -rf bin .build dist
