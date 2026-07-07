// Package askpass lets webssh feed a host's saved password to ssh non-interactively
// via the SSH_ASKPASS mechanism, so the browser terminal and the native terminal
// both log in without the user retyping the password.
//
// It works by pointing ssh at a tiny helper script (SSH_ASKPASS) and forcing its
// use (SSH_ASKPASS_REQUIRE=force, OpenSSH >= 8.4). The script carries no secret:
// the password is passed to the ssh child in the WEBSSH_ASKPASS_PW environment
// variable, which the script echoes when ssh asks for a password. For a brand-new
// host key ssh first asks "continue connecting (yes/no)?" — the script answers
// "yes" (trust-on-first-use); a *changed* host key still fails safely, as ssh
// refuses it without prompting.
package askpass

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// helperName is the on-disk name of the askpass script inside the data dir.
const helperName = "webssh-askpass.sh"

// pwEnv is the environment variable the helper reads the password from.
const pwEnv = "WEBSSH_ASKPASS_PW"

// script is the helper body. $1 is the prompt text ssh passes to askpass.
const script = `#!/bin/sh
# Managed by webssh — do not edit. Answers ssh prompts for saved-password hosts.
case "$1" in
  *"continue connecting"*|*"(yes/no"*) printf 'yes\n' ;;
  *) printf '%s\n' "$` + pwEnv + `" ;;
esac
`

// Ensure writes the helper script into dataDir (idempotent) and returns its path.
func Ensure(dataDir string) (string, error) {
	path := filepath.Join(dataDir, helperName)
	// Rewrite only when missing or stale, so we don't churn the file each launch.
	if cur, err := os.ReadFile(path); err == nil && string(cur) == script {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// Env returns the environment additions that make ssh use the helper to answer
// its prompts with the given password. Append these to the ssh child's env. This
// is used where webssh execs ssh directly (the browser terminal) and controls
// the environment ssh actually starts with.
func Env(helper, password string) []string {
	return []string{
		"SSH_ASKPASS=" + helper,
		"SSH_ASKPASS_REQUIRE=force",
		pwEnv + "=" + password,
	}
}

// Wrapper writes a per-host launch script that self-sets the SSH_ASKPASS
// environment and then execs ssh, and returns its path. It exists for the native
// terminal path: many terminal emulators run a single background server whose
// environment we cannot influence, so exporting SSH_ASKPASS onto the emulator
// process does not reach the ssh child. A wrapper that exports the variables
// itself works regardless of how the emulator spawns the command.
//
// The password is embedded in the script (mode 0700 in the 0700 data dir),
// consistent with the plaintext host password already stored in the database.
func Wrapper(dataDir string, hostID int64, helper, password string) (string, error) {
	path := filepath.Join(dataDir, fmt.Sprintf("webssh-term-%d.sh", hostID))
	body := "#!/bin/sh\n" +
		"# Managed by webssh — feeds this host's saved password to ssh.\n" +
		"export SSH_ASKPASS=" + shQuote(helper) + "\n" +
		"export SSH_ASKPASS_REQUIRE=force\n" +
		"export " + pwEnv + "=" + shQuote(password) + "\n" +
		"exec ssh \"$@\"\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// shQuote wraps s in single quotes for /bin/sh, escaping embedded single quotes,
// so an arbitrary password can't break out of the string or be interpreted.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
