package appwin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The expected IDs were read off real windows on a Wayland (COSMIC) session.
func TestWaylandAppID(t *testing.T) {
	for _, tc := range []struct{ exe, url, want string }{
		{"/usr/bin/chromium", "http://127.0.0.1:8022/?token=abc", "chrome-127.0.0.1__-Default"},
		{"/usr/bin/microsoft-edge", "http://127.0.0.1:8022/?token=abc", "msedge-127.0.0.1__-Default"},
		{"/opt/google/chrome/google-chrome", "http://localhost:9000/", "chrome-localhost__-Default"},
		{"/usr/bin/brave-browser", "http://127.0.0.1:8022/", "brave-127.0.0.1__-Default"},
	} {
		got, ok := waylandAppID(tc.exe, tc.url)
		if !ok || got != tc.want {
			t.Errorf("waylandAppID(%q, %q) = %q, %v; want %q", tc.exe, tc.url, got, ok, tc.want)
		}
	}
}

func TestWriteDesktopAlias(t *testing.T) {
	dir := t.TempDir()
	id := "chrome-127.0.0.1__-Default"
	if err := writeDesktopAlias(dir, id, "/opt/webssh"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".desktop"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Icon=webssh", "NoDisplay=true", "Exec=/opt/webssh", "StartupWMClass=" + id, aliasMarker} {
		if !strings.Contains(string(data), want) {
			t.Errorf("alias lacks %q:\n%s", want, data)
		}
	}
	// A moved binary updates our own alias.
	if err := writeDesktopAlias(dir, id, "/new/webssh"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, id+".desktop"))
	if !strings.Contains(string(data), "Exec=/new/webssh") {
		t.Errorf("alias not updated:\n%s", data)
	}
}

func TestWriteDesktopAliasLeavesForeignEntry(t *testing.T) {
	dir := t.TempDir()
	id := "chrome-127.0.0.1__-Default"
	foreign := []byte("[Desktop Entry]\nName=Someone's web app\n")
	if err := os.WriteFile(filepath.Join(dir, id+".desktop"), foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeDesktopAlias(dir, id, "/opt/webssh"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, id+".desktop"))
	if string(data) != string(foreign) {
		t.Errorf("foreign entry overwritten:\n%s", data)
	}
}
