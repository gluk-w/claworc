package cfaccess

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testAUD    = "test-aud-tag"
	testIssuer = "https://team.cloudflareaccess.com"
)

// jwksServer serves a JWKS document for the given public keys (by kid) and
// counts how many times it was hit.
type jwksServer struct {
	*httptest.Server
	hits atomic.Int64
}

func newJWKSServer(t *testing.T, keys map[string]*rsa.PublicKey) *jwksServer {
	t.Helper()
	js := &jwksServer{}
	js.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		js.hits.Add(1)
		var doc jwksDoc
		for kid, pub := range keys {
			doc.Keys = append(doc.Keys, jwksKey{
				Kid: kid,
				Kty: "RSA",
				N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(js.Close)
	return js
}

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}

func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"aud":   testAUD,
		"iss":   testIssuer,
		"email": "User@Example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Add(-time.Minute).Unix(),
	}
}

func TestVerify_Valid(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	v := NewVerifier(srv.URL, testAUD, testIssuer)

	token := signToken(t, key, "kid-1", validClaims())
	email, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if email != "user@example.com" {
		t.Fatalf("email = %q, want normalized user@example.com", email)
	}
}

func TestVerify_WrongAudience(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	v := NewVerifier(srv.URL, testAUD, testIssuer)

	c := validClaims()
	c["aud"] = "other-aud"
	if _, err := v.Verify(context.Background(), signToken(t, key, "kid-1", c)); err == nil {
		t.Fatal("expected error for wrong audience")
	}
}

func TestVerify_WrongIssuer(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	v := NewVerifier(srv.URL, testAUD, testIssuer)

	c := validClaims()
	c["iss"] = "https://evil.example.com"
	if _, err := v.Verify(context.Background(), signToken(t, key, "kid-1", c)); err == nil {
		t.Fatal("expected error for wrong issuer")
	}
}

func TestVerify_Expired(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	v := NewVerifier(srv.URL, testAUD, testIssuer)

	c := validClaims()
	c["exp"] = time.Now().Add(-time.Hour).Unix()
	if _, err := v.Verify(context.Background(), signToken(t, key, "kid-1", c)); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVerify_MissingExpiration(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	v := NewVerifier(srv.URL, testAUD, testIssuer)

	c := validClaims()
	delete(c, "exp")
	if _, err := v.Verify(context.Background(), signToken(t, key, "kid-1", c)); err == nil {
		t.Fatal("expected error for missing exp")
	}
}

func TestVerify_BadSignature(t *testing.T) {
	key := newKey(t)
	other := newKey(t)
	// JWKS serves the legitimate key, but the token is signed by `other`.
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	v := NewVerifier(srv.URL, testAUD, testIssuer)

	if _, err := v.Verify(context.Background(), signToken(t, other, "kid-1", validClaims())); err == nil {
		t.Fatal("expected error for bad signature")
	}
}

func TestVerify_NonRSAlgRejected(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	v := NewVerifier(srv.URL, testAUD, testIssuer)

	// HS256 token using the modulus bytes as the HMAC secret — an alg-confusion
	// attempt. Must be rejected because only RS256 is accepted.
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims())
	tok.Header["kid"] = "kid-1"
	s, err := tok.SignedString(key.PublicKey.N.Bytes())
	if err != nil {
		t.Fatalf("sign HS256: %v", err)
	}
	if _, err := v.Verify(context.Background(), s); err == nil {
		t.Fatal("expected error for non-RS256 algorithm")
	}
}

func TestVerify_MissingEmail(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	v := NewVerifier(srv.URL, testAUD, testIssuer)

	c := validClaims()
	delete(c, "email")
	if _, err := v.Verify(context.Background(), signToken(t, key, "kid-1", c)); err == nil {
		t.Fatal("expected error for missing email claim")
	}
}

func TestVerify_UnknownKidRefetchesOnce(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, map[string]*rsa.PublicKey{"kid-1": &key.PublicKey})
	v := NewVerifier(srv.URL, testAUD, testIssuer)

	// First verification populates the cache (1 fetch).
	if _, err := v.Verify(context.Background(), signToken(t, key, "kid-1", validClaims())); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if got := srv.hits.Load(); got != 1 {
		t.Fatalf("hits after first verify = %d, want 1", got)
	}

	// A token with an unknown kid forces exactly one refetch, then fails.
	if _, err := v.Verify(context.Background(), signToken(t, key, "kid-unknown", validClaims())); err == nil {
		t.Fatal("expected error for unknown kid")
	}
	if got := srv.hits.Load(); got != 2 {
		t.Fatalf("hits after unknown-kid verify = %d, want 2", got)
	}
}

func TestVerify_ConcurrentUnknownKidSingleFetch(t *testing.T) {
	key := newKey(t)
	// Slow server so concurrent goroutines pile up on the same refetch.
	js := &jwksServer{}
	js.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		js.hits.Add(1)
		time.Sleep(50 * time.Millisecond)
		doc := jwksDoc{Keys: []jwksKey{{
			Kid: "kid-1",
			Kty: "RSA",
			N:   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(doc)
	}))
	defer js.Close()

	v := NewVerifier(js.URL, testAUD, testIssuer)
	token := signToken(t, key, "kid-1", validClaims())

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v.Verify(context.Background(), token)
		}()
	}
	wg.Wait()

	if got := js.hits.Load(); got != 1 {
		t.Fatalf("concurrent cold-cache verifies triggered %d fetches, want 1", got)
	}
}
