// Package telnet speaks enough of RFC 854/855 to drive a remote login from a
// browser terminal, without shelling out to a telnet(1) binary — most current
// distributions no longer ship one, and requiring it would make the feature
// fail on a clean install.
//
// A Conn looks like a byte stream: Read returns only application data, with
// option negotiation answered and stripped along the way, and Write escapes the
// IAC byte so binary input cannot be mistaken for a command. The negotiation is
// deliberately minimal — the client agrees to terminal type and window size (so
// full-screen programs lay out correctly), lets the server take over echo and
// suppress go-ahead (character-at-a-time mode, which is what xterm.js expects),
// and refuses everything else.
package telnet

import (
	"bufio"
	"context"
	"net"
	"sync"
	"sync/atomic"
)

// Telnet protocol bytes (RFC 854).
const (
	iac  = 255 // Interpret As Command — introduces every command sequence
	dont = 254
	do   = 253
	wont = 252
	will = 251
	sb   = 250 // start subnegotiation
	se   = 240 // end subnegotiation
)

// Options this client understands (RFC 857, 858, 1091, 1073).
const (
	optEcho  = 1  // server echoes typed characters
	optSGA   = 3  // suppress go-ahead: character-at-a-time mode
	optTType = 24 // terminal type
	optNAWS  = 31 // negotiate about window size
)

// termType is announced to the server; it matches the TERM the PTY-backed ssh
// session sets, so both terminals render the same.
const termType = "xterm-256color"

// parse states for the IAC state machine, which must survive a read boundary
// falling in the middle of a command sequence.
type state int

const (
	stData state = iota
	stIAC
	stCmd // an option byte is expected for the pending command
	stSub // inside a subnegotiation, collecting payload
	stSubIAC
)

// Conn is a telnet connection to a remote host.
type Conn struct {
	conn net.Conn
	br   *bufio.Reader

	// writeMu serialises writes: negotiation replies are produced by the read
	// path while the caller's input is written from another goroutine.
	writeMu sync.Mutex

	st      state
	cmd     byte   // pending DO/DONT/WILL/WONT
	subOpt  byte   // option being subnegotiated
	subData []byte // subnegotiation payload

	// Agreed option state, tracked so a request for an option already in the
	// requested state is not acknowledged again (RFC 854) — acknowledging would
	// bounce the same command back and forth forever. These maps belong to the
	// read goroutine and must not be touched from anywhere else.
	weWill   map[byte]bool
	theyWill map[byte]bool

	// nawsAgreed mirrors weWill[optNAWS] for Resize, which is called from the
	// websocket control goroutine while the read goroutine is negotiating. A
	// plain map would be a concurrent read and write, which Go aborts on.
	nawsAgreed atomic.Bool

	sizeMu     sync.Mutex
	rows, cols uint16
}

// Dial opens a telnet connection to addr ("host:port").
func Dial(ctx context.Context, addr string) (*Conn, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &Conn{
		conn:     nc,
		br:       bufio.NewReader(nc),
		weWill:   map[byte]bool{},
		theyWill: map[byte]bool{},
		rows:     24,
		cols:     80,
	}
	// Open with our preferences. Servers that ignore them simply never reply,
	// and the connection still works in line mode.
	c.send(will, optTType)
	c.send(will, optNAWS)
	c.send(do, optSGA)
	c.send(do, optEcho)
	return c, nil
}

// Read returns application data with all option negotiation handled and
// removed. It blocks until at least one data byte is available, so a burst of
// pure negotiation never looks like end of stream.
func (c *Conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := 0
	for n < len(p) {
		b, err := c.br.ReadByte()
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		if data, ok := c.step(b); ok {
			p[n] = data
			n++
		}
		// Hand off what we have as soon as the socket is drained; keep reading
		// while only negotiation has arrived.
		if n > 0 && c.br.Buffered() == 0 {
			break
		}
	}
	return n, nil
}

// step feeds one received byte through the IAC state machine, returning the
// byte to pass to the caller when it is application data.
func (c *Conn) step(b byte) (byte, bool) {
	switch c.st {
	case stData:
		if b == iac {
			c.st = stIAC
			return 0, false
		}
		return b, true

	case stIAC:
		switch b {
		case iac: // escaped 0xFF — real data
			c.st = stData
			return iac, true
		case do, dont, will, wont:
			c.cmd, c.st = b, stCmd
		case sb:
			c.st, c.subOpt, c.subData = stSub, 0, c.subData[:0]
		default: // NOP, Data Mark, Break, ... — nothing to do
			c.st = stData
		}
		return 0, false

	case stCmd:
		c.negotiate(c.cmd, b)
		c.st = stData
		return 0, false

	case stSub:
		if b == iac {
			c.st = stSubIAC
			return 0, false
		}
		if c.subOpt == 0 && len(c.subData) == 0 {
			c.subOpt = b
		} else {
			c.subData = append(c.subData, b)
		}
		return 0, false

	case stSubIAC:
		if b == se {
			c.subnegotiate()
			c.st = stData
			return 0, false
		}
		// IAC IAC inside a subnegotiation is a literal 0xFF payload byte.
		if b == iac {
			c.subData = append(c.subData, iac)
		}
		c.st = stSub
		return 0, false
	}
	c.st = stData
	return 0, false
}

// negotiate answers a DO/DONT/WILL/WONT for one option.
func (c *Conn) negotiate(cmd, opt byte) {
	switch cmd {
	case do: // the server asks us to enable opt
		if opt == optTType || opt == optNAWS {
			if !c.weWill[opt] {
				c.weWill[opt] = true
				c.send(will, opt)
			}
			if opt == optNAWS {
				c.nawsAgreed.Store(true)
				c.sendNAWS()
			}
			return
		}
		c.weWill[opt] = false
		c.send(wont, opt)

	case dont:
		if c.weWill[opt] {
			c.weWill[opt] = false
			if opt == optNAWS {
				c.nawsAgreed.Store(false)
			}
			c.send(wont, opt)
		}

	case will: // the server offers to enable opt
		if opt == optEcho || opt == optSGA {
			if !c.theyWill[opt] {
				c.theyWill[opt] = true
				c.send(do, opt)
			}
			return
		}
		c.theyWill[opt] = false
		c.send(dont, opt)

	case wont:
		if c.theyWill[opt] {
			c.theyWill[opt] = false
			c.send(dont, opt)
		}
	}
}

// subnegotiate handles the payload of a completed IAC SB ... IAC SE. Only the
// terminal-type SEND request needs an answer; anything else is ignored.
func (c *Conn) subnegotiate() {
	const ttypeSend, ttypeIs = 1, 0
	if c.subOpt != optTType || len(c.subData) == 0 || c.subData[0] != ttypeSend {
		return
	}
	out := []byte{iac, sb, optTType, ttypeIs}
	out = append(out, termType...)
	out = append(out, iac, se)
	c.rawWrite(out)
}

// SetWindowSize records the terminal geometry and, once the server has accepted
// NAWS, tells it — so full-screen programs on the far end resize with the
// browser window.
func (c *Conn) SetWindowSize(rows, cols uint16) error {
	c.sizeMu.Lock()
	c.rows, c.cols = rows, cols
	c.sizeMu.Unlock()
	if !c.nawsAgreed.Load() {
		return nil // server never agreed; nothing to send
	}
	c.sendNAWS()
	return nil
}

// Resize matches the pty.Session signature so both kinds of terminal session
// can be driven through one interface.
func (c *Conn) Resize(rows, cols uint16) error { return c.SetWindowSize(rows, cols) }

func (c *Conn) sendNAWS() {
	c.sizeMu.Lock()
	rows, cols := c.rows, c.cols
	c.sizeMu.Unlock()
	// Width and height are 16-bit, big-endian, and each 0xFF byte is doubled
	// like any other IAC in the stream.
	payload := []byte{byte(cols >> 8), byte(cols), byte(rows >> 8), byte(rows)}
	out := []byte{iac, sb, optNAWS}
	for _, b := range payload {
		out = append(out, b)
		if b == iac {
			out = append(out, iac)
		}
	}
	out = append(out, iac, se)
	c.rawWrite(out)
}

// Write sends terminal input, escaping IAC so a 0xFF byte is delivered as data.
func (c *Conn) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p))
	for _, b := range p {
		out = append(out, b)
		if b == iac {
			out = append(out, iac)
		}
	}
	if err := c.rawWrite(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *Conn) send(cmd, opt byte) { c.rawWrite([]byte{iac, cmd, opt}) }

func (c *Conn) rawWrite(b []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.conn.Write(b)
	return err
}

// Close ends the session. Like pty.Session.Close it reports no error: callers
// close in a defer during teardown, where nothing can act on one.
func (c *Conn) Close() { _ = c.conn.Close() }
