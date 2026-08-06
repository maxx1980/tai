package version

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		in    string
		nums  []int
		pre   string
		ahead int
		dirty bool
		ok    bool
	}{
		{in: "v0.2.0", nums: []int{0, 2, 0}, ok: true},
		{in: "0.2.0", nums: []int{0, 2, 0}, ok: true},
		{in: "v0.2.0-3-g40c40f1", nums: []int{0, 2, 0}, ahead: 3, ok: true},
		{in: "v0.2.0-3-g40c40f1-dirty", nums: []int{0, 2, 0}, ahead: 3, dirty: true, ok: true},
		{in: "v1.0.0-rc1", nums: []int{1, 0, 0}, pre: "rc1", ok: true},
		{in: "v10.4", nums: []int{10, 4}, ok: true},
		// No numbers to compare: an untagged build, reportable but not orderable.
		{in: "dev"},
		{in: "40c40f1"},
		{in: ""},
	}
	for _, tc := range tests {
		got := Parse(tc.in)
		if got.OK != tc.ok || got.Ahead != tc.ahead || got.Pre != tc.pre || got.Dirty != tc.dirty {
			t.Errorf("Parse(%q) = %+v, want ok=%v ahead=%d pre=%q dirty=%v",
				tc.in, got, tc.ok, tc.ahead, tc.pre, tc.dirty)
			continue
		}
		if len(got.Nums) != len(tc.nums) {
			t.Errorf("Parse(%q).Nums = %v, want %v", tc.in, got.Nums, tc.nums)
			continue
		}
		for i := range tc.nums {
			if got.Nums[i] != tc.nums[i] {
				t.Errorf("Parse(%q).Nums = %v, want %v", tc.in, got.Nums, tc.nums)
				break
			}
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"v0.3.0", "v0.2.0", true},
		{"v0.2.1", "v0.2.0", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.2.0", "v0.2.0", false},
		{"v0.2.0", "v0.3.0", false},
		// The running build is three commits past the tag GitHub offers, so it
		// already contains it — this is the state of a maintainer's checkout.
		{"v0.2.0", "v0.2.0-3-g40c40f1", false},
		{"v0.3.0", "v0.2.0-3-g40c40f1", true},
		// "1.2" and "1.2.0" name the same release.
		{"v1.2", "v1.2.0", false},
		{"v1.2.0", "v1.2", false},
		// A pre-release is older than the release it leads to, and newer than
		// the version before it.
		{"v1.0.0", "v1.0.0-rc1", true},
		{"v1.0.0-rc1", "v1.0.0", false},
		{"v1.0.0-rc2", "v1.0.0-rc1", true},
		// Nothing to compare against: never nag an unversioned build.
		{"v0.3.0", "dev", false},
		{"v0.3.0", "40c40f1", false},
		{"main", "v0.2.0", false},
	}
	for _, tc := range tests {
		if got := IsNewer(tc.latest, tc.current); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}
