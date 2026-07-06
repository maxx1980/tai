.PHONY: all ui build run clean deps

BINARY := webssh

all: build

deps:
	cd web && npm install

ui:
	cd web && npm run build

# build compiles the SPA then the Go binary (which embeds web/dist).
build: ui
	go build -o $(BINARY) ./cmd/webssh

# run builds everything and starts the server (opens the browser).
run: build
	./$(BINARY)

# dev runs the Vite dev server (proxying /api to a separately-running backend).
dev:
	cd web && npm run dev

clean:
	rm -f $(BINARY)
	rm -rf web/dist web/node_modules
