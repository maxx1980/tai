// Package appwin decides how the webssh interface is presented: a normal
// browser tab, a chromeless app window, or a native webview embedded in the
// binary.
//
// The three modes differ in one way that leaks into the API: a webview is a GTK
// window and GTK insists on owning the main OS thread, so Open blocks until the
// window closes. Browser-backed modes only spawn a process and return. Callers
// must branch on Blocking() rather than assume either shape.
package appwin

import (
	"log"

	"webssh/internal/config"
)

// Mode selects how the interface is opened.
type Mode string

const (
	ModeBrowser Mode = "browser" // system default handler (or browser_cmd)
	ModeApp     Mode = "app"     // chromium --app window with its own profile
	ModeWebview Mode = "webview" // embedded webkit2gtk window
)

// ParseMode maps a stored setting to a Mode, falling back to app for anything
// unrecognised (including the empty string).
func ParseMode(s string) Mode {
	switch Mode(s) {
	case ModeBrowser:
		return ModeBrowser
	case ModeWebview:
		return ModeWebview
	default:
		return ModeApp
	}
}

// Modes lists every mode, for validating flags and rendering help.
var Modes = []Mode{ModeBrowser, ModeApp, ModeWebview}

// Valid reports whether s names a real mode (ParseMode is deliberately lenient;
// this is not).
func Valid(s string) bool {
	for _, m := range Modes {
		if Mode(s) == m {
			return true
		}
	}
	return false
}

// UI presents the webssh interface.
type UI interface {
	// Open shows url. Process-backed UIs return once the process is spawned;
	// a webview returns only when its window is closed.
	Open(url string) error
	// Blocking reports whether Open owns the calling (main) goroutine.
	Blocking() bool
	// Close asks a blocking UI to tear down. No-op for the others.
	Close()
	// Mode reports the implementation actually selected after fallbacks. The
	// launcher uses it to detect an app window that never connected and retry in
	// the system browser.
	Mode() Mode
}

// New picks an implementation for mode, degrading rather than failing:
// an unbuilt or unavailable webview falls back to an app window, and an app
// window with no chromium browser installed falls back to the browser mode
// (which itself ends at xdg-open). An explicit browserCmd always wins, since
// it is the user stating exactly what they want. appBrowser pins which browser
// drives the app window; empty means detect one.
func New(mode Mode, p config.Paths, browserCmd, appBrowser string) UI {
	if browserCmd != "" {
		return &browserUI{cmd: browserCmd}
	}

	switch mode {
	case ModeWebview:
		if ui, err := newWebview(); err == nil {
			return ui
		} else {
			log.Printf("webview unavailable (%v); falling back to an app window", err)
		}
		fallthrough
	case ModeApp:
		if exe, ok := findChromium(appBrowser); ok {
			return &chromiumUI{exe: exe, profileDir: p.BrowserDir}
		}
		log.Printf("no chromium-based browser found; falling back to the default browser")
	}
	return &browserUI{}
}
