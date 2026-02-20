package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func TestGenerateAgentCertPair(t *testing.T) {
	certPEM, keyPEM, err := GenerateAgentCertPair("test-instance")
	if err != nil {
		t.Fatalf("GenerateAgentCertPair() error = %v", err)
	}

	// Verify cert PEM is non-empty and valid
	if certPEM == "" {
		t.Fatal("certPEM is empty")
	}
	if keyPEM == "" {
		t.Fatal("keyPEM is empty")
	}

	// Parse the certificate
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("failed to decode cert PEM")
	}
	if block.Type != "CERTIFICATE" {
		t.Fatalf("cert PEM block type = %q, want CERTIFICATE", block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}

	// Verify CommonName
	if cert.Subject.CommonName != "agent-test-instance" {
		t.Errorf("CommonName = %q, want %q", cert.Subject.CommonName, "agent-test-instance")
	}

	// Verify validity (~10 years)
	expectedDuration := 10 * 365 * 24 * time.Hour
	actualDuration := cert.NotAfter.Sub(cert.NotBefore)
	if actualDuration < expectedDuration-time.Hour || actualDuration > expectedDuration+time.Hour {
		t.Errorf("validity duration = %v, want ~%v", actualDuration, expectedDuration)
	}

	// Verify KeyUsage
	if cert.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Error("KeyUsageKeyEncipherment not set")
	}
	if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Error("KeyUsageDigitalSignature not set")
	}

	// Verify ExtKeyUsage
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ExtKeyUsage = %v, want [ServerAuth]", cert.ExtKeyUsage)
	}

	// Verify it's ECDSA P-256
	pubKey, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatal("public key is not ECDSA")
	}
	if pubKey.Curve != elliptic.P256() {
		t.Error("curve is not P-256")
	}

	// Parse the private key
	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		t.Fatal("failed to decode key PEM")
	}
	if keyBlock.Type != "EC PRIVATE KEY" {
		t.Fatalf("key PEM block type = %q, want EC PRIVATE KEY", keyBlock.Type)
	}

	privKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatalf("ParseECPrivateKey() error = %v", err)
	}

	// Verify the key matches the cert
	if !privKey.PublicKey.Equal(pubKey) {
		t.Error("private key does not match certificate public key")
	}

	// Verify the cert+key pair can be used as a TLS certificate
	_, err = tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}
}

func TestGenerateAgentCertPair_UniquePerCall(t *testing.T) {
	cert1, key1, err := GenerateAgentCertPair("inst-a")
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}

	cert2, key2, err := GenerateAgentCertPair("inst-a")
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}

	if cert1 == cert2 {
		t.Error("two calls with same name produced identical certs")
	}
	if key1 == key2 {
		t.Error("two calls with same name produced identical keys")
	}
}

func TestGenerateAgentCertPair_DifferentNames(t *testing.T) {
	certPEM1, _, err := GenerateAgentCertPair("alpha")
	if err != nil {
		t.Fatalf("GenerateAgentCertPair(alpha) error = %v", err)
	}

	certPEM2, _, err := GenerateAgentCertPair("beta")
	if err != nil {
		t.Fatalf("GenerateAgentCertPair(beta) error = %v", err)
	}

	// Parse and verify different CommonNames
	block1, _ := pem.Decode([]byte(certPEM1))
	cert1, _ := x509.ParseCertificate(block1.Bytes)

	block2, _ := pem.Decode([]byte(certPEM2))
	cert2, _ := x509.ParseCertificate(block2.Bytes)

	if cert1.Subject.CommonName == cert2.Subject.CommonName {
		t.Errorf("both certs have CommonName = %q, expected different", cert1.Subject.CommonName)
	}

	if !strings.Contains(cert1.Subject.CommonName, "alpha") {
		t.Errorf("cert1 CommonName = %q, expected to contain 'alpha'", cert1.Subject.CommonName)
	}
	if !strings.Contains(cert2.Subject.CommonName, "beta") {
		t.Errorf("cert2 CommonName = %q, expected to contain 'beta'", cert2.Subject.CommonName)
	}
}

func TestGenerateAgentCertPair_SelfSigned(t *testing.T) {
	certPEM, _, err := GenerateAgentCertPair("self-signed-test")
	if err != nil {
		t.Fatalf("GenerateAgentCertPair() error = %v", err)
	}

	block, _ := pem.Decode([]byte(certPEM))
	cert, _ := x509.ParseCertificate(block.Bytes)

	// Verify self-signed: issuer == subject
	if cert.Issuer.CommonName != cert.Subject.CommonName {
		t.Errorf("Issuer.CN = %q, Subject.CN = %q; expected equal for self-signed",
			cert.Issuer.CommonName, cert.Subject.CommonName)
	}

	// Verify the cert can verify itself (self-signed)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	_, err = cert.Verify(x509.VerifyOptions{
		Roots: pool,
	})
	if err != nil {
		t.Errorf("self-signed verification failed: %v", err)
	}
}

func TestGenerateControlPlaneCertPair(t *testing.T) {
	certPEM, keyPEM, err := GenerateControlPlaneCertPair()
	if err != nil {
		t.Fatalf("GenerateControlPlaneCertPair() error = %v", err)
	}

	if certPEM == "" {
		t.Fatal("certPEM is empty")
	}
	if keyPEM == "" {
		t.Fatal("keyPEM is empty")
	}

	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("failed to decode cert PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}

	// Verify CommonName
	if cert.Subject.CommonName != "claworc-control-plane" {
		t.Errorf("CommonName = %q, want %q", cert.Subject.CommonName, "claworc-control-plane")
	}

	// Verify ExtKeyUsage is ClientAuth
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("ExtKeyUsage = %v, want [ClientAuth]", cert.ExtKeyUsage)
	}

	// Verify it's ECDSA P-256
	pubKey, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatal("public key is not ECDSA")
	}
	if pubKey.Curve != elliptic.P256() {
		t.Error("curve is not P-256")
	}

	// Verify the cert+key pair can be used as a TLS certificate
	_, err = tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}

	// Verify self-signed
	if cert.Issuer.CommonName != cert.Subject.CommonName {
		t.Errorf("not self-signed: Issuer.CN = %q, Subject.CN = %q",
			cert.Issuer.CommonName, cert.Subject.CommonName)
	}
}
