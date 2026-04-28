NUON_REPO_ROOT ?= /Users/harsh/work/nuonco/nuon
EXTENSION_PKG := ./bins/cli/extensions/nuon-ext-mirror
EXTENSION_PKGS := ./bins/cli/extensions/nuon-ext-mirror/...
BINARY := nuon-ext-mirror

.PHONY: build test fmt vet clean check-repo

check-repo:
	@test -f "$(NUON_REPO_ROOT)/go.mod" || \
		(echo "NUON_REPO_ROOT must point to the Nuon monorepo root (missing go.mod at $(NUON_REPO_ROOT))" && exit 1)

build:
	go build -o "$(CURDIR)/$(BINARY)" .

build-monorepo: check-repo
	go -C "$(NUON_REPO_ROOT)" build -o "$(CURDIR)/$(BINARY)" "$(EXTENSION_PKG)"

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -f "$(BINARY)"
	rm -rf dist/
