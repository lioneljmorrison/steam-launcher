# Build on WSL (or anywhere) for Windows, and install to a Windows path so
# "Start with Windows" doesn't depend on WSL running.
WIN_LOCALAPPDATA := $(shell wslpath "$$(cmd.exe /c 'echo %LOCALAPPDATA%' 2>/dev/null | tr -d '\r')" 2>/dev/null)
INSTALL_DIR := $(WIN_LOCALAPPDATA)/Programs/cardtridge

# Version comes from the latest git tag (e.g. v0.3.0, or v0.3.0-2-gabc123-dirty
# between releases). The update repo is the GitHub "owner/name" of origin.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REPO ?= $(shell git remote get-url origin 2>/dev/null | sed -E 's#^(https://github.com/|git@github.com:)##; s#\.git$$##')
LDFLAGS := -H=windowsgui -s -w -X main.version=$(VERSION) -X main.updateRepo=$(REPO)

DIST := dist

.PHONY: build install reload vet release
build:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o cardtridge.exe .

vet:
	GOOS=windows go vet ./...

install: build
	mkdir -p "$(INSTALL_DIR)"
	cp cardtridge.exe "$(INSTALL_DIR)/"

# Builds first so a failed build leaves the running copy alone, then stops it
# (and a Steam QR Launcher from before the rename), waits for it to exit so
# its folders and single-instance lock are free, installs, and starts the new
# copy with its window open on the QR page.
reload: build
	-taskkill.exe /IM cardtridge.exe /IM steam-launcher.exe /F /T >/dev/null 2>&1
	@for i in $$(seq 20); do tasklist.exe /NH | grep -qiE '^(cardtridge|steam-launcher)\.exe' || break; sleep 1; done
	mkdir -p "$(INSTALL_DIR)"
	@for i in $$(seq 20); do cp cardtridge.exe "$(INSTALL_DIR)/" 2>/dev/null && break; sleep 1; done
	cmp -s cardtridge.exe "$(INSTALL_DIR)/cardtridge.exe" || { echo "couldn't replace the installed exe"; exit 1; }
	cd /mnt/c && powershell.exe -NoProfile -Command "Start-Process -FilePath '$$(wslpath -w "$(INSTALL_DIR)/cardtridge.exe")' -ArgumentList '--show','/'"

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
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(DIST)/cardtridge.exe .
	printf '{\n  "version": "%s",\n  "url": "https://github.com/%s/releases/download/%s/cardtridge.exe",\n  "sha256": "%s"\n}\n' \
		"$(VERSION)" "$(REPO)" "$(VERSION)" "$$(sha256sum $(DIST)/cardtridge.exe | cut -d' ' -f1)" > $(DIST)/latest.json
	gh release create $(VERSION) $(DIST)/cardtridge.exe $(DIST)/latest.json --repo $(REPO) --title "$(VERSION)" --generate-notes $(PRERELEASE)
