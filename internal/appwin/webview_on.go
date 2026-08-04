//go:build webview

package appwin

import (
	webview "github.com/webview/webview_go"
)

// webviewUI is a native webkit2gtk window hosting the SPA, so no browser is
// involved at all.
type webviewUI struct {
	w webview.WebView
}

func newWebview() (UI, error) {
	return &webviewUI{}, nil
}

func (v *webviewUI) Blocking() bool { return true }

// Open creates the window and runs the GTK loop. It must be called from the
// main goroutine with the OS thread locked; Run only returns once the user
// closes the window (or Close is called).
func (v *webviewUI) Open(rawURL string) error {
	v.w = webview.New(false)
	defer v.w.Destroy()
	v.w.SetTitle("webssh")
	v.w.SetSize(1280, 840, webview.HintNone)
	v.w.Navigate(rawURL)
	v.w.Run()
	return nil
}

// Close ends the GTK loop from another goroutine. Dispatch marshals the call
// onto the UI thread, which is the only place it is legal to touch the window.
func (v *webviewUI) Close() {
	if v.w == nil {
		return
	}
	v.w.Dispatch(func() { v.w.Terminate() })
}
