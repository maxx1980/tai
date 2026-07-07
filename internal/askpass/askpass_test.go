package askpass

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
