//go:build !windows

// webssh-uninstall only makes sense on Windows; this stub exists purely so
// `go build ./...`/`go vet ./...` keep working on other platforms.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "webssh-uninstall is a Windows-only program; build/run it with GOOS=windows.")
	os.Exit(1)
}
