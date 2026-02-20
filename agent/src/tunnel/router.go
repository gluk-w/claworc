package tunnel

import (
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// ChannelHandler handles a yamux stream for a specific channel.
// The stream's channel header has already been consumed; the handler
// receives the stream positioned at the first byte after the newline.
type ChannelHandler func(conn net.Conn)

var (
	channelMu       sync.RWMutex
	channelHandlers = make(map[string]ChannelHandler)
)

// RegisterChannel registers a handler for the given channel name.
// It is safe to call from multiple goroutines.
func RegisterChannel(name string, handler ChannelHandler) {
	channelMu.Lock()
	defer channelMu.Unlock()
	channelHandlers[name] = handler
}

// routeStream reads the channel header from a yamux stream and
// dispatches it to the registered ChannelHandler.
func routeStream(stream *yamux.Stream) {
	// Give the client 5 seconds to send the channel header.
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))

	channel, err := readChannelHeader(stream)
	if err != nil {
		log.Printf("tunnel: failed to read channel header from stream %d: %v", stream.StreamID(), err)
		stream.Close()
		return
	}

	// Clear the deadline for the actual handler.
	stream.SetReadDeadline(time.Time{})

	channelMu.RLock()
	handler, ok := channelHandlers[channel]
	channelMu.RUnlock()

	if !ok {
		log.Printf("tunnel: unknown channel %q on stream %d, closing", channel, stream.StreamID())
		stream.Close()
		return
	}

	handler(stream)
}

// readChannelHeader reads a newline-terminated channel name from r.
// It reads one byte at a time to avoid buffering past the header.
func readChannelHeader(r io.Reader) (string, error) {
	var buf []byte
	b := make([]byte, 1)
	for {
		_, err := r.Read(b)
		if err != nil {
			return "", err
		}
		if b[0] == '\n' {
			return string(buf), nil
		}
		buf = append(buf, b[0])
		if len(buf) > 64 {
			return "", errors.New("channel header exceeds 64 bytes")
		}
	}
}

// PingHandler returns a ChannelHandler that responds to health-check pings.
// It writes "pong\n" and closes the stream.
func PingHandler() ChannelHandler {
	return func(conn net.Conn) {
		defer conn.Close()
		conn.Write([]byte("pong\n"))
	}
}

// HTTPChannelHandler returns a ChannelHandler that serves HTTP/1.1
// requests from the yamux stream using the given handler. Each stream
// is treated as a single HTTP connection (supporting keep-alive).
// The handler blocks until the underlying connection is closed.
func HTTPChannelHandler(handler http.Handler) ChannelHandler {
	return func(conn net.Conn) {
		ln := newSingleConnListener(conn)
		srv := &http.Server{Handler: handler}
		if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Printf("tunnel: http channel serve error: %v", err)
		}
	}
}

// singleConnListener is a net.Listener that yields exactly one net.Conn
// on the first Accept call. When that connection is closed (by the HTTP
// server or after a WebSocket hijack), the listener automatically closes
// itself, causing http.Server.Serve to return.
type singleConnListener struct {
	connCh    chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
	localAddr net.Addr
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	l := &singleConnListener{
		connCh:    make(chan net.Conn, 1),
		done:      make(chan struct{}),
		localAddr: conn.LocalAddr(),
	}
	l.connCh <- &closeNotifyConn{Conn: conn, listener: l}
	return l
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connCh:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return l.localAddr
}

// closeNotifyConn wraps a net.Conn and notifies the singleConnListener
// when the connection is closed, so the listener can unblock Accept.
type closeNotifyConn struct {
	net.Conn
	listener  *singleConnListener
	closeOnce sync.Once
}

func (c *closeNotifyConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(func() { c.listener.Close() })
	return err
}
