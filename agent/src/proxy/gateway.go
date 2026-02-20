package proxy

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// GatewayHandler returns an http.Handler that reverse-proxies HTTP and
// WebSocket requests to the OpenClaw gateway running at gatewayAddr.
// It rewrites the Host header to "localhost" so the gateway accepts
// the request, and sets X-Real-IP / X-Forwarded-For to 127.0.0.1.
func GatewayHandler(gatewayAddr string) http.Handler {
	target := &url.URL{
		Scheme: "http",
		Host:   gatewayAddr,
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = "localhost"
			pr.Out.Header.Set("X-Real-IP", "127.0.0.1")
			pr.Out.Header.Set("X-Forwarded-For", "127.0.0.1")
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			proxyWebSocket(w, r, gatewayAddr)
			return
		}
		rp.ServeHTTP(w, r)
	})
}

func proxyWebSocket(w http.ResponseWriter, r *http.Request, gatewayAddr string) {
	// Negotiate subprotocols from the client request.
	requestedProtocol := r.Header.Get("Sec-WebSocket-Protocol")
	var subprotocols []string
	if requestedProtocol != "" {
		subprotocols = strings.Split(requestedProtocol, ", ")
	}

	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       subprotocols,
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("gateway ws: accept error: %v", err)
		return
	}
	defer clientConn.CloseNow()

	// Build upstream ws:// URL targeting the gateway on localhost.
	wsURL := "ws://" + gatewayAddr + r.URL.Path
	if r.URL.RawQuery != "" {
		wsURL += "?" + r.URL.RawQuery
	}

	dialCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	upstreamConn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		Subprotocols: subprotocols,
		HTTPHeader: http.Header{
			"Host":            []string{"localhost"},
			"X-Real-IP":      []string{"127.0.0.1"},
			"X-Forwarded-For": []string{"127.0.0.1"},
		},
	})
	if err != nil {
		log.Printf("gateway ws: upstream dial error for %s: %v", wsURL, err)
		clientConn.Close(websocket.StatusBadGateway, "cannot connect to gateway")
		return
	}
	defer upstreamConn.CloseNow()

	clientConn.SetReadLimit(4 * 1024 * 1024)
	upstreamConn.SetReadLimit(4 * 1024 * 1024)

	relayCtx, relayCancel := context.WithCancel(r.Context())
	defer relayCancel()

	// Client -> Upstream
	go func() {
		defer relayCancel()
		for {
			msgType, data, err := clientConn.Read(relayCtx)
			if err != nil {
				return
			}
			if err := upstreamConn.Write(relayCtx, msgType, data); err != nil {
				return
			}
		}
	}()

	// Upstream -> Client
	func() {
		defer relayCancel()
		for {
			msgType, data, err := upstreamConn.Read(relayCtx)
			if err != nil {
				return
			}
			if err := clientConn.Write(relayCtx, msgType, data); err != nil {
				return
			}
		}
	}()

	clientConn.Close(websocket.StatusNormalClosure, "")
	upstreamConn.Close(websocket.StatusNormalClosure, "")
}
