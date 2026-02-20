package tunnel

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// newYamuxPair creates a connected yamux client/server pair over net.Pipe.
func newYamuxPair(t *testing.T) (*yamux.Session, *yamux.Session) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })

	srv, err := yamux.Server(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })

	cli, err := yamux.Client(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })

	return cli, srv
}

func TestOpenChannel_WritesHeader(t *testing.T) {
	cli, srv := newYamuxPair(t)

	tc := &TunnelClient{session: cli}

	conn, err := tc.OpenChannel(t.Context(), "neko")
	if err != nil {
		t.Fatalf("OpenChannel: %v", err)
	}
	defer conn.Close()

	// Server side: accept the stream and read the header.
	stream, err := srv.AcceptStream()
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	defer stream.Close()

	stream.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(stream)
	header, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read header: %v", err)
	}

	header = strings.TrimSuffix(header, "\n")
	if header != "neko" {
		t.Errorf("got header %q, want %q", header, "neko")
	}
}

func TestOpenChannel_AllChannels(t *testing.T) {
	for _, ch := range []string{ChannelGateway, ChannelNeko, ChannelTerminal, ChannelFiles, ChannelLogs, ChannelPing} {
		t.Run(ch, func(t *testing.T) {
			cli, srv := newYamuxPair(t)
			tc := &TunnelClient{session: cli}

			conn, err := tc.OpenChannel(t.Context(), ch)
			if err != nil {
				t.Fatalf("OpenChannel(%q): %v", ch, err)
			}
			defer conn.Close()

			stream, err := srv.AcceptStream()
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()

			stream.SetReadDeadline(time.Now().Add(2 * time.Second))
			reader := bufio.NewReader(stream)
			header, _ := reader.ReadString('\n')
			header = strings.TrimSuffix(header, "\n")
			if header != ch {
				t.Errorf("got %q, want %q", header, ch)
			}
		})
	}
}

func TestOpenChannel_DataAfterHeader(t *testing.T) {
	cli, srv := newYamuxPair(t)
	tc := &TunnelClient{session: cli}

	conn, err := tc.OpenChannel(t.Context(), "neko")
	if err != nil {
		t.Fatal(err)
	}

	// Write some payload after the channel header.
	_, err = conn.Write([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	stream, err := srv.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	stream.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(stream)
	header, _ := reader.ReadString('\n')
	if strings.TrimSuffix(header, "\n") != "neko" {
		t.Fatalf("bad header: %q", header)
	}

	buf := make([]byte, 32)
	n, _ := reader.Read(buf)
	if string(buf[:n]) != "hello" {
		t.Errorf("got payload %q, want %q", string(buf[:n]), "hello")
	}
}

func TestOpenChannel_NotConnected(t *testing.T) {
	tc := &TunnelClient{}
	_, err := tc.OpenChannel(t.Context(), "neko")
	if err == nil {
		t.Fatal("expected error when not connected")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- sendPing tests ----

// pongServer accepts yamux streams on the server session, reads the
// "ping\n" channel header, and writes "pong\n" back.
func pongServer(t *testing.T, srv *yamux.Session) {
	t.Helper()
	go func() {
		for {
			stream, err := srv.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				defer stream.Close()
				reader := bufio.NewReader(stream)
				header, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSuffix(header, "\n") == ChannelPing {
					stream.Write([]byte("pong\n"))
				}
			}()
		}
	}()
}

func TestSendPing_Success(t *testing.T) {
	cli, srv := newYamuxPair(t)
	pongServer(t, srv)

	tc := &TunnelClient{instanceID: 1, instanceName: "test", session: cli}

	err := tc.sendPing(t.Context())
	if err != nil {
		t.Fatalf("sendPing failed: %v", err)
	}
}

func TestSendPing_ClosedSession(t *testing.T) {
	a, b := net.Pipe()
	cli, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	cli.Close()

	tc := &TunnelClient{instanceID: 1, instanceName: "test", session: cli}

	err = tc.sendPing(t.Context())
	if err == nil {
		t.Fatal("expected error on closed session")
	}
}

func TestSendPing_BadResponse(t *testing.T) {
	cli, srv := newYamuxPair(t)

	// Server that sends wrong response.
	go func() {
		for {
			stream, err := srv.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				defer stream.Close()
				reader := bufio.NewReader(stream)
				reader.ReadString('\n') // consume header
				stream.Write([]byte("wrong\n"))
			}()
		}
	}()

	tc := &TunnelClient{instanceID: 1, instanceName: "test", session: cli}

	err := tc.sendPing(t.Context())
	if err == nil {
		t.Fatal("expected error for bad ping response")
	}
	if !strings.Contains(err.Error(), "unexpected ping response") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStartPing_ClosesSessionOnFailure(t *testing.T) {
	cli, srv := newYamuxPair(t)

	// Server that never responds to pings (just closes the stream).
	go func() {
		for {
			stream, err := srv.AcceptStream()
			if err != nil {
				return
			}
			stream.Close()
		}
	}()

	tc := &TunnelClient{instanceID: 1, instanceName: "test", session: cli}

	// Use a short ping interval for the test.
	oldInterval := PingInterval
	PingInterval = 10 * time.Millisecond
	t.Cleanup(func() { PingInterval = oldInterval })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	tc.StartPing(ctx)

	// Wait for the ping to fail and close the session.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tc.IsClosed() {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session was not closed after ping failure")
}
