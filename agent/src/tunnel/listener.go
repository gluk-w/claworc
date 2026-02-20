package tunnel

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/gluk-w/claworc/agent/config"
)

// Sessions stores active yamux sessions keyed by remote address.
var Sessions sync.Map

// ListenTunnel starts a TLS listener on cfg.TunnelAddr that accepts
// inbound WebSocket connections from the control plane and wraps each
// in a yamux session for multiplexed streaming.
func ListenTunnel(cfg config.Config) error {
	cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		return err
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS12,
	}

	ln, err := tls.Listen("tcp", cfg.TunnelAddr, tlsCfg)
	if err != nil {
		return err
	}

	log.Printf("tunnel: listening on %s (mTLS)", cfg.TunnelAddr)

	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel", tunnelHandler)

	srv := &http.Server{
		Handler:   mux,
		TLSConfig: tlsCfg,
	}

	// Serve on the already-TLS-wrapped listener (no need for ServeTLS).
	return srv.Serve(ln)
}

func tunnelHandler(w http.ResponseWriter, r *http.Request) {
	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("tunnel: websocket accept error: %v", err)
		return
	}

	remoteAddr := r.RemoteAddr
	log.Printf("tunnel: connection accepted from %s", remoteAddr)

	// Wrap the WebSocket as a net.Conn for yamux.
	netConn := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)

	session, err := yamux.Server(netConn, nil)
	if err != nil {
		log.Printf("tunnel: yamux server error: %v", err)
		wsConn.CloseNow()
		return
	}

	Sessions.Store(remoteAddr, session)
	defer func() {
		Sessions.Delete(remoteAddr)
		session.Close()
	}()

	log.Printf("tunnel: yamux session established with %s", remoteAddr)

	// Accept streams until the session closes.
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			if !isSessionClosed(err) {
				log.Printf("tunnel: accept stream error from %s: %v", remoteAddr, err)
			}
			return
		}
		log.Printf("tunnel: stream %d accepted from %s (closing — no handlers registered yet)", stream.StreamID(), remoteAddr)
		stream.Close()
	}
}

func isSessionClosed(err error) bool {
	return err == yamux.ErrSessionShutdown || isNetClosedErr(err)
}

func isNetClosedErr(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		return opErr.Err.Error() == "use of closed network connection"
	}
	return false
}
