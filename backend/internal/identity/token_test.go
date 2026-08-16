package identity_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/identity"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
)

func testUser() identity.User {
	return identity.User{ID: uuid.New(), Email: "ada@example.com", Name: "Ada Lovelace"}
}

// verifierFor builds a verifier over the public key of the given issuer, the
// same way the stock and billing services do after reading the JWKS.
func verifierFor(t *testing.T, issuer *identity.TokenIssuer, key *rsa.PrivateKey) *authn.Verifier {
	t.Helper()

	keys := map[string]*rsa.PublicKey{}
	for _, entry := range issuer.PublicJWKS()["keys"].([]map[string]string) {
		keys[entry["kid"]] = &key.PublicKey
	}
	return authn.NewVerifierWithKeys(keys)
}

func TestAccessTokenIsAcceptedByTheOtherServices(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuer := identity.NewTokenIssuer(key)
	user := testUser()

	token, expiresIn, err := issuer.IssueAccessToken(user)
	if err != nil {
		t.Fatalf("IssueAccessToken() returned error: %v", err)
	}
	if expiresIn != identity.AccessTokenTTL {
		t.Errorf("expiresIn = %v, want %v", expiresIn, identity.AccessTokenTTL)
	}
	if strings.Count(token, ".") != 2 {
		t.Fatalf("token = %q, want a JWT", token)
	}

	verified, err := verifierFor(t, issuer, key).Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify() returned error: %v", err)
	}
	if verified.ID != user.ID {
		t.Errorf("subject = %v, want %v", verified.ID, user.ID)
	}
	if verified.Email != user.Email || verified.Name != user.Name {
		t.Errorf("verified user = %+v, want the issued one", verified)
	}
}

func TestTokenSignedByAnotherKeyIsRefused(t *testing.T) {
	trusted, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	attacker, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	trustedIssuer := identity.NewTokenIssuer(trusted)
	forged, _, err := identity.NewTokenIssuer(attacker).IssueAccessToken(testUser())
	if err != nil {
		t.Fatalf("IssueAccessToken() returned error: %v", err)
	}

	if _, err := verifierFor(t, trustedIssuer, trusted).Verify(context.Background(), forged); err == nil {
		t.Fatal("a token signed by another key was accepted, want it refused")
	}
}

func TestTamperedTokenIsRefused(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuer := identity.NewTokenIssuer(key)
	verifier := verifierFor(t, issuer, key)

	token, _, err := issuer.IssueAccessToken(testUser())
	if err != nil {
		t.Fatalf("IssueAccessToken() returned error: %v", err)
	}

	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + parts[1] + "x." + parts[2]

	if _, err := verifier.Verify(context.Background(), tampered); err == nil {
		t.Error("a tampered token was accepted, want it refused")
	}
	if _, err := verifier.Verify(context.Background(), "not-a-token"); err == nil {
		t.Error("a malformed token was accepted, want it refused")
	}
	if _, err := verifier.Verify(context.Background(), ""); err == nil {
		t.Error("an empty token was accepted, want it refused")
	}
}

func TestPublicJWKSDescribesTheSigningKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	jwks := identity.NewTokenIssuer(key).PublicJWKS()
	entries, ok := jwks["keys"].([]map[string]string)
	if !ok || len(entries) != 1 {
		t.Fatalf("jwks = %v, want a single key", jwks)
	}

	entry := entries[0]
	if entry["kty"] != "RSA" || entry["alg"] != "RS256" || entry["use"] != "sig" {
		t.Errorf("key = %v, want an RSA signing key", entry)
	}
	if entry["kid"] == "" || entry["n"] == "" || entry["e"] == "" {
		t.Errorf("key = %v, want kid, n and e filled", entry)
	}
	// The private exponent must never be published.
	for _, value := range entry {
		if strings.Contains(value, key.D.String()) {
			t.Fatal("the JWKS exposes the private key")
		}
	}
}

func TestLoadOrGeneratePrivateKey(t *testing.T) {
	generated, wasGenerated, err := identity.LoadOrGeneratePrivateKey("")
	if err != nil {
		t.Fatalf("LoadOrGeneratePrivateKey() returned error: %v", err)
	}
	if !wasGenerated || generated == nil {
		t.Error("no key was generated for an empty configuration")
	}

	encoded := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: mustMarshalPKCS8(t, generated),
	})

	loaded, wasGenerated, err := identity.LoadOrGeneratePrivateKey(string(encoded))
	if err != nil {
		t.Fatalf("LoadOrGeneratePrivateKey() returned error: %v", err)
	}
	if wasGenerated {
		t.Error("a configured key was reported as generated")
	}
	if loaded.N.Cmp(generated.N) != 0 {
		t.Error("the loaded key is not the configured one")
	}

	if _, _, err := identity.LoadOrGeneratePrivateKey("not a pem"); err == nil {
		t.Error("LoadOrGeneratePrivateKey() accepted a malformed key, want an error")
	}
}

func TestRefreshTokensAreRandomAndStoredHashed(t *testing.T) {
	first, firstHash, err := identity.NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken() returned error: %v", err)
	}
	second, secondHash, err := identity.NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken() returned error: %v", err)
	}

	if first == second {
		t.Error("two refresh tokens came out equal, want them random")
	}
	if firstHash == secondHash {
		t.Error("two refresh tokens produced the same hash")
	}
	if strings.Contains(firstHash, first) {
		t.Error("the stored hash contains the token itself")
	}
	if identity.HashRefreshToken(first) != firstHash {
		t.Error("hashing the same token twice gave different results")
	}
}

func TestAccessTokenLifetimeIsShort(t *testing.T) {
	if identity.AccessTokenTTL > 30*time.Minute {
		t.Errorf("AccessTokenTTL = %v, want a short lived token", identity.AccessTokenTTL)
	}
	if identity.RefreshTokenTTL < 24*time.Hour {
		t.Errorf("RefreshTokenTTL = %v, want it to survive a working day", identity.RefreshTokenTTL)
	}
}

func mustMarshalPKCS8(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()

	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return encoded
}
