.PHONY: all ui build build-webview run clean deps dev icons syso build-windows install-desktop uninstall-desktop

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
	rm -f $(BINARY) $(BINARY).exe cmd/webssh/*.syso
	rm -rf web/dist web/node_modules assets/png
