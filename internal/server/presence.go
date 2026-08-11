package server

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"webssh/internal/config"
)

// Presence tracking: every open tab holds one /api/presence websocket. Closing
// the tab tears the TCP connection down in the kernel, so this fires even when
// the browser is killed outright — unlike a beforeunload beacon, which is best
// effort and also cannot tell a close apart from a reload.
//
// When the count reaches zero the daemon waits exitGrace (long enough for a
// reload to reconnect) and then exits, unless exit_on_close has been turned off.

const (
	presencePing    = 30 * time.Second
	presenceTimeout = 70 * time.Second
)

// Quit is closed when the daemon should shut down because the last tab is gone.
func (s *Server) Quit() <-chan struct{} { return s.quit }

// HasClients reports whether at least one browser tab has established its
// presence websocket. The launcher uses this after spawning a Chromium app
// window: no connection means it should fall back to a normal browser tab.
func (s *Server) HasClients() bool { return s.clients.Load() > 0 }

// exitOnClose reports the current setting; it is read at grace-timer fire time
// so toggling it in the UI takes effect without a restart.
func (s *Server) exitOnClose() bool {
	return s.st.GetSetting(config.KeyExitOnClose, config.Defaults(s.paths.Home)[config.KeyExitOnClose]) != "0"
}

func (s *Server) addClient() {
	s.clients.Add(1)
	// A tab came back before the grace period expired — call off the shutdown.
	s.graceMu.Lock()
	if s.graceT != nil {
		s.graceT.Stop()
		s.graceT = nil
	}
	s.graceMu.Unlock()
}

func (s *Server) removeClient() {
	if s.clients.Add(-1) > 0 {
		return
	}
	s.graceMu.Lock()
	defer s.graceMu.Unlock()
	if s.graceT != nil {
		s.graceT.Stop()
	}
	s.graceT = time.AfterFunc(exitGrace, func() {
		// Re-check both conditions: a tab may have reconnected, or the user may
		// have turned the setting off, while the timer was pending.
		if s.clients.Load() > 0 || !s.exitOnClose() {
			return
		}
		log.Printf("no browser tab left for %s — shutting down (disable with exit_on_close)", exitGrace)
		s.quitOnce.Do(func() { close(s.quit) })
	})
}

// handlePresence keeps a websocket open for the lifetime of a browser tab. It
// carries no data; the connection existing is the whole signal. Deliberately
// not behind requireUnlocked: a locked tab is still an open tab, and the socket
// exposes nothing.
func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	if !s.authDisabled.Load() && !s.validToken(r.URL.Query().Get("token")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	s.addClient()
	defer s.removeClient()

	// Pings keep a wedged connection (suspended laptop, stalled socket) from
	// being mistaken for an open tab forever.
	conn.SetReadDeadline(time.Now().Add(presenceTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(presenceTimeout))
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(presencePing)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}
