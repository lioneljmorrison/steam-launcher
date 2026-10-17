# Build on WSL (or anywhere) for Windows, and install to a Windows path so
# "Start with Windows" doesn't depend on WSL running.
WIN_LOCALAPPDATA := $(shell wslpath "$$(cmd.exe /c 'echo %LOCALAPPDATA%' 2>/dev/null | tr -d '\r')" 2>/dev/null)
INSTALL_DIR := $(WIN_LOCALAPPDATA)/Programs/steam-launcher

.PHONY: build install vet
build:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-H=windowsgui -s -w" -o steam-launcher.exe .

vet:
	GOOS=windows go vet ./...

install: build
	mkdir -p "$(INSTALL_DIR)"
	cp steam-launcher.exe "$(INSTALL_DIR)/"
