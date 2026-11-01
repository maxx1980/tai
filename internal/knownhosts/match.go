package knownhosts

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"strconv"
	"strings"

	"webssh/internal/store"
)

// With HashKnownHosts (the default in Debian/Ubuntu's /etc/ssh/ssh_config),
// ssh writes "|1|<salt>|<hash>" instead of the host name: hash is
// HMAC-SHA1(salt, name). The name cannot be recovered, but a candidate can be
// checked — exactly what `ssh-keygen -F` does. webssh checks the inventory's
// hosts so the Known hosts panel can say which machine an entry belongs to.

const hashPrefix = "|1|"

// IsHashed reports whether a host pattern is a hashed token.
func IsHashed(pattern string) bool { return strings.HasPrefix(pattern, hashPrefix) }

// matchHashed checks name against a "|1|salt|hash" token.
func matchHashed(token, name string) bool {
	parts := strings.Split(strings.TrimPrefix(token, hashPrefix), "|")
	if len(parts) != 2 {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(name))
	return hmac.Equal(mac.Sum(nil), want)
}

// MatchPattern reports whether name matches one known_hosts host field: a
// hashed token or a comma-separated pattern list (with * and ? wildcards).
// Negated patterns ("!host") only exclude, so they never produce a match.
func MatchPattern(field, name string) bool {
	name = strings.ToLower(name)
	if IsHashed(field) {
		return matchHashed(field, name)
	}
	matched := false
	for _, p := range strings.Split(field, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "!") {
			if glob(p[1:], name) {
				return false
			}
			continue
		}
		if glob(p, name) {
			matched = true
		}
	}
	return matched
}

// glob matches name against an OpenSSH pattern: * is any run of characters, ?
// one character, everything else literal — including the brackets of
// "[host]:port", which path.Match would read as a character class.
func glob(pattern, name string) bool {
	if pattern == "" {
		return name == ""
	}
	switch pattern[0] {
	case '*':
		for i := 0; i <= len(name); i++ {
			if glob(pattern[1:], name[i:]) {
				return true
			}
		}
		return false
	case '?':
		return name != "" && glob(pattern[1:], name[1:])
	default:
		return name != "" && name[0] == pattern[0] && glob(pattern[1:], name[1:])
	}
}

// LookupNames are the names ssh would have recorded for a host: its address
// and alias, in the "[name]:port" form when the SSH port is not 22.
func LookupNames(h store.Host) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range []string{h.Hostname, h.Alias} {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		if h.Port != 0 && h.Port != 22 {
			n = "[" + n + "]:" + strconv.Itoa(h.Port)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// Matches returns the inventory hosts an entry belongs to, with the name that
// matched.
func Matches(kh store.KnownHost, hosts []store.Host) []store.KnownHostMatch {
	var out []store.KnownHostMatch
	for _, h := range hosts {
		for _, n := range LookupNames(h) {
			if MatchPattern(kh.Hosts, n) {
				out = append(out, store.KnownHostMatch{HostID: h.ID, Alias: h.Alias, Name: n})
				break
			}
		}
	}
	return out
}
