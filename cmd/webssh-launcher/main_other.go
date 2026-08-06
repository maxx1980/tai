//go:build !windows

// webssh-launcher only makes sense on Windows (it's what the Desktop/Start
// Menu shortcut webssh-setup creates points at); this stub exists purely so
// `go build ./...`/`go vet ./...` keep working on other platforms.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "webssh-launcher is a Windows-only program; build/run it with GOOS=windows.")
	os.Exit(1)
}
