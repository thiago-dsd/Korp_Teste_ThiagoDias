// Package authntest issues access tokens for tests, so a handler test can
// authenticate without running the identity service.
package authntest

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
)

const testKeyID = "test-key"

// Signer issues tokens accepted by the verifier it comes with.
type Signer struct {
	key      *rsa.PrivateKey
	Verifier *authn.Verifier
}

// New builds a signer and the matching verifier.
func New(t *testing.T) *Signer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test signing key: %v", err)
	}

	return &Signer{
		key:      key,
		Verifier: authn.NewVerifierWithKeys(map[string]*rsa.PublicKey{testKeyID: &key.PublicKey}),
	}
}

// Token signs an access token for a random user.
func (s *Signer) Token(t *testing.T) string {
	t.Helper()
	return s.TokenFor(t, uuid.New(), "operator@example.com")
}

// TokenFor signs an access token for the given user.
func (s *Signer) TokenFor(t *testing.T, userID uuid.UUID, email string) string {
	t.Helper()

	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   userID.String(),
		"iss":   authn.Issuer,
		"aud":   authn.Audience,
		"iat":   now.Unix(),
		"exp":   now.Add(15 * time.Minute).Unix(),
		"email": email,
		"name":  "Test Operator",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKeyID

	signed, err := token.SignedString(s.key)
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return signed
}

// Authenticate puts a valid token on a request.
func (s *Signer) Authenticate(t *testing.T, request *http.Request) {
	t.Helper()
	request.Header.Set("Authorization", "Bearer "+s.Token(t))
}
