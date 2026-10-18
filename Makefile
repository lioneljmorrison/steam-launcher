# Build on WSL (or anywhere) for Windows, and install to a Windows path so
# "Start with Windows" doesn't depend on WSL running.
WIN_LOCALAPPDATA := $(shell wslpath "$$(cmd.exe /c 'echo %LOCALAPPDATA%' 2>/dev/null | tr -d '\r')" 2>/dev/null)
INSTALL_DIR := $(WIN_LOCALAPPDATA)/Programs/steam-launcher

# Version comes from the latest git tag (e.g. v0.3.0, or v0.3.0-2-gabc123-dirty
# between releases). The update repo is the GitHub "owner/name" of origin.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REPO ?= $(shell git remote get-url origin 2>/dev/null | sed -E 's#^(https://github.com/|git@github.com:)##; s#\.git$$##')
LDFLAGS := -H=windowsgui -s -w -X main.version=$(VERSION) -X main.updateRepo=$(REPO)

DIST := dist

.PHONY: build install vet release
build:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o steam-launcher.exe .

vet:
	GOOS=windows go vet ./...

install: build
	mkdir -p "$(INSTALL_DIR)"
	cp steam-launcher.exe "$(INSTALL_DIR)/"

# make release VERSION=v0.3.0         (stable)
# make release VERSION=v0.3.0-beta.1  (pre-release, only offered on the beta channel)
# Tags the current commit, builds, and publishes a GitHub release with the exe
# and latest.json, which running copies check via "Check for updates".
PRERELEASE = $(if $(findstring -,$(VERSION)),--prerelease,)
release:
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-(beta|rc)\.[0-9]+)?$$' || { echo "usage: make release VERSION=vX.Y.Z or vX.Y.Z-beta.N (or -rc.N)"; exit 1; }
	@test -n "$(REPO)" || { echo "no GitHub origin remote"; exit 1; }
	@git diff --quiet && git diff --cached --quiet || { echo "commit your changes first"; exit 1; }
	git tag -a $(VERSION) -m "$(VERSION)"
	git push origin HEAD $(VERSION)
	rm -rf $(DIST) && mkdir -p $(DIST)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(DIST)/steam-launcher.exe .
	printf '{\n  "version": "%s",\n  "url": "https://github.com/%s/releases/download/%s/steam-launcher.exe",\n  "sha256": "%s"\n}\n' \
		"$(VERSION)" "$(REPO)" "$(VERSION)" "$$(sha256sum $(DIST)/steam-launcher.exe | cut -d' ' -f1)" > $(DIST)/latest.json
	gh release create $(VERSION) $(DIST)/steam-launcher.exe $(DIST)/latest.json --repo $(REPO) --title "$(VERSION)" --generate-notes $(PRERELEASE)
