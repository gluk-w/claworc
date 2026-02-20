package tunnel

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// resetHandlers clears all registered channel handlers between tests.
func resetHandlers() {
	channelMu.Lock()
	channelHandlers = make(map[string]ChannelHandler)
	channelMu.Unlock()
}

// ---- readChannelHeader tests ----

func TestReadChannelHeader_Valid(t *testing.T) {
	r := strings.NewReader("neko\nrest of data")
	ch, err := readChannelHeader(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "neko" {
		t.Errorf("got channel %q, want %q", ch, "neko")
	}
}

func TestReadChannelHeader_AllChannels(t *testing.T) {
	for _, name := range []string{ChannelGateway, ChannelNeko, ChannelTerminal, ChannelFiles, ChannelLogs, ChannelPing} {
		r := strings.NewReader(name + "\n")
		ch, err := readChannelHeader(r)
		if err != nil {
			t.Errorf("channel %q: unexpected error: %v", name, err)
		}
		if ch != name {
			t.Errorf("got %q, want %q", ch, name)
		}
	}
}

func TestReadChannelHeader_TooLong(t *testing.T) {
	long := strings.Repeat("x", 65) + "\n"
	_, err := readChannelHeader(strings.NewReader(long))
	if err == nil {
		t.Fatal("expected error for header > 64 bytes")
	}
	if !strings.Contains(err.Error(), "exceeds 64 bytes") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestReadChannelHeader_EOF(t *testing.T) {
	r := strings.NewReader("neko") // no newline
	_, err := readChannelHeader(r)
	if err == nil {
		t.Fatal("expected error on EOF before newline")
	}
}

func TestReadChannelHeader_EmptyLine(t *testing.T) {
	r := strings.NewReader("\n")
	ch, err := readChannelHeader(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "" {
		t.Errorf("expected empty channel name, got %q", ch)
	}
}

// ---- Channel routing tests ----

func TestRouteStream_RegisteredChannel(t *testing.T) {
	resetHandlers()
	defer resetHandlers()

	received := make(chan string, 1)
	RegisterChannel("test-ch", func(conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 32)
		n, _ := conn.Read(buf)
		received <- string(buf[:n])
	})

	// Create a yamux pair to get a real *yamux.Stream.
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	yamuxServer, err := yamux.Server(serverConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer yamuxServer.Close()

	yamuxClient, err := yamux.Client(clientConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer yamuxClient.Close()

	// Client opens a stream and writes channel header + payload.
	clientStream, err := yamuxClient.Open()
	if err != nil {
		t.Fatal(err)
	}
	_, err = clientStream.Write([]byte("test-ch\nhello"))
	if err != nil {
		t.Fatal(err)
	}

	// Server accepts the stream and routes it.
	serverStream, err := yamuxServer.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}

	go routeStream(serverStream)

	select {
	case msg := <-received:
		if msg != "hello" {
			t.Errorf("got %q, want %q", msg, "hello")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for handler to receive data")
	}
}

func TestRouteStream_UnknownChannel(t *testing.T) {
	resetHandlers()
	defer resetHandlers()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	yamuxServer, err := yamux.Server(serverConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer yamuxServer.Close()

	yamuxClient, err := yamux.Client(clientConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer yamuxClient.Close()

	clientStream, err := yamuxClient.Open()
	if err != nil {
		t.Fatal(err)
	}
	_, err = clientStream.Write([]byte("unknown-channel\n"))
	if err != nil {
		t.Fatal(err)
	}

	serverStream, err := yamuxServer.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		routeStream(serverStream)
		close(done)
	}()

	select {
	case <-done:
		// routeStream should return after closing the unknown channel stream.
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: routeStream did not return for unknown channel")
	}

	// The stream should be closed — read should return an error.
	buf := make([]byte, 1)
	_, err = clientStream.Read(buf)
	if err == nil {
		t.Error("expected error reading from closed stream")
	}
}

// ---- HTTPChannelHandler tests ----

func TestHTTPChannelHandler_ServesRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "path=%s", r.URL.Path)
	})

	ch := HTTPChannelHandler(handler)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ch(serverConn)
	}()

	// Send an HTTP request over the pipe.
	req := "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	_, err := clientConn.Write([]byte(req))
	if err != nil {
		t.Fatal(err)
	}

	// Read the full response.
	clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := io.ReadAll(clientConn)
	if err != nil {
		t.Fatal(err)
	}

	respStr := string(resp)
	if !strings.Contains(respStr, "200 OK") {
		t.Errorf("expected 200 OK in response, got:\n%s", respStr)
	}
	if !strings.Contains(respStr, "path=/ws") {
		t.Errorf("expected path=/ws in body, got:\n%s", respStr)
	}

	wg.Wait()
}

func TestHTTPChannelHandler_MultiplePaths(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "path=%s", r.URL.Path)
	})

	ch := HTTPChannelHandler(handler)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	go ch(serverConn)

	// Send a request with Connection: close to terminate cleanly.
	req := "GET /test/path HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	_, err := clientConn.Write([]byte(req))
	if err != nil {
		t.Fatal(err)
	}

	clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := io.ReadAll(clientConn)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(resp), "path=/test/path") {
		t.Errorf("expected path=/test/path in response, got:\n%s", string(resp))
	}
}

// ---- singleConnListener tests ----

func TestSingleConnListener_AcceptOnce(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ln := newSingleConnListener(server)

	// First Accept should succeed.
	conn, err := ln.Accept()
	if err != nil {
		t.Fatalf("first Accept failed: %v", err)
	}
	if conn == nil {
		t.Fatal("first Accept returned nil conn")
	}

	// Close the connection (triggers listener close via closeNotifyConn).
	conn.Close()

	// Second Accept should return ErrClosed.
	_, err = ln.Accept()
	if err == nil {
		t.Fatal("expected error on second Accept")
	}
}

func TestSingleConnListener_CloseUnblocks(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ln := newSingleConnListener(server)

	// Consume the first Accept.
	conn, _ := ln.Accept()

	done := make(chan struct{})
	go func() {
		ln.Accept() // blocks until Close
		close(done)
	}()

	// Close via the conn wrapper.
	conn.Close()

	select {
	case <-done:
		// Accept unblocked as expected.
	case <-time.After(2 * time.Second):
		t.Fatal("second Accept did not unblock after Close")
	}
}

func TestSingleConnListener_Addr(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ln := newSingleConnListener(server)
	if ln.Addr() == nil {
		t.Error("Addr() returned nil")
	}
}

// ---- PingHandler tests ----

func TestPingHandler_RespondsPong(t *testing.T) {
	resetHandlers()
	defer resetHandlers()

	RegisterChannel(ChannelPing, PingHandler())

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	yamuxServer, err := yamux.Server(serverConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer yamuxServer.Close()

	yamuxClient, err := yamux.Client(clientConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer yamuxClient.Close()

	// Client sends "ping\n" channel header.
	clientStream, err := yamuxClient.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer clientStream.Close()

	if _, err := clientStream.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}

	// Server accepts and routes.
	serverStream, err := yamuxServer.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		routeStream(serverStream)
		close(done)
	}()

	// Read the "pong\n" response.
	clientStream.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 5)
	n, err := io.ReadFull(clientStream, buf)
	if err != nil {
		t.Fatalf("read pong: %v", err)
	}
	if string(buf[:n]) != "pong\n" {
		t.Errorf("got %q, want %q", string(buf[:n]), "pong\n")
	}

	select {
	case <-done:
		// Handler returned after writing pong and closing.
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for ping handler to complete")
	}
}

func TestReadChannelHeader_PingChannel(t *testing.T) {
	r := strings.NewReader("ping\n")
	ch, err := readChannelHeader(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != ChannelPing {
		t.Errorf("got %q, want %q", ch, ChannelPing)
	}
}

// ---- RegisterChannel tests ----

func TestRegisterChannel_Concurrent(t *testing.T) {
	resetHandlers()
	defer resetHandlers()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			name := fmt.Sprintf("ch-%d", n)
			RegisterChannel(name, func(conn net.Conn) { conn.Close() })
		}(i)
	}
	wg.Wait()

	channelMu.RLock()
	defer channelMu.RUnlock()
	if len(channelHandlers) != 10 {
		t.Errorf("expected 10 handlers, got %d", len(channelHandlers))
	}
}
