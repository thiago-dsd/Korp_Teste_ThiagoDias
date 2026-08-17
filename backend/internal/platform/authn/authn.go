// Package authn verifies the access tokens issued by the identity service.
//
// Verification is asymmetric: services only ever hold the public key, fetched
// from the identity service, so none of them can mint a token.
package authn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"crypto/rsa"
	"encoding/base64"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
)

// Claim values every service requires.
const (
	Issuer   = "korp-identity"
	Audience = "korp-invoices"
)

// jwksRefetchCooldown bounds how often an unknown key id may cause the key set
// to be read again.
//
// Refusing an unknown key without asking would mean a rotated signing key is
// not honoured until the whole set expires, and every session issued with the
// new key fails in the meantime. Asking every time would let anyone with a made
// up key id turn this service into a load generator against identity. Asking at
// most this often does neither.
const jwksRefetchCooldown = 30 * time.Second

// Errors reported to callers.
var (
	// ErrMissingToken reports a request without credentials.
	ErrMissingToken = apperr.Unauthorized("missing_token", "Sign in to continue.")
	// ErrInvalidToken reports credentials that cannot be accepted.
	ErrInvalidToken = apperr.Unauthorized("invalid_token", "Your session is not valid anymore. Please sign in again.")
)

// User is who is behind a request.
type User struct {
	ID    uuid.UUID
	Email string
	Name  string
	// Role is what this person may do. It comes from the signed token, so a
	// caller cannot claim one.
	Role string
}

// Roles understood across the services.
const (
	RoleOperator = "operator"
	RoleAdmin    = "admin"
)

// ErrForbidden reports a caller who is authenticated but not allowed.
var ErrForbidden = apperr.Forbidden("forbidden",
	"Your account is not allowed to perform this operation.")

type contextKey string

const userKey contextKey = "authenticated-user"

// Verifier checks access tokens against the keys of the identity service.
type Verifier struct {
	jwksURL string
	http    *http.Client

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	now       func() time.Time
}

// NewVerifier builds a verifier reading keys from the given JWKS endpoint.
func NewVerifier(jwksURL string) *Verifier {
	return &Verifier{
		jwksURL: jwksURL,
		http:    &http.Client{Timeout: 5 * time.Second},
		keys:    map[string]*rsa.PublicKey{},
		now:     time.Now,
	}
}

// NewVerifierWithKeys builds a verifier over keys already at hand, which is
// what the identity service itself uses.
func NewVerifierWithKeys(keys map[string]*rsa.PublicKey) *Verifier {
	verifier := &Verifier{keys: keys, now: time.Now}
	verifier.fetchedAt = time.Now().Add(100 * 365 * 24 * time.Hour) // never refetched
	return verifier
}

// Verify parses and validates a token, returning who it belongs to.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (User, error) {
	parsed, err := jwt.ParseWithClaims(rawToken, &jwt.RegisteredClaims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
		}
		keyID, _ := token.Header["kid"].(string)
		return v.keyFor(ctx, keyID)
	},
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
	)
	if err != nil {
		return User{}, ErrInvalidToken.WithCause(err)
	}

	claims, ok := parsed.Claims.(*jwt.RegisteredClaims)
	if !ok || claims.Subject == "" {
		return User{}, ErrInvalidToken
	}

	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return User{}, ErrInvalidToken.WithCause(err)
	}

	// Email and name are convenience claims; they are read from the raw token
	// only after the signature has been accepted.
	var extra struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if payload, err := decodePayload(rawToken); err == nil {
		_ = json.Unmarshal(payload, &extra)
	}

	role := extra.Role
	if role == "" {
		// A token issued before roles existed belongs to somebody who could
		// already do everything; treating it as the lesser role is what keeps a
		// rolling deploy from handing out privileges nobody granted.
		role = RoleOperator
	}
	return User{ID: id, Email: extra.Email, Name: extra.Name, Role: role}, nil
}

func (v *Verifier) keyFor(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	key, found := v.keys[keyID]
	recentlyAsked := v.now().Sub(v.fetchedAt) < jwksRefetchCooldown
	v.mu.RUnlock()

	if found {
		return key, nil
	}
	if recentlyAsked && len(v.keys) > 0 {
		// The key set was read moments ago and still does not know this key, so
		// asking again now would only be work. A key rotated since then is
		// picked up as soon as the cooldown passes.
		return nil, fmt.Errorf("unknown signing key %q", keyID)
	}
	if v.jwksURL == "" {
		return nil, fmt.Errorf("unknown signing key %q", keyID)
	}

	if err := v.refresh(ctx); err != nil {
		return nil, err
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	if key, found := v.keys[keyID]; found {
		return key, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", keyID)
}

// refresh reads the key set from the identity service.
func (v *Verifier) refresh(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("build jwks request: %w", err)
	}

	response, err := v.http.Do(request)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks endpoint answered %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read jwks: %w", err)
	}

	keys, err := parseJWKS(body)
	if err != nil {
		return err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys = keys
	v.fetchedAt = v.now()
	return nil
}

func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var document struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(document.Keys))
	for _, entry := range document.Keys {
		if entry.Kty != "RSA" {
			continue
		}
		modulus, err := base64.RawURLEncoding.DecodeString(entry.N)
		if err != nil {
			return nil, fmt.Errorf("decode jwks modulus: %w", err)
		}
		exponent, err := base64.RawURLEncoding.DecodeString(entry.E)
		if err != nil {
			return nil, fmt.Errorf("decode jwks exponent: %w", err)
		}
		keys[entry.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(modulus),
			E: int(new(big.Int).SetBytes(exponent).Int64()),
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("jwks has no usable keys")
	}
	return keys, nil
}

func decodePayload(rawToken string) ([]byte, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("token is not a JWT")
	}
	return base64.RawURLEncoding.DecodeString(parts[1])
}

// RequireUser rejects requests without a valid access token and puts the
// authenticated user in the context of the ones it lets through.
func RequireUser(verifier *Verifier) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawToken, ok := bearerToken(r)
			if !ok {
				httpx.WriteError(w, r, ErrMissingToken)
				return
			}

			user, err := verifier.Verify(r.Context(), rawToken)
			if err != nil {
				httpx.WriteError(w, r, err)
				return
			}

			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
		})
	}
}

// WithUser returns a context carrying the authenticated user.
func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userKey, user)
}

// UserFrom returns the authenticated user of a request.
func UserFrom(ctx context.Context) (User, error) {
	user, ok := ctx.Value(userKey).(User)
	if !ok {
		return User{}, ErrMissingToken
	}
	return user, nil
}

// UserIDFrom returns the id of the authenticated user.
func UserIDFrom(ctx context.Context) (uuid.UUID, error) {
	user, err := UserFrom(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return user.ID, nil
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	prefix := "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

// RequireRole refuses a caller whose role is not among the allowed ones.
//
// It sits inside RequireUser, so by the time it runs the identity is proven and
// the role comes from the signed token rather than from anything the caller
// sent. Enforcing it here rather than asking the identity service keeps the
// services independent: the token carries everything needed to decide.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, role := range roles {
		allowed[role] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := UserFrom(r.Context())
			if err != nil {
				httpx.WriteError(w, r, err)
				return
			}
			if !allowed[user.Role] {
				httpx.WriteError(w, r, ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
