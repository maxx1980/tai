package server

import (
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSudo puts a `sudo` on PATH that accepts the password "secret" on stdin
// (-S), honours -n only once a drop-in exists, and runs install/rm itself
// (dropping -o/-g, which need root) so the test never touches /etc.
func fakeSudo(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
nopw=0; pw=0
while [ $# -gt 0 ]; do case "$1" in
  -S) pw=1; shift;; -k) shift;; -p) shift 2;; -n) nopw=1; shift;; *) break;; esac; done
if [ $pw = 1 ]; then read line; [ "$line" = secret ] || { echo "Sorry, try again."; echo "sudo: 1 incorrect password attempt"; exit 1; }; fi
if [ $nopw = 1 ] && ! ls "$SUDOERS_DIR"/* >/dev/null 2>&1; then echo "sudo: a password is required"; exit 1; fi
case "$1" in
  true) exit 0;;
  install) shift; mode=""; while [ $# -gt 2 ]; do case "$1" in -m) mode=$2; shift 2;; -o|-g) shift 2;; *) shift;; esac; done
    cp "$1" "$2" && chmod "$mode" "$2";;
  rm) shift; rm "$@";;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	t.Setenv("SUDOERS_DIR", dir)
	old := sudoersDir
	sudoersDir = dir
	t.Cleanup(func() { sudoersDir = old })
}

func TestSudoEnableWrongPasswordThenEnableAndRemove(t *testing.T) {
	u, err := user.Current()
	if err != nil || u.Uid == "0" || !validUser.MatchString(u.Username) {
		t.Skip("needs a regular, sudoers-safe user")
	}
	fakeSudo(t)
	s, _ := newSecurityTestServer(t)
	dest := filepath.Join(sudoersDir, u.Username)

	call := func(method string, h func(w *httptest.ResponseRecorder), body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec)
		return rec
	}
	enable := func(pw string) *httptest.ResponseRecorder {
		return call("POST", func(w *httptest.ResponseRecorder) {
			s.handleSudoEnable(w, httptest.NewRequest("POST", "/api/system/sudo", strings.NewReader(`{"password":"`+pw+`"}`)))
		}, "")
	}

	if rec := enable("wrong"); rec.Code != 403 || !strings.Contains(rec.Body.String(), "wrong sudo password") {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("drop-in written despite the wrong password")
	}

	if rec := enable("secret"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"nopasswd":true`) {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != u.Username+" ALL=(ALL) NOPASSWD: ALL\n" {
		t.Fatalf("drop-in = %q, %v", data, err)
	}
	if st, _ := os.Stat(dest); st.Mode().Perm() != 0o440 {
		t.Errorf("mode = %v, want 0440", st.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(s.paths.DataDir, "sudoers-*")); len(left) != 0 {
		t.Errorf("staged rule left behind: %v", left)
	}

	rec := httptest.NewRecorder()
	s.handleSudoStatus(rec, httptest.NewRequest("GET", "/api/system/sudo", nil))
	if !strings.Contains(rec.Body.String(), `"nopasswd":true`) {
		t.Errorf("status after enable: %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	s.handleSudoDisable(rec, httptest.NewRequest("DELETE", "/api/system/sudo", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"nopasswd":false`) {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("drop-in still present after Remove")
	}
}
