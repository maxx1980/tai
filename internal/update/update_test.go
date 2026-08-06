package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// touchExec creates an executable file, the way a real tool would look on disk.
func touchExec(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLookIn(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	want := touchExec(t, first, "tool")
	touchExec(t, second, "tool") // shadowed: earlier entries win, as PATH does
	// A non-executable file and a directory must not count as the tool.
	if err := os.MkdirAll(filepath.Join(root, "third", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "third", "plain"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := strings.Join([]string{filepath.Join(root, "third"), first, second}, string(os.PathListSeparator))

	got, ok := lookIn(path, "tool")
	if !ok || got != want {
		t.Errorf("lookIn(tool) = %q, %v; want %q, true", got, ok, want)
	}
	if _, ok := lookIn(path, "plain"); ok {
		t.Error("lookIn matched a non-executable file")
	}
	if _, ok := lookIn(path, "absent"); ok {
		t.Error("lookIn matched a tool that is not there")
	}
}

// resolve must hand back an absolute path, because exec.Command resolves bare
// names against the process's own PATH and ignores the Env we build.
func TestResolve(t *testing.T) {
	if got := resolve("/usr/bin/git"); got != "/usr/bin/git" {
		t.Errorf("resolve(absolute) = %q, want it unchanged", got)
	}
	// An unknown name passes through so exec produces the error, not us.
	if got := resolve("definitely-not-a-real-tool"); got != "definitely-not-a-real-tool" {
		t.Errorf("resolve(unknown) = %q, want it unchanged", got)
	}
}

// fallbackDirs must order nvm's node directories by version, not by string:
// sorted as text, v9 comes after v22 and the wrong node would be picked.
func TestFallbackDirsPicksNewestNode(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"v9.1.0", "v22.23.1", "v18.0.0"} {
		if err := os.MkdirAll(filepath.Join(home, ".nvm", "versions", "node", v, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(home, ".nvm", "versions", "node", "v22.23.1", "bin")
	dirs := fallbackDirs(home)
	found := false
	for _, d := range dirs {
		if strings.Contains(d, ".nvm") {
			if d != want {
				t.Errorf("nvm dir = %q, want %q", d, want)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("fallbackDirs did not offer an nvm directory: %v", dirs)
	}
}

func TestNodeVersionOf(t *testing.T) {
	if got := nodeVersionOf("/home/u/.nvm/versions/node/v22.23.1/bin"); got != "v22.23.1" {
		t.Errorf("nodeVersionOf = %q, want v22.23.1", got)
	}
}

// commandEnv must carry exactly one PATH. With two, glibc's getenv returns the
// first, so an appended override would be ignored and make would look for go
// and npm on the desktop session's bare PATH again.
func TestCommandEnvHasSinglePATH(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	var seen []string
	for _, kv := range commandEnv() {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			seen = append(seen, v)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("commandEnv has %d PATH entries, want exactly 1: %v", len(seen), seen)
	}
	if seen[0] != buildPATH() {
		t.Errorf("commandEnv PATH = %q, want buildPATH", seen[0])
	}
}

// buildPATH must never lose what the process was started with, whatever the
// login shell says.
func TestBuildPATHKeepsInheritedEntries(t *testing.T) {
	for _, dir := range []string{"/usr/bin", "/bin"} {
		if !strings.Contains(buildPATH(), dir) {
			t.Errorf("buildPATH dropped %s: %s", dir, buildPATH())
		}
	}
}
