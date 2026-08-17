package identity

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Token lifetimes. Access tokens are short lived because they cannot be
// revoked; refresh tokens live longer but are rotated on every use and can be
// revoked at any moment.
const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 7 * 24 * time.Hour
	// ReuseGracePeriod is how long after a refresh token is exchanged that
	// presenting it again still counts as a race rather than as a replay.
	//
	// It exists because two tabs of one browser share a token and refresh at
	// the same instant; without it the loser of that race would end the
	// session of somebody who did nothing wrong. The window is deliberately
	// short: it is the one moment an attacker could replay a stolen token
	// unnoticed, and that is the price of not signing honest people out.
	ReuseGracePeriod = 15 * time.Second
)

// Issuer is the value of the `iss` claim, and Audience the `aud` claim the
// other services require.
const (
	Issuer   = "korp-identity"
	Audience = "korp-invoices"
)

// TokenPair is what a successful sign in gives back.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
}

// Claims carried by an access token.
type Claims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
	Name  string `json:"name"`
	// Role travels in the token so the other services can enforce it without
	// asking identity on every request, and without reaching into its database.
	Role string `json:"role"`
}

// TokenIssuer signs access tokens and publishes the key that verifies them.
//
// Signing is asymmetric on purpose: the stock and billing services only ever
// hold the public key, so a leak on their side cannot be used to mint tokens.
type TokenIssuer struct {
	privateKey *rsa.PrivateKey
	keyID      string
	now        func() time.Time
}

// NewTokenIssuer builds an issuer for the given RSA key.
func NewTokenIssuer(privateKey *rsa.PrivateKey) *TokenIssuer {
	return &TokenIssuer{
		privateKey: privateKey,
		keyID:      keyIDOf(&privateKey.PublicKey),
		now:        time.Now,
	}
}

// LoadOrGeneratePrivateKey reads a PEM encoded RSA key, generating a temporary
// one when none is configured. A generated key only lives while the process
// does, which is fine for development and reported by the returned flag.
func LoadOrGeneratePrivateKey(pemKey string) (key *rsa.PrivateKey, generated bool, err error) {
	if pemKey == "" {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, false, fmt.Errorf("generate signing key: %w", err)
		}
		return key, true, nil
	}

	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, false, fmt.Errorf("signing key is not valid PEM")
	}

	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, false, fmt.Errorf("signing key is not an RSA key")
		}
		return rsaKey, false, nil
	}

	rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, false, fmt.Errorf("parse signing key: %w", err)
	}
	return rsaKey, false, nil
}

// IssueAccessToken signs a token for the given user.
func (i *TokenIssuer) IssueAccessToken(user User) (string, time.Duration, error) {
	issuedAt := i.now().UTC()
	expiresAt := issuedAt.Add(AccessTokenTTL)

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			NotBefore: jwt.NewNumericDate(issuedAt),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			ID:        uuid.NewString(),
		},
		Email: user.Email,
		Name:  user.Name,
		Role:  string(user.Role),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = i.keyID

	signed, err := token.SignedString(i.privateKey)
	if err != nil {
		return "", 0, fmt.Errorf("sign access token: %w", err)
	}
	return signed, AccessTokenTTL, nil
}

// PublicJWKS returns the key set the other services use to verify tokens.
func (i *TokenIssuer) PublicJWKS() map[string]any {
	public := i.privateKey.PublicKey

	return map[string]any{
		"keys": []map[string]string{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": i.keyID,
				"n":   base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(bigEndian(public.E)),
			},
		},
	}
}

// NewRefreshToken returns an opaque token and the hash stored alongside it.
// Only the hash is persisted, so a dump of the table cannot be replayed.
func NewRefreshToken() (token string, hash string, err error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}

	token = base64.RawURLEncoding.EncodeToString(buffer)
	return token, HashRefreshToken(token), nil
}

// HashRefreshToken hashes an opaque refresh token for storage and lookup.
func HashRefreshToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// keyIDOf derives a stable identifier from the public key, so a rotated key
// produces tokens the services can tell apart.
func keyIDOf(public *rsa.PublicKey) string {
	digest := sha256.Sum256(append(public.N.Bytes(), bigEndian(public.E)...))
	return base64.RawURLEncoding.EncodeToString(digest[:8])
}

func bigEndian(value int) []byte {
	bytes := make([]byte, 0, 4)
	for shift := 24; shift >= 0; shift -= 8 {
		part := byte(value >> shift)
		if len(bytes) == 0 && part == 0 {
			continue
		}
		bytes = append(bytes, part)
	}
	if len(bytes) == 0 {
		return []byte{0}
	}
	return bytes
}
