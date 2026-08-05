package appwin

import (
	"log"
	"os/exec"
	"runtime"
	"strings"

	"webssh/internal/launcher"
)

// browserUI opens the URL the way webssh always has: through a user-supplied
// command when cmd is set, otherwise through the platform's default handler.
type browserUI struct {
	cmd string // browser_cmd template, may be empty
}

func (b *browserUI) Blocking() bool { return false }
func (b *browserUI) Close()         {}

func (b *browserUI) Open(rawURL string) error { return OpenURL(b.cmd, rawURL) }

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
		switch runtime.GOOS {
		case "darwin":
			name = "open"
		case "windows":
			name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
		default:
			name = "xdg-open"
		}
		args = append(args, rawURL)
	}

	return exec.Command(name, args...).Start()
}
