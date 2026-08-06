//go:build !windows

// wininstall only makes sense on Windows; this empty file exists purely so
// `go build ./...`/`go vet ./...` keep working on other platforms.
package wininstall
