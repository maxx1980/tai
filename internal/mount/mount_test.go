package mount

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFuseAllowsOther(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "enabled", content: "user_allow_other\n", want: true},
		{name: "enabled with whitespace", content: "  user_allow_other  # WSL access\n", want: true},
		{name: "commented out", content: "# user_allow_other\n", want: false},
		{name: "different option", content: "mount_max = 1000\n", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fuse.conf")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := fuseAllowsOther(path); got != tt.want {
				t.Fatalf("fuseAllowsOther() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFuseAllowsOtherMissingFile(t *testing.T) {
	if fuseAllowsOther(filepath.Join(t.TempDir(), "missing.conf")) {
		t.Fatal("missing fuse.conf enabled allow_other")
	}
}
