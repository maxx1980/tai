package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"runtime/debug"

	"github.com/gorilla/websocket"

	"webssh/internal/askpass"
	"webssh/internal/pty"
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

// handleTerminal bridges a browser xterm.js terminal to `ssh <alias>` over a PTY.
// The websocket carries the token as a query param (browsers can't set headers).
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if !s.validToken(r.URL.Query().Get("token")) {
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

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

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
		conn.WriteMessage(websocket.TextMessage, []byte("failed to start ssh: "+err.Error()))
		return
	}
	defer sess.Close()

	// PTY output -> browser (binary frames).
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("terminal goroutine panic: %v\n%s", rec, debug.Stack())
				conn.Close()
			}
		}()
		buf := make([]byte, 4096)
		for {
			n, err := sess.Pty.Read(buf)
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

	// Browser -> PTY. Text frames are JSON control (resize); binary frames are stdin.
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
			if _, err := io.WriteString(sess.Pty, string(data)); err != nil {
				return
			}
		}
	}
}
