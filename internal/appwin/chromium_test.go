package appwin

import (
	"os"
	"path/filepath"
	"testing"

	"webssh/internal/config"
)

// withCandidates swaps the detection lists for the duration of a test, so the
// result does not depend on what happens to be installed on the machine.
func withCandidates(t *testing.T, names, paths []string) {
	t.Helper()
	oldNames, oldPaths := chromiumNames, chromiumPaths
	chromiumNames, chromiumPaths = names, paths
	t.Cleanup(func() { chromiumNames, chromiumPaths = oldNames, oldPaths })
}

// fakeExe writes an executable file and returns its path.
func fakeExe(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindChromiumOnPath(t *testing.T) {
	dir := t.TempDir()
	fakeExe(t, dir, "my-browser")
	t.Setenv("PATH", dir)
	withCandidates(t, []string{"my-browser"}, nil)

	got, ok := findChromium("")
	if !ok {
		t.Fatal("expected to find my-browser on PATH")
	}
	if filepath.Base(got) != "my-browser" {
		t.Fatalf("got %q, want a path ending in my-browser", got)
	}
}

// Chrome and Edge install into /opt without ever touching PATH, so the absolute
// fallback is the one that matters most in practice.
func TestFindChromiumAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	exe := fakeExe(t, dir, "edge")
	t.Setenv("PATH", t.TempDir()) // nothing on PATH
	withCandidates(t, []string{"definitely-not-installed"}, []string{exe})

	got, ok := findChromium("")
	if !ok || got != exe {
		t.Fatalf("findChromium() = %q, %v; want %q, true", got, ok, exe)
	}
}

func TestFindChromiumNothingInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	withCandidates(t, []string{"definitely-not-installed"}, []string{"/nonexistent/browser"})

	if got, ok := findChromium(""); ok {
		t.Fatalf("findChromium() = %q, true; want not found", got)
	}
}

// A directory is not a browser, even at a candidate path.
func TestFindChromiumIgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	withCandidates(t, []string{"definitely-not-installed"}, []string{dir})

	if _, ok := findChromium(""); ok {
		t.Fatal("a directory must not be accepted as a browser")
	}
}

func TestNewFallsBackToBrowserWithoutChromium(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	withCandidates(t, []string{"definitely-not-installed"}, nil)

	if _, ok := New(ModeApp, config.Paths{}, "", "").(*browserUI); !ok {
		t.Fatal("app mode without a chromium browser must fall back to browserUI")
	}
}

// Whatever the mode, an explicit browser_cmd is the user stating exactly what
// they want and must win.
func TestNewBrowserCmdWins(t *testing.T) {
	dir := t.TempDir()
	exe := fakeExe(t, dir, "edge")
	withCandidates(t, nil, []string{exe})

	for _, mode := range Modes {
		ui, ok := New(mode, config.Paths{}, "firefox {{url}}", "").(*browserUI)
		if !ok {
			t.Fatalf("mode %q: browser_cmd must yield browserUI", mode)
		}
		if ui.cmd != "firefox {{url}}" {
			t.Fatalf("mode %q: cmd = %q", mode, ui.cmd)
		}
	}
}

func TestNewAppModeUsesChromium(t *testing.T) {
	dir := t.TempDir()
	exe := fakeExe(t, dir, "edge")
	withCandidates(t, nil, []string{exe})

	ui, ok := New(ModeApp, config.Paths{BrowserDir: "/tmp/profile"}, "", "").(*chromiumUI)
	if !ok {
		t.Fatal("app mode with a chromium browser must yield chromiumUI")
	}
	if ui.exe != exe || ui.profileDir != "/tmp/profile" {
		t.Fatalf("chromiumUI{exe:%q, profileDir:%q}", ui.exe, ui.profileDir)
	}
}

// Without the webview build tag, requesting a webview must degrade rather than
// leave the user with no interface at all.
func TestWebviewModeDegradesWhenNotBuiltIn(t *testing.T) {
	dir := t.TempDir()
	exe := fakeExe(t, dir, "edge")
	withCandidates(t, nil, []string{exe})

	switch New(ModeWebview, config.Paths{}, "", "").(type) {
	case *chromiumUI, *browserUI: // either is a working fallback
	default:
		t.Fatal("webview mode must fall back to a usable UI")
	}
}

func TestParseModeAndValid(t *testing.T) {
	for in, want := range map[string]Mode{
		"browser":  ModeBrowser,
		"app":      ModeApp,
		"webview":  ModeWebview,
		"":         ModeApp, // unset settings mean the default
		"nonsense": ModeApp,
	} {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", in, got, want)
		}
	}
	for _, ok := range []string{"browser", "app", "webview"} {
		if !Valid(ok) {
			t.Errorf("Valid(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "nonsense", "App"} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true", bad)
		}
	}
}

// A pinned browser must win over detection, and a stale pin must not leave the
// user without a window.
func TestFindChromiumPinned(t *testing.T) {
	dir := t.TempDir()
	detected := fakeExe(t, dir, "detected")
	pinned := fakeExe(t, dir, "pinned")
	t.Setenv("PATH", t.TempDir())
	withCandidates(t, nil, []string{detected})

	if got, ok := findChromium(pinned); !ok || got != pinned {
		t.Fatalf("findChromium(pinned) = %q, %v; want %q", got, ok, pinned)
	}
	if got, ok := findChromium("/nonexistent/browser"); !ok || got != detected {
		t.Fatalf("a stale pin must fall back to detection, got %q, %v", got, ok)
	}
}

func TestNewAppModeHonoursPinnedBrowser(t *testing.T) {
	dir := t.TempDir()
	detected := fakeExe(t, dir, "detected")
	pinned := fakeExe(t, dir, "pinned")
	withCandidates(t, nil, []string{detected})

	ui, ok := New(ModeApp, config.Paths{}, "", pinned).(*chromiumUI)
	if !ok || ui.exe != pinned {
		t.Fatalf("app mode must use the pinned browser, got %#v", ui)
	}
}

func TestBrowserFallbackHonoursAppBrowser(t *testing.T) {
	ui := &chromiumUI{exe: "/usr/bin/microsoft-edge"}
	if got := fallbackBrowserCommand(ui, ""); got != "/usr/bin/microsoft-edge" {
		t.Fatalf("fallback browser = %q, want pinned Edge", got)
	}
	if got := fallbackBrowserCommand(ui, "firefox {{url}}"); got != "firefox {{url}}" {
		t.Fatalf("fallback browser = %q, want explicit browser_cmd", got)
	}
}

// FindChromiumAll backs the installer's menu, so duplicates (a PATH symlink to
// the same binary under /opt) would show up as bogus extra choices.
func TestFindChromiumAllDeduplicates(t *testing.T) {
	dir := t.TempDir()
	real := fakeExe(t, dir, "edge-real")
	link := filepath.Join(dir, "edge-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	withCandidates(t, nil, []string{real, link})

	if got := FindChromiumAll(); len(got) != 1 || got[0] != real {
		t.Fatalf("FindChromiumAll() = %v; want just [%s]", got, real)
	}
}

func TestFindChromiumAllEmpty(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	withCandidates(t, []string{"definitely-not-installed"}, []string{"/nonexistent"})

	if got := FindChromiumAll(); len(got) != 0 {
		t.Fatalf("FindChromiumAll() = %v; want empty", got)
	}
}
