package askpass

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/creack/pty"
)

func TestEnsureAndWrapper(t *testing.T) {
	dir := t.TempDir()

	helper, err := Ensure(dir)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := filepath.Dir(helper); got != dir {
		t.Fatalf("helper not in dataDir: %s", helper)
	}
	if fi, err := os.Stat(helper); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("helper stat: mode=%v err=%v", fi.Mode().Perm(), err)
	}

	// A password containing shell metacharacters must be safely single-quoted.
	pw := `p@ss'w"rd $X`
	wp, err := Wrapper(dir, 7, helper, pw)
	if err != nil {
		t.Fatalf("Wrapper: %v", err)
	}
	b, err := os.ReadFile(wp)
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	body := string(b)
	if !strings.Contains(body, "SSH_ASKPASS_REQUIRE=force") {
		t.Errorf("wrapper missing force: %s", body)
	}
	if !strings.Contains(body, `export WEBSSH_ASKPASS_PW='p@ss'\''w"rd $X'`) {
		t.Errorf("password not safely quoted:\n%s", body)
	}
	if !strings.HasSuffix(body, "exec ssh \"$@\"\n") {
		t.Errorf("wrapper must exec ssh with args:\n%s", body)
	}
}

const hostKeyPrompt = "The authenticity of host '10.0.0.1 (10.0.0.1)' can't be established.\n" +
	"ED25519 key fingerprint is SHA256:abc.\nAre you sure you want to continue connecting (yes/no/[fingerprint])? "

// Without a terminal the helper refuses an unknown host key instead of
// answering "yes" on the user's behalf; passwords are still answered.
func TestHelperNeverAcceptsHostKeyWithoutTerminal(t *testing.T) {
	helper, err := Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := func(prompt string) string {
		cmd := exec.Command("setsid", helper, prompt) // setsid: no controlling terminal
		cmd.Env = append(os.Environ(), pwEnv+"=s3cret")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("helper: %v", err)
		}
		return string(out)
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not installed")
	}
	if got := run(hostKeyPrompt); got != "no\n" {
		t.Errorf("host-key prompt without a terminal answered %q, want \"no\"", got)
	}
	if got := run("maxx@10.0.0.1's password: "); got != "s3cret\n" {
		t.Errorf("password prompt answered %q", got)
	}
}

// On a terminal the helper shows ssh's question and passes on the answer.
func TestHelperAsksOnTerminal(t *testing.T) {
	helper, err := Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{"yes", "no"} {
		cmd := exec.Command(helper, hostKeyPrompt)
		out := &strings.Builder{}
		cmd.Stdout = out
		tty, err := pty.StartWithAttrs(cmd, nil, &syscall.SysProcAttr{Setsid: true, Setctty: true})
		if err != nil {
			t.Skipf("no pty: %v", err)
		}
		// Wait for the question to appear on the terminal, then answer it.
		buf := make([]byte, 4096)
		seen := ""
		for !strings.Contains(seen, "continue connecting") {
			n, err := tty.Read(buf)
			if err != nil {
				t.Fatalf("read tty: %v (saw %q)", err, seen)
			}
			seen += string(buf[:n])
		}
		if !strings.Contains(seen, "SHA256:abc") {
			t.Errorf("fingerprint not shown on the terminal: %q", seen)
		}
		tty.Write([]byte(answer + "\n"))
		_ = cmd.Wait()
		tty.Close()
		if out.String() != answer+"\n" {
			t.Errorf("typed %q, helper answered ssh %q", answer, out.String())
		}
	}
}
