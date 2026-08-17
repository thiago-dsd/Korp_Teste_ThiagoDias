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

// Token signs an access token for a random administrator, which is what most
// handler tests need: they are exercising the handler, not the authorisation.
func (s *Signer) Token(t *testing.T) string {
	t.Helper()
	return s.TokenFor(t, uuid.New(), "admin@example.com")
}

// TokenForRole signs a token for a random user with the given role, for the
// tests that are about what a role may and may not do.
func (s *Signer) TokenForRole(t *testing.T, role string) string {
	t.Helper()
	return s.token(t, uuid.New(), "someone@example.com", role)
}

// TokenFor signs an access token for the given user, as an administrator.
func (s *Signer) TokenFor(t *testing.T, userID uuid.UUID, email string) string {
	t.Helper()
	return s.token(t, userID, email, authn.RoleAdmin)
}

func (s *Signer) token(t *testing.T, userID uuid.UUID, email, role string) string {
	t.Helper()

	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   userID.String(),
		"iss":   authn.Issuer,
		"aud":   authn.Audience,
		"iat":   now.Unix(),
		"exp":   now.Add(15 * time.Minute).Unix(),
		"email": email,
		"name":  "Test User",
		"role":  role,
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
