// Package version records which build of webssh is running and knows how to
// order two version strings, which is all the updater needs to answer "is the
// tag on GitHub newer than what I am?".
//
// The strings it handles are the ones `git describe --tags --always --dirty`
// produces, because that is what the Makefile stamps in: a plain tag
// ("v0.2.0"), a tag plus commits since it ("v0.2.0-3-g40c40f1"), either of
// those with "-dirty" appended, or a bare commit hash when no tag exists yet.
package version

import (
	"strconv"
	"strings"
)

// Version is the running build's version, injected by the Makefile with
// -ldflags -X. A bare `go build` leaves it at "dev"; the updater falls back to
// asking git in the working copy in that case.
var Version = "dev"

// Parsed is a version string broken into the parts that decide ordering.
type Parsed struct {
	Nums  []int  // "0.2.0" -> [0 2 0]
	Pre   string // pre-release suffix ("rc1"), empty for a final release
	Ahead int    // commits past the tag, from git describe's "-3-g<sha>"
	Dirty bool   // the working copy had uncommitted changes at build time
	OK    bool   // false when the string carried no numeric version at all
}

// Parse breaks a version string apart. OK is false for anything with no
// numbers in it ("dev", a bare commit hash) — such a build cannot be compared,
// only reported.
func Parse(s string) Parsed {
	var p Parsed
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if s == "" {
		return p
	}
	if rest, ok := strings.CutSuffix(s, "-dirty"); ok {
		p.Dirty = true
		s = rest
	}
	// git describe appends "-<N>-g<sha>" when HEAD is past the tag. Recognise
	// it by shape so a real pre-release like "1.0.0-rc1" is not mistaken for it.
	if i := strings.LastIndex(s, "-g"); i > 0 {
		if j := strings.LastIndex(s[:i], "-"); j >= 0 {
			if n, err := strconv.Atoi(s[j+1 : i]); err == nil {
				p.Ahead = n
				s = s[:j]
			}
		}
	}
	if base, pre, found := strings.Cut(s, "-"); found {
		s, p.Pre = base, pre
	}
	for _, part := range strings.Split(s, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return Parsed{Dirty: p.Dirty} // not a version we can order
		}
		p.Nums = append(p.Nums, n)
	}
	p.OK = len(p.Nums) > 0
	return p
}

// Compare orders two parsed versions: negative when a is older, zero when they
// are the same build, positive when a is newer.
func Compare(a, b Parsed) int {
	for i := 0; i < max(len(a.Nums), len(b.Nums)); i++ {
		// A missing component is zero, so "1.2" and "1.2.0" are the same.
		if c := at(a.Nums, i) - at(b.Nums, i); c != 0 {
			return sign(c)
		}
	}
	// A pre-release comes before the release it leads up to.
	if (a.Pre == "") != (b.Pre == "") {
		if a.Pre == "" {
			return 1
		}
		return -1
	}
	if a.Pre != b.Pre {
		return strings.Compare(a.Pre, b.Pre)
	}
	// Same tag: whichever build has more commits on top of it is newer.
	return sign(a.Ahead - b.Ahead)
}

// IsNewer reports whether latest is a version worth offering over current.
// An unparseable current (a "dev" build) is never told it is out of date: we
// have no idea what is in it, and offering an update that might be a downgrade
// is worse than staying quiet.
func IsNewer(latest, current string) bool {
	l, c := Parse(latest), Parse(current)
	if !l.OK || !c.OK {
		return false
	}
	return Compare(l, c) > 0
}

func at(nums []int, i int) int {
	if i < len(nums) {
		return nums[i]
	}
	return 0
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
