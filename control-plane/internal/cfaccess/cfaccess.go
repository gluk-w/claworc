// Package cfaccess verifies Cloudflare Access (Zero Trust) JWTs.
//
// When Claworc runs behind Cloudflare Access, every proxied request carries a
// signed JWT in the Cf-Access-Jwt-Assertion header. This package validates that
// JWT against the team's JWKS endpoint (<team-domain>/cdn-cgi/access/certs),
// checks the audience/issuer/expiry, and returns the authenticated email from
// the verified claims. The plaintext Cf-Access-Authenticated-User-Email header
// is deliberately NOT trusted — only the cryptographically verified claim is.
package cfaccess

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// keyCacheTTL bounds how long cached JWKS keys are reused before the next
	// verification forces a refresh. Cloudflare rotates signing keys
	// periodically; an unknown kid also forces an out-of-band refetch.
	keyCacheTTL = 15 * time.Minute
	httpTimeout = 10 * time.Second
)

// Verifier validates Cloudflare Access JWTs against a JWKS endpoint. It is safe
// for concurrent use; one instance is shared process-wide.
type Verifier struct {
	certsURL string
	aud      string
	issuer   string
	client   *http.Client

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time

	fetchMu sync.Mutex // serializes JWKS refetches so concurrent misses collapse
}

// NewVerifier builds a Verifier. certsURL is the JWKS endpoint
// (<team-domain>/cdn-cgi/access/certs), aud is the Access application AUD tag,
// and issuer is the team domain URL that Cloudflare sets as the JWT `iss`.
func NewVerifier(certsURL, aud, issuer string) *Verifier {
	return &Verifier{
		certsURL: certsURL,
		aud:      aud,
		issuer:   issuer,
		// Plain client relying on http.DefaultTransport, which honors
		// HTTPS_PROXY and the system cert bundle from the environment. Do NOT
		// swap in a custom Transport that bypasses ProxyFromEnvironment, and
		// never set InsecureSkipVerify — JWKS integrity is security-critical.
		client: &http.Client{Timeout: httpTimeout},
		keys:   map[string]*rsa.PublicKey{},
	}
}

// Verify validates the given Cf-Access-Jwt-Assertion token and returns the
// normalized (lowercased, trimmed) email from the verified `email` claim. It
// returns an error if the signature, audience, issuer, expiry, or email claim
// fails validation.
func (v *Verifier) Verify(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", errors.New("empty token")
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithAudience(v.aud),
		jwt.WithIssuer(v.issuer),
		jwt.WithExpirationRequired(),
	)

	claims := jwt.MapClaims{}
	_, err := parser.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		// Defense-in-depth against alg-confusion: WithValidMethods already
		// rejects non-RS256, but assert the concrete signing method too.
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
		}
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("token missing kid header")
		}
		return v.keyForKID(ctx, kid)
	})
	if err != nil {
		return "", err
	}

	email, _ := claims["email"].(string)
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", errors.New("token missing email claim")
	}
	return email, nil
}

// keyForKID returns the RSA public key for the given kid, fetching/refreshing
// the JWKS when the key is missing or the cache is stale.
func (v *Verifier) keyForKID(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	key, ok := v.keys[kid]
	fetchedAt := v.fetchedAt
	v.mu.RUnlock()

	if ok && time.Since(fetchedAt) <= keyCacheTTL {
		return key, nil
	}

	if err := v.refresh(ctx, fetchedAt); err != nil {
		// Fall back to a cached (possibly stale) key on a transient fetch
		// error rather than failing an otherwise valid request.
		if ok {
			return key, nil
		}
		return nil, err
	}

	v.mu.RLock()
	key, ok = v.keys[kid]
	v.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no signing key for kid %q", kid)
	}
	return key, nil
}

// refresh refetches the JWKS, collapsing concurrent refetches: callers pass the
// fetchedAt they observed before deciding to refresh, and any caller that finds
// the cache has advanced past that point skips its own fetch.
func (v *Verifier) refresh(ctx context.Context, seen time.Time) error {
	v.fetchMu.Lock()
	defer v.fetchMu.Unlock()

	v.mu.RLock()
	advanced := v.fetchedAt.After(seen)
	v.mu.RUnlock()
	if advanced {
		return nil
	}

	keys, err := v.fetchKeys(ctx)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.keys = keys
	v.fetchedAt = time.Now()
	v.mu.Unlock()
	return nil
}

type jwksKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDoc struct {
	Keys []jwksKey `json:"keys"`
}

func (v *Verifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.certsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch JWKS: unexpected status %d", resp.StatusCode)
	}

	var doc jwksDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := parseRSAKey(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, errors.New("JWKS contained no usable RSA keys")
	}
	return keys, nil
}

func parseRSAKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := b64urlDecode(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eBytes, err := b64urlDecode(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if n.Sign() == 0 || !e.IsInt64() || e.Int64() <= 0 || e.Int64() > int64(^uint32(0)) {
		return nil, errors.New("invalid RSA public key parameters")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

// b64urlDecode decodes base64url with or without padding (JWKS values are
// typically unpadded RawURLEncoding, but tolerate padded input too).
func b64urlDecode(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}
