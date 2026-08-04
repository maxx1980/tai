//go:build !webview

package appwin

import "errors"

// newWebview reports that this binary was built without webview support. The
// embedded window needs cgo and libwebkit2gtk headers, which would drag a C
// toolchain into every build (and break the pure-Go windows cross-compile), so
// it lives behind `make build-webview`.
func newWebview() (UI, error) {
	return nil, errors.New("built without the webview tag (use `make build-webview`)")
}
