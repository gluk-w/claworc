package tunnel

import (
	"bufio"
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
	for _, ch := range []string{ChannelGateway, ChannelNeko, ChannelTerminal, ChannelFiles, ChannelLogs} {
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
