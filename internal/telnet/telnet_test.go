package telnet

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// server starts a listener and hands the accepted connection to fn. It returns
// the address to dial and a channel that closes once fn returns.
func server(t *testing.T, fn func(net.Conn)) (string, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		fn(c)
	}()
	return ln.Addr().String(), done
}

// openingLen is the size of the preference block Dial sends: four three-byte
// commands (WILL TTYPE, WILL NAWS, DO SGA, DO ECHO).
const openingLen = 4 * 3

// readOpening drains exactly that block, so the test server's next read sees
// replies rather than leftover preference bytes that TCP happened to deliver in
// a separate segment.
func readOpening(t *testing.T, c net.Conn) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, openingLen)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Errorf("reading client preferences: %v", err)
		return nil
	}
	_ = c.SetReadDeadline(time.Time{})
	return buf
}

func TestDialAnnouncesPreferences(t *testing.T) {
	var got []byte
	addr, done := server(t, func(c net.Conn) { got = readOpening(t, c) })

	conn, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-done

	for _, want := range [][]byte{
		{iac, will, optTType},
		{iac, will, optNAWS},
		{iac, do, optSGA},
		{iac, do, optEcho},
	} {
		if !bytes.Contains(got, want) {
			t.Errorf("opening handshake %v is missing %v", got, want)
		}
	}
}

func TestReadStripsNegotiationAndReturnsData(t *testing.T) {
	addr, _ := server(t, func(c net.Conn) {
		readOpening(t, c)
		// Negotiation interleaved with the login banner, including an escaped
		// 0xFF that must survive as data.
		c.Write([]byte{iac, will, optEcho})
		c.Write([]byte("log"))
		c.Write([]byte{iac, will, optSGA})
		c.Write([]byte("in: "))
		c.Write([]byte{iac, iac})
		time.Sleep(150 * time.Millisecond)
	})

	conn, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var got []byte
	deadline := time.Now().Add(2 * time.Second)
	for len(got) < 8 && time.Now().Before(deadline) {
		buf := make([]byte, 32)
		n, err := conn.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	want := append([]byte("login: "), 0xFF)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRepliesToOptionRequests(t *testing.T) {
	replies := make(chan []byte, 1)
	addr, _ := server(t, func(c net.Conn) {
		readOpening(t, c)
		// DO for an option we support, DO for one we do not, and WILL for one
		// we do not want.
		c.Write([]byte{iac, do, optTType, iac, do, optEcho, iac, will, 99})
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 3*3) // three three-byte replies, possibly split across segments
		n, _ := io.ReadFull(c, buf)
		replies <- append([]byte(nil), buf[:n]...)
	})

	conn, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		buf := make([]byte, 32)
		conn.Read(buf) // drive the state machine
	}()

	select {
	case got := <-replies:
		// TTYPE is supported → WILL. ECHO is a server-side option, so being
		// told to enable it ourselves is refused. Unknown option 99 → DONT.
		for _, want := range [][]byte{
			{iac, will, optTType},
			{iac, wont, optEcho},
			{iac, dont, 99},
		} {
			if !bytes.Contains(got, want) {
				t.Errorf("replies %v missing %v", got, want)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no negotiation reply")
	}
}

func TestAnswersTerminalTypeRequest(t *testing.T) {
	replies := make(chan []byte, 1)
	addr, _ := server(t, func(c net.Conn) {
		readOpening(t, c)
		c.Write([]byte{iac, sb, optTType, 1 /* SEND */, iac, se})
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		replies <- append([]byte(nil), buf[:n]...)
	})

	conn, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		buf := make([]byte, 32)
		conn.Read(buf)
	}()

	select {
	case got := <-replies:
		want := append([]byte{iac, sb, optTType, 0 /* IS */}, termType...)
		want = append(want, iac, se)
		if !bytes.Contains(got, want) {
			t.Fatalf("got %q, want it to contain %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no terminal-type reply")
	}
}

func TestWindowSizeSentOnlyAfterAgreement(t *testing.T) {
	replies := make(chan []byte, 1)
	addr, _ := server(t, func(c net.Conn) {
		readOpening(t, c)
		c.Write([]byte{iac, do, optNAWS})
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		var acc []byte
		buf := make([]byte, 64)
		for len(acc) < 18 {
			n, err := c.Read(buf)
			acc = append(acc, buf[:n]...)
			if err != nil {
				break
			}
		}
		replies <- acc
	})

	conn, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Before the server agrees, a resize must not put anything on the wire.
	if err := conn.Resize(50, 100); err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 32)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)
	if err := conn.Resize(50, 100); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-replies:
		want := []byte{iac, sb, optNAWS, 0, 100, 0, 50, iac, se}
		if !bytes.Contains(got, want) {
			t.Fatalf("got %v, want it to contain NAWS %v", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no NAWS subnegotiation")
	}
}

// The websocket bridge resizes from the control goroutine while the read
// goroutine is still negotiating options. Run both hard against each other so
// -race catches any shared state that is not safe to touch from both.
func TestConcurrentResizeDuringNegotiation(t *testing.T) {
	addr, _ := server(t, func(c net.Conn) {
		readOpening(t, c)
		for i := 0; i < 200; i++ {
			c.Write([]byte{iac, do, optNAWS, iac, dont, optNAWS})
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		io.Copy(io.Discard, c)
	})

	conn, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()

	for i := 0; i < 300; i++ {
		if err := conn.Resize(uint16(20+i%30), uint16(80+i%40)); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()
	<-done
}

func TestWriteEscapesIAC(t *testing.T) {
	replies := make(chan []byte, 1)
	addr, _ := server(t, func(c net.Conn) {
		readOpening(t, c)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		replies <- append([]byte(nil), buf[:n]...)
	})

	conn, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	n, err := conn.Write([]byte{'a', 0xFF, 'b'})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("Write reported %d bytes, want the 3 the caller passed", n)
	}

	select {
	case got := <-replies:
		want := []byte{'a', iac, iac, 'b'}
		if !bytes.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing written")
	}
}

func TestReadReportsClose(t *testing.T) {
	addr, _ := server(t, func(c net.Conn) {
		readOpening(t, c)
		c.Close()
	})

	conn, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	buf := make([]byte, 32)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF && !isClosed(err) {
				t.Fatalf("unexpected error %v", err)
			}
			return
		}
	}
}

func isClosed(err error) bool {
	ne, ok := err.(net.Error)
	return ok && !ne.Timeout()
}

func TestDialRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing is listening now

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Dial(ctx, addr); err == nil {
		t.Fatal("dialling a dead port should fail")
	}
}
