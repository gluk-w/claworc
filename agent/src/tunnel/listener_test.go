package tunnel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/gluk-w/claworc/agent/config"
)

// generateTestCert creates a self-signed ECDSA P-256 certificate for testing.
// The returned PEM bytes can be written to disk or parsed in-process.
func generateTestCert(cn string) (certPEM, keyPEM []byte, err error) {
	return generateTestCertWithUsage(cn, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
}

// generateTestClientCert creates a self-signed ECDSA P-256 client certificate.
func generateTestClientCert(cn string) (certPEM, keyPEM []byte, err error) {
	return generateTestCertWithUsage(cn, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
}

func generateTestCertWithUsage(cn string, extKeyUsage []x509.ExtKeyUsage) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  extKeyUsage,
		DNSNames:     []string{cn},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

// TestListenTunnel_MutualTLS_Yamux validates the mTLS handshake and yamux
// framing end-to-end: generate a self-signed cert, start ListenTunnel on a
// random port, dial it with the matching cert using yamux.Client, open one
// stream, write "ping", and read back the echo from the server side.
func TestListenTunnel_MutualTLS_Yamux(t *testing.T) {
	// Install an echo stream handler so we can verify data round-trips.
	origHandler := StreamHandler
	StreamHandler = func(stream *yamux.Stream) {
		defer stream.Close()
		io.Copy(stream, stream)
	}
	defer func() { StreamHandler = origHandler }()

	// Generate the agent (server) certificate.
	const cn = "agent-test-instance"
	serverCertPEM, serverKeyPEM, err := generateTestCert(cn)
	if err != nil {
		t.Fatalf("generate server cert: %v", err)
	}

	// Write cert and key to temp files (ListenTunnel loads from disk).
	dir := t.TempDir()
	certFile := dir + "/agent.crt"
	keyFile := dir + "/agent.key"
	if err := os.WriteFile(certFile, serverCertPEM, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, serverKeyPEM, 0600); err != nil {
		t.Fatal(err)
	}

	// Grab a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := config.Config{
		TunnelAddr:     addr,
		TLSCert:        certFile,
		TLSKey:         keyFile,
		ControlPlaneCA: dir + "/nonexistent-ca.crt", // triggers fallback to RequireAnyClientCert
	}

	// Start the tunnel listener in the background.
	errCh := make(chan error, 1)
	go func() {
		errCh <- ListenTunnel(cfg)
	}()

	// Wait for the listener to be ready.
	waitForListener(t, addr, 2*time.Second)

	// Check that the listener didn't fail immediately.
	select {
	case err := <-errCh:
		t.Fatalf("ListenTunnel exited early: %v", err)
	default:
	}

	// Generate a client certificate (server requires mTLS).
	clientCertPEM, clientKeyPEM, err := generateTestCert("control-plane-test")
	if err != nil {
		t.Fatalf("generate client cert: %v", err)
	}
	clientCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		t.Fatalf("parse client cert: %v", err)
	}

	// Build a TLS config that trusts the server cert and presents a client cert.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(serverCertPEM) {
		t.Fatal("failed to add server cert to pool")
	}
	tlsCfg := &tls.Config{
		RootCAs:      pool,
		ServerName:   cn,
		Certificates: []tls.Certificate{clientCert},
		MinVersion:   tls.VersionTLS12,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Dial the tunnel endpoint over WebSocket with mTLS.
	wsConn, _, err := websocket.Dial(ctx, fmt.Sprintf("wss://%s/tunnel", addr), &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
			},
		},
	})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}

	// Wrap the WebSocket as a net.Conn and create a yamux client session.
	netConn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
	session, err := yamux.Client(netConn, nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	defer session.Close()

	// Open a stream and write "ping".
	stream, err := session.Open()
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	// Read back the echo from the server side.
	buf := make([]byte, 4)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}

	if got := string(buf); got != "ping" {
		t.Errorf("echo = %q, want %q", got, "ping")
	}
}

// TestListenTunnel_HardenedMTLS validates that when the control-plane CA
// cert is available, the agent uses RequireAndVerifyClientCert and only
// accepts connections presenting the correct client certificate.
func TestListenTunnel_HardenedMTLS(t *testing.T) {
	origHandler := StreamHandler
	StreamHandler = func(stream *yamux.Stream) {
		defer stream.Close()
		io.Copy(stream, stream)
	}
	defer func() { StreamHandler = origHandler }()

	const cn = "agent-test-hardened"
	serverCertPEM, serverKeyPEM, err := generateTestCert(cn)
	if err != nil {
		t.Fatalf("generate server cert: %v", err)
	}

	// Generate the control-plane client cert (with ClientAuth usage).
	clientCertPEM, clientKeyPEM, err := generateTestClientCert("claworc-control-plane")
	if err != nil {
		t.Fatalf("generate client cert: %v", err)
	}

	dir := t.TempDir()
	certFile := dir + "/agent.crt"
	keyFile := dir + "/agent.key"
	cpCAFile := dir + "/cp-ca.crt"
	if err := os.WriteFile(certFile, serverCertPEM, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, serverKeyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	// Write the client cert as the CP CA so the agent can verify it.
	if err := os.WriteFile(cpCAFile, clientCertPEM, 0644); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := config.Config{
		TunnelAddr:     addr,
		TLSCert:        certFile,
		TLSKey:         keyFile,
		ControlPlaneCA: cpCAFile,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- ListenTunnel(cfg)
	}()
	waitForListener(t, addr, 2*time.Second)

	select {
	case err := <-errCh:
		t.Fatalf("ListenTunnel exited early: %v", err)
	default:
	}

	// Connect with the correct client certificate — should succeed.
	clientCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		t.Fatalf("parse client cert: %v", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(serverCertPEM) {
		t.Fatal("failed to add server cert to pool")
	}
	tlsCfg := &tls.Config{
		RootCAs:      pool,
		ServerName:   cn,
		Certificates: []tls.Certificate{clientCert},
		MinVersion:   tls.VersionTLS12,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsConn, _, err := websocket.Dial(ctx, fmt.Sprintf("wss://%s/tunnel", addr), &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
			},
		},
	})
	if err != nil {
		t.Fatalf("websocket dial with valid cert: %v", err)
	}

	netConn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
	session, err := yamux.Client(netConn, nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	defer session.Close()

	stream, err := session.Open()
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if got := string(buf); got != "ping" {
		t.Errorf("echo = %q, want %q", got, "ping")
	}
}

// TestListenTunnel_HardenedMTLS_RejectsWrongCert validates that in hardened
// mode the agent rejects clients presenting an unknown certificate.
func TestListenTunnel_HardenedMTLS_RejectsWrongCert(t *testing.T) {
	origHandler := StreamHandler
	StreamHandler = func(stream *yamux.Stream) {
		defer stream.Close()
		io.Copy(stream, stream)
	}
	defer func() { StreamHandler = origHandler }()

	const cn = "agent-test-reject"
	serverCertPEM, serverKeyPEM, err := generateTestCert(cn)
	if err != nil {
		t.Fatalf("generate server cert: %v", err)
	}

	// Generate the "correct" client cert and write it as CP CA.
	correctClientCertPEM, _, err := generateTestClientCert("claworc-control-plane")
	if err != nil {
		t.Fatalf("generate correct client cert: %v", err)
	}

	// Generate a "wrong" client cert (different key pair).
	wrongClientCertPEM, wrongClientKeyPEM, err := generateTestClientCert("impostor")
	if err != nil {
		t.Fatalf("generate wrong client cert: %v", err)
	}
	_ = wrongClientCertPEM

	dir := t.TempDir()
	certFile := dir + "/agent.crt"
	keyFile := dir + "/agent.key"
	cpCAFile := dir + "/cp-ca.crt"
	if err := os.WriteFile(certFile, serverCertPEM, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, serverKeyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cpCAFile, correctClientCertPEM, 0644); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := config.Config{
		TunnelAddr:     addr,
		TLSCert:        certFile,
		TLSKey:         keyFile,
		ControlPlaneCA: cpCAFile,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- ListenTunnel(cfg)
	}()
	waitForListener(t, addr, 2*time.Second)

	// Try connecting with the wrong client cert — should fail.
	wrongCert, err := tls.X509KeyPair(wrongClientCertPEM, wrongClientKeyPEM)
	if err != nil {
		t.Fatalf("parse wrong cert: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(serverCertPEM)

	tlsCfg := &tls.Config{
		RootCAs:      pool,
		ServerName:   cn,
		Certificates: []tls.Certificate{wrongCert},
		MinVersion:   tls.VersionTLS12,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _, err = websocket.Dial(ctx, fmt.Sprintf("wss://%s/tunnel", addr), &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
			},
		},
	})
	if err == nil {
		t.Fatal("expected dial to fail with wrong client cert, but it succeeded")
	}
}

// waitForListener polls the given address until a TCP connection succeeds
// or the timeout elapses.
func waitForListener(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("listener on %s did not become ready within %v", addr, timeout)
}
