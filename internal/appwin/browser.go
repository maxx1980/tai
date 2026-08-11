package appwin

import (
	"log"
	"os/exec"
	"runtime"
	"strings"

	"webssh/internal/config"
	"webssh/internal/launcher"
)

// browserUI opens the URL the way webssh always has: through a user-supplied
// command when cmd is set, otherwise through the platform's default handler.
type browserUI struct {
	cmd string // browser_cmd template, may be empty
}

func (b *browserUI) Blocking() bool { return false }
func (b *browserUI) Close()         {}
func (b *browserUI) Mode() Mode     { return ModeBrowser }

func (b *browserUI) Open(rawURL string) error { return OpenURL(b.cmd, rawURL) }

// OpenBrowserFallback opens rawURL as a regular tab while preserving the
// browser selected for an app window. Falling back through xdg-open here would
// silently switch, for example, a pinned Edge app to the system-default Chrome.
func OpenBrowserFallback(ui UI, browserCmd, rawURL string) error {
	return OpenURL(fallbackBrowserCommand(ui, browserCmd), rawURL)
}

func fallbackBrowserCommand(ui UI, browserCmd string) string {
	if browserCmd != "" {
		return browserCmd
	}
	if chromium, ok := ui.(*chromiumUI); ok {
		return chromium.exe
	}
	return ""
}

// OpenURL hands rawURL to the user's browser command, falling back to the
// platform's default handler when none is configured (or it is unparseable).
// Exported so the API can open a host's web UI — Proxmox, a plain HTTP service —
// in a real browser window rather than inside the chromeless app window.
func OpenURL(browserCmd, rawURL string) error {
	var name string
	var args []string

	if strings.TrimSpace(browserCmd) != "" {
		hasPlaceholder := strings.Contains(browserCmd, "{{url}}")
		tmpl := strings.ReplaceAll(browserCmd, "{{url}}", rawURL)
		if fields, err := launcher.SplitFields(tmpl); err != nil || len(fields) == 0 {
			log.Printf("invalid browser command %q (%v); using system default", browserCmd, err)
		} else {
			name, args = fields[0], fields[1:]
			if !hasPlaceholder {
				args = append(args, rawURL)
			}
		}
	}

	if name == "" {
		switch {
		case runtime.GOOS == "darwin":
			name = "open"
		case runtime.GOOS == "windows":
			name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
		case config.RunningUnderTermux():
			// No xdg-open, no browser window to speak of — termux-open-url
			// (from the optional termux-api package) hands the URL to
			// Android's default handler. Without it, there is nothing to
			// exec: the token URL is already printed to the terminal, which
			// is tap-to-open there, so leave name empty and skip Start().
			if p, err := exec.LookPath("termux-open-url"); err == nil {
				name = p
			}
		default:
			name = "xdg-open"
		}
		if name == "" {
			return nil
		}
		args = append(args, rawURL)
	}

	return exec.Command(name, args...).Start()
}
