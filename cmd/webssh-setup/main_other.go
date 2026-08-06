//go:build !windows

// webssh-setup only makes sense on Windows (it drives wsl.exe and Windows'
// UAC elevation); this stub exists purely so `go build ./...`/`go vet ./...`
// keep working on other platforms, mirroring internal/mount's linux/darwin split.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "webssh-setup is a Windows-only installer; build/run it with GOOS=windows.")
	os.Exit(1)
}
