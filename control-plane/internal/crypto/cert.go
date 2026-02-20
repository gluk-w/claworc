package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// GenerateAgentCertPair creates a self-signed ECDSA P-256 TLS certificate
// for an agent instance. The certificate is self-signed because the control
// plane stores the exact public cert for pinned verification — no shared CA
// is needed.
func GenerateAgentCertPair(instanceName string) (certPEM, keyPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ECDSA key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", fmt.Errorf("generate serial number: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: fmt.Sprintf("agent-%s", instanceName),
		},
		NotBefore:             now,
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour), // ~10 years
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("create certificate: %w", err)
	}

	certPEMBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}

	keyPEMBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})

	return string(certPEMBytes), string(keyPEMBytes), nil
}

// GenerateControlPlaneCertPair creates a self-signed ECDSA P-256 client
// certificate for the control plane. Agents verify this certificate to
// authenticate inbound tunnel connections.
func GenerateControlPlaneCertPair() (certPEM, keyPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ECDSA key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", fmt.Errorf("generate serial number: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "claworc-control-plane",
		},
		NotBefore:             now,
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("create certificate: %w", err)
	}

	certPEMBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}

	keyPEMBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})

	return string(certPEMBytes), string(keyPEMBytes), nil
}

var (
	cpCertOnce sync.Once
	cpCert     *tls.Certificate
	cpCertPEM  string
	cpCertErr  error
)

// GetControlPlaneCert returns the control-plane client TLS certificate,
// generating and persisting it on first call. The public cert PEM is also
// returned for distribution to agents.
func GetControlPlaneCert() (tlsCert *tls.Certificate, publicPEM string, err error) {
	cpCertOnce.Do(func() {
		cpCertPEM, cpCert, cpCertErr = loadOrGenerateCPCert()
	})
	return cpCert, cpCertPEM, cpCertErr
}

// ResetControlPlaneCertCache clears the cached cert (for testing).
func ResetControlPlaneCertCache() {
	cpCertOnce = sync.Once{}
	cpCert = nil
	cpCertPEM = ""
	cpCertErr = nil
}

func loadOrGenerateCPCert() (string, *tls.Certificate, error) {
	certPEM, err := database.GetSetting("cp_client_cert")
	if err == nil && certPEM != "" {
		encKeyPEM, err := database.GetSetting("cp_client_cert_key")
		if err == nil && encKeyPEM != "" {
			keyPEM, err := Decrypt(encKeyPEM)
			if err == nil {
				parsed, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
				if err == nil {
					return certPEM, &parsed, nil
				}
			}
		}
	}

	// Generate new cert pair.
	certPEM, keyPEM, err := GenerateControlPlaneCertPair()
	if err != nil {
		return "", nil, fmt.Errorf("generate control-plane cert: %w", err)
	}

	encKeyPEM, err := Encrypt(keyPEM)
	if err != nil {
		return "", nil, fmt.Errorf("encrypt control-plane key: %w", err)
	}

	if err := database.SetSetting("cp_client_cert", certPEM); err != nil {
		return "", nil, fmt.Errorf("save control-plane cert: %w", err)
	}
	if err := database.SetSetting("cp_client_cert_key", encKeyPEM); err != nil {
		return "", nil, fmt.Errorf("save control-plane key: %w", err)
	}

	parsed, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return "", nil, fmt.Errorf("parse control-plane cert: %w", err)
	}

	return certPEM, &parsed, nil
}
