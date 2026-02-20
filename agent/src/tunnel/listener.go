package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/gluk-w/claworc/agent/config"
)

// Sessions stores active yamux sessions keyed by remote address.
var Sessions sync.Map

// StreamHandler is called for each accepted yamux stream. The default
// reads a channel header and routes to the registered ChannelHandler.
// Override in tests to install custom behavior (e.g., echo).
var StreamHandler func(stream *yamux.Stream) = routeStream

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
		MinVersion:   tls.VersionTLS12,
	}

	// If the control-plane CA certificate is available, require and verify
	// client certificates (full mTLS). Otherwise fall back to requiring any
	// client cert without chain verification.
	if cpCA, err := os.ReadFile(cfg.ControlPlaneCA); err == nil && len(cpCA) > 0 {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(cpCA) {
			tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
			tlsCfg.ClientCAs = pool
			log.Printf("tunnel: mTLS hardened — verifying client certs against %s", cfg.ControlPlaneCA)
		} else {
			log.Printf("tunnel: warning: could not parse control-plane CA from %s, falling back to RequireAnyClientCert", cfg.ControlPlaneCA)
			tlsCfg.ClientAuth = tls.RequireAnyClientCert
		}
	} else {
		log.Printf("tunnel: control-plane CA not found at %s, using RequireAnyClientCert", cfg.ControlPlaneCA)
		tlsCfg.ClientAuth = tls.RequireAnyClientCert
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
		go StreamHandler(stream)
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
