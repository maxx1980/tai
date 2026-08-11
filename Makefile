.PHONY: all ui build build-webview run clean deps dev icons syso build-windows setup-syso build-windows-setup launcher-syso build-windows-launcher uninstall-syso build-windows-uninstall install-desktop uninstall-desktop dist dist-termux-src dist-windows dist-all

BINARY := webssh
# VERSION is stamped into the binary so the updater can compare it with the
# newest tag on GitHub. A build from a tarball (no .git) has no version; the
# updater then reports "dev" and never offers an update it cannot reason about.
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X webssh/internal/version.Version=$(VERSION)
# go-winres declares an older go directive, so `go run pkg@ver` would fall back to
# whatever toolchain is on PATH; pin it to the one this module already builds with.
WINRES := GOTOOLCHAIN=$(shell go env GOVERSION) go run github.com/tc-hib/go-winres@v0.3.3
ICON_SIZES := 16 24 32 48 64 128 256 512
DESKTOP_DIR := $(HOME)/.local/share/applications
ICON_DIR := $(HOME)/.local/share/icons/hicolor
# Release archives. ARCH is overridable so one host can build both of them.
ARCH ?= $(shell go env GOARCH)
DIST_DIR := dist
DIST_NAME := $(BINARY)-$(VERSION)-linux-$(ARCH)
DIST_TERMUX_NAME := $(BINARY)-$(VERSION)-termux-src

all: build

deps:
	cd web && npm install

# icons renders every raster size from assets/icon.svg (needs rsvg-convert).
icons:
	./assets/gen-icons.sh

ui: icons
	cd web && npm run build

# build compiles the SPA then the Go binary (which embeds web/dist).
build: ui
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/webssh

# build-webview links the embedded webkit2gtk window into the binary. It needs
# cgo and the webkit headers (Debian/Deepin: libgtk-3-dev libwebkit2gtk-4.0-dev),
# which is why it is not the default build.
build-webview: ui
	go build -tags webview -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/webssh

# syso builds the Windows resource object; the Go linker picks up any *.syso
# next to package main automatically when GOOS=windows.
syso: icons
	$(WINRES) make --arch amd64 --in winres/winres.json --out cmd/webssh/rsrc

build-windows: ui syso
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY).exe ./cmd/webssh

# launcher-syso/build-windows-launcher build webssh-launcher.exe: the program
# the Desktop/Start Menu shortcut webssh-setup creates points at. It starts
# webssh inside WSL and opens the URL it prints in the Windows browser.
# -H=windowsgui suppresses its console window - it has nothing to print to
# the user, and a message box (see main_windows.go) covers the error case.
launcher-syso: icons
	$(WINRES) make --arch amd64 --in winres/winres-launcher.json --out cmd/webssh-launcher/rsrc

build-windows-launcher: launcher-syso
	GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui $(LDFLAGS)" -o webssh-launcher.exe ./cmd/webssh-launcher

# uninstall-syso/build-windows-uninstall build webssh-uninstall.exe: what
# webssh-setup registers as the Settings > Apps Uninstall button target. It
# removes the shortcuts, that registration, and (with -purge) webssh itself
# from inside WSL via get.sh --uninstall.
uninstall-syso: icons
	$(WINRES) make --arch amd64 --in winres/winres-uninstall.json --out cmd/webssh-uninstall/rsrc

build-windows-uninstall: uninstall-syso
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o webssh-uninstall.exe ./cmd/webssh-uninstall

# setup-syso/build-windows-setup build webssh-setup.exe: a double-click
# installer that gets WSL and a distro ready, runs get.sh inside them, then
# embeds and installs webssh-launcher.exe/webssh-uninstall.exe, points a
# shortcut at the launcher, and registers webssh under Settings > Apps - for
# people who don't want to open PowerShell or fight its ExecutionPolicy. Pure
# Go (no cgo), same as build-windows. build-windows-launcher/-uninstall must
# run first: the embed directives in cmd/webssh-setup need both binaries on disk.
setup-syso: icons
	$(WINRES) make --arch amd64 --in winres/winres-setup.json --out cmd/webssh-setup/rsrc

build-windows-setup: build-windows-launcher build-windows-uninstall setup-syso
	mkdir -p cmd/webssh-setup/assets
	cp webssh-launcher.exe cmd/webssh-setup/assets/webssh-launcher.exe
	cp webssh-uninstall.exe cmd/webssh-setup/assets/webssh-uninstall.exe
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o webssh-setup.exe ./cmd/webssh-setup

# dist packs a release archive: the binary plus the icons and .desktop template
# get.sh needs to register the app, and get.sh itself so the archive installs
# offline. CGO is off, which makes the binary static (it runs on any glibc or
# musl system) and cross-compiles without a C toolchain — the price is that a
# release binary has no webview mode, since that one needs cgo and webkit.
dist: ui
	rm -rf $(DIST_DIR)/$(DIST_NAME)
	mkdir -p $(DIST_DIR)/$(DIST_NAME)/icons
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) \
		go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(DIST_DIR)/$(DIST_NAME)/$(BINARY) ./cmd/webssh
	for n in $(ICON_SIZES); do \
		install -m644 assets/png/icon-$$n.png $(DIST_DIR)/$(DIST_NAME)/icons/$(BINARY)-$$n.png; \
	done
	install -m644 assets/icon.svg $(DIST_DIR)/$(DIST_NAME)/icons/$(BINARY).svg
	install -m644 packaging/webssh.desktop.in $(DIST_DIR)/$(DIST_NAME)/$(BINARY).desktop.in
	install -m755 get.sh $(DIST_DIR)/$(DIST_NAME)/get.sh
	tar -czf $(DIST_DIR)/$(DIST_NAME).tar.gz -C $(DIST_DIR) $(DIST_NAME)
	rm -rf $(DIST_DIR)/$(DIST_NAME)
	cd $(DIST_DIR) && sha256sum *.tar.gz > SHA256SUMS
	@echo "==> $(DIST_DIR)/$(DIST_NAME).tar.gz"

# dist-termux-src packs a source tarball for Termux: cmd/, internal/, web/
# (with web/dist already built, so Termux never needs Node — only Go), go.mod
# and go.sum. No release *binary* works under Termux's bionic dynamic linker
# (see internal/config's RunningUnderTermux doc): a plain CGO_ENABLED=0
# GOOS=linux build gets rejected outright (non-PIE), and even a PIE rebuild
# fails bionic's TLS-segment alignment check on modern Android. The only
# binary that actually runs is one built with GOOS=android CGO_ENABLED=1 —
# which is exactly what `pkg install golang` inside Termux itself defaults
# to — so packaging/termux/install.sh has Termux build from this source
# tarball instead of downloading a prebuilt binary.
dist-termux-src: ui
	rm -rf $(DIST_DIR)/$(DIST_TERMUX_NAME)
	mkdir -p $(DIST_DIR)/$(DIST_TERMUX_NAME)
	cp -r cmd internal web go.mod go.sum $(DIST_DIR)/$(DIST_TERMUX_NAME)/
	rm -rf $(DIST_DIR)/$(DIST_TERMUX_NAME)/web/node_modules
	mkdir -p $(DIST_DIR)/$(DIST_TERMUX_NAME)/packaging/termux
	install -m755 packaging/termux/install.sh $(DIST_DIR)/$(DIST_TERMUX_NAME)/packaging/termux/install.sh
	install -m755 packaging/termux/webssh.sh $(DIST_DIR)/$(DIST_TERMUX_NAME)/packaging/termux/webssh.sh
	tar -czf $(DIST_DIR)/$(DIST_TERMUX_NAME).tar.gz -C $(DIST_DIR) $(DIST_TERMUX_NAME)
	rm -rf $(DIST_DIR)/$(DIST_TERMUX_NAME)
	cd $(DIST_DIR) && sha256sum *.tar.gz > SHA256SUMS
	@echo "==> $(DIST_DIR)/$(DIST_TERMUX_NAME).tar.gz"

# dist-windows builds webssh-setup.exe (which embeds webssh-launcher.exe and
# webssh-uninstall.exe - nothing else needs publishing separately) and copies
# it into the release directory under a versioned name. get.sh never reads
# it - Windows installs go through get.ps1/get.bat/webssh-setup.exe, never
# get.sh directly - it just rides along in the same release as the Linux
# archives.
dist-windows: build-windows-setup
	mkdir -p $(DIST_DIR)
	cp webssh-setup.exe $(DIST_DIR)/$(BINARY)-setup-$(VERSION).exe
	cd $(DIST_DIR) && sha256sum *.tar.gz *.exe > SHA256SUMS
	@echo "==> $(DIST_DIR)/$(BINARY)-setup-$(VERSION).exe"

# dist-all builds every platform a release publishes. The SHA256SUMS the last
# run writes covers all of them, which is what get.sh verifies against (for
# the Linux archives; webssh-setup.exe carries its own verification story by
# virtue of being downloaded straight from a GitHub release URL).
dist-all:
	rm -rf $(DIST_DIR)
	$(MAKE) dist ARCH=amd64
	$(MAKE) dist ARCH=arm64
	$(MAKE) dist-termux-src
	$(MAKE) dist-windows
	@echo
	@cat $(DIST_DIR)/SHA256SUMS

# run builds everything and starts the server (opens the browser).
run: build
	./$(BINARY)

# dev runs the Vite dev server (proxying /api to a separately-running backend).
dev:
	cd web && npm run dev

# install-desktop registers webssh in the application menu of the current user.
# Exec must be absolute, so it is substituted into the template at install time.
install-desktop: icons
	for n in $(ICON_SIZES); do \
		install -Dm644 assets/png/icon-$$n.png $(ICON_DIR)/$${n}x$${n}/apps/$(BINARY).png; \
	done
	install -Dm644 assets/icon.svg $(ICON_DIR)/scalable/apps/$(BINARY).svg
	mkdir -p $(DESKTOP_DIR)
	sed 's|@EXEC@|$(CURDIR)/$(BINARY)|' packaging/webssh.desktop.in > $(DESKTOP_DIR)/$(BINARY).desktop
	-gtk-update-icon-cache -f -t $(ICON_DIR)
	-update-desktop-database $(DESKTOP_DIR)

uninstall-desktop:
	rm -f $(DESKTOP_DIR)/$(BINARY).desktop
	for n in $(ICON_SIZES); do \
		rm -f $(ICON_DIR)/$${n}x$${n}/apps/$(BINARY).png; \
	done
	rm -f $(ICON_DIR)/scalable/apps/$(BINARY).svg
	-gtk-update-icon-cache -f -t $(ICON_DIR)
	-update-desktop-database $(DESKTOP_DIR)

clean:
	rm -f $(BINARY) $(BINARY).exe webssh-setup.exe webssh-launcher.exe webssh-uninstall.exe \
		cmd/webssh/*.syso cmd/webssh-setup/*.syso cmd/webssh-launcher/*.syso cmd/webssh-uninstall/*.syso \
		cmd/webssh-setup/assets/webssh-launcher.exe cmd/webssh-setup/assets/webssh-uninstall.exe
	rm -rf web/dist web/node_modules assets/png $(DIST_DIR)
