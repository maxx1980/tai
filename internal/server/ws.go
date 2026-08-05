package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"

	"github.com/gorilla/websocket"

	"webssh/internal/askpass"
	"webssh/internal/pty"
	"webssh/internal/store"
	"webssh/internal/telnet"
)

var upgrader = websocket.Upgrader{
	// Only accept same-origin (loopback) websocket connections.
	CheckOrigin: func(r *http.Request) bool { return originIsLocal(r) },
}

// ctrlMsg is a JSON control frame sent by the browser (currently only resize).
type ctrlMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// termSession is the far end of a browser terminal: an ssh process on a PTY, or
// a direct telnet connection. Both are just a resizable byte stream here.
type termSession interface {
	io.ReadWriter
	Resize(rows, cols uint16) error
	Close()
}

// startSession connects the requested protocol. telnet is spoken natively (see
// internal/telnet) rather than by running telnet(1), which most distributions no
// longer install.
func (s *Server) startSession(ctx context.Context, h store.Host, telnetMode bool) (termSession, error) {
	if telnetMode {
		addr := net.JoinHostPort(hostAddr(h), strconv.Itoa(h.TelnetPort))
		conn, err := telnet.Dial(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("telnet %s: %w", addr, err)
		}
		return conn, nil
	}

	// When the host has a saved password, wire ssh to answer its own password
	// (and new-host-key) prompts via the SSH_ASKPASS helper, so the user isn't
	// prompted in the browser terminal.
	var extraEnv []string
	if pw := s.st.GetHostPassword(h.ID); pw != "" {
		if helper, herr := askpass.Ensure(s.paths.DataDir); herr == nil {
			extraEnv = askpass.Env(helper, pw)
		} else {
			log.Printf("askpass helper: %v", herr)
		}
	}
	sess, err := pty.Start(s.sshArgs(h), extraEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to start ssh: %w", err)
	}
	return sess, nil
}

// handleTerminal bridges a browser xterm.js terminal to a shell client over a
// PTY: `ssh` by default, or `telnet` when the request asks for ?proto=telnet and
// the host has a telnet port. The websocket carries the token as a query param
// (browsers can't set headers).
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if !s.authDisabled.Load() && !s.validToken(r.URL.Query().Get("token")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Honour the master-password gate (the browser sends the session cookie on
	// same-origin websocket handshakes).
	if !s.isUnlocked(r) {
		http.Error(w, "locked", http.StatusLocked)
		return
	}
	id, err := pathID(r)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	h, err := s.st.GetHost(id)
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}

	telnetMode := r.URL.Query().Get("proto") == "telnet"
	if telnetMode && h.TelnetPort == 0 {
		http.Error(w, "host has no telnet port", http.StatusBadRequest)
		return
	}
	if !telnetMode && h.Port == 0 {
		http.Error(w, "host has no SSH port", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	sess, err := s.startSession(r.Context(), h, telnetMode)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
		return
	}
	defer sess.Close()

	// Session output -> browser (binary frames).
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("terminal goroutine panic: %v\n%s", rec, debug.Stack())
				conn.Close()
			}
		}()
		buf := make([]byte, 4096)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				conn.Close()
				return
			}
		}
	}()

	// Browser -> session. Text frames are JSON control (resize); binary frames
	// are terminal input.
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch mt {
		case websocket.TextMessage:
			var c ctrlMsg
			if json.Unmarshal(data, &c) == nil && c.Type == "resize" {
				_ = sess.Resize(c.Rows, c.Cols)
			}
		case websocket.BinaryMessage:
			if _, err := sess.Write(data); err != nil {
				return
			}
		}
	}
}
