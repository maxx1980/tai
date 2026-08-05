package server

import (
	"fmt"
	"net"
	"net/http"
	"strconv"

	"webssh/internal/appwin"
	"webssh/internal/config"
	"webssh/internal/store"
)

// serviceURL builds the browser URL for one of a host's web services. Proxmox VE
// and Backup Server always serve TLS, so those are https regardless of port.
//
// The port is omitted only when it is the one the scheme already implies (80 for
// http, 443 for https) — never because it is the service's usual port. Dropping
// :8006 from a PVE URL would point the browser at 443 instead, which is a
// different server.
func serviceURL(h store.Host, kind string) (string, error) {
	host := hostAddr(h)
	if host == "" {
		return "", fmt.Errorf("host has no address")
	}

	var scheme string
	var port int
	switch kind {
	case "pve":
		scheme, port = "https", h.PVEPort
	case "pbs":
		scheme, port = "https", h.PBSPort
	case "http":
		scheme, port = "http", h.HTTPPort
	case "https":
		scheme, port = "https", h.HTTPSPort
	default:
		return "", fmt.Errorf("unknown service %q", kind)
	}
	if port == 0 {
		return "", fmt.Errorf("host %s has no %s port configured", h.Alias, kind)
	}

	if (scheme == "http" && port == 80) || (scheme == "https" && port == 443) {
		return fmt.Sprintf("%s://%s/", scheme, host), nil
	}
	return fmt.Sprintf("%s://%s/", scheme, net.JoinHostPort(host, strconv.Itoa(port))), nil
}

// handleLaunchURL opens a host's web service (PVE, PBS, plain HTTP or HTTPS) in
// the user's real browser. It goes through the server rather than window.open so
// an admin panel lands in a normal browser window with an address bar, even when
// webssh itself is running as a chromeless app window.
func (s *Server) handleLaunchURL(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var body struct {
		Kind string `json:"kind"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	h, err := s.st.GetHost(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	url, err := serviceURL(h, body.Kind)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := appwin.OpenURL(s.settingsWithDefaults()[config.KeyBrowserCmd], url); err != nil {
		writeErr(w, 500, fmt.Errorf("open %s: %w", url, err))
		return
	}
	writeJSON(w, 200, map[string]any{"url": url})
}
