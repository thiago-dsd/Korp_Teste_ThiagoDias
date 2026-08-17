package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// jwksOf serves a key set for one key, counting how often it is read.
func jwksOf(t *testing.T, key *rsa.PublicKey, keyID string, reads *atomic.Int64) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		fmt.Fprintf(w, `{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":%q,"n":%q,"e":"AQAB"}]}`,
			keyID, base64.RawURLEncoding.EncodeToString(key.N.Bytes()))
	}))
	t.Cleanup(server.Close)
	return server
}

// A signing key that was rotated has to be picked up, or every session issued
// with the new one fails until the cached set happens to expire.
func TestAnUnknownKeyIdCausesTheKeySetToBeReadAgain(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	var reads atomic.Int64
	server := jwksOf(t, &key.PublicKey, "second-key", &reads)

	verifier := NewVerifier(server.URL)
	// A set was already read and does not contain the rotated key.
	verifier.keys = map[string]*rsa.PublicKey{"first-key": &key.PublicKey}
	verifier.fetchedAt = time.Now().Add(-jwksRefetchCooldown - time.Second)

	if _, err := verifier.keyFor(context.Background(), "second-key"); err != nil {
		t.Fatalf("the rotated key was not picked up: %v", err)
	}
	if reads.Load() != 1 {
		t.Errorf("the key set was read %d times, want 1", reads.Load())
	}
}

// Asking every time would let anybody with a made up key id turn this service
// into a load generator against identity.
func TestRepeatedUnknownKeyIdsDoNotHammerIdentity(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	var reads atomic.Int64
	server := jwksOf(t, &key.PublicKey, "real-key", &reads)

	verifier := NewVerifier(server.URL)
	verifier.keys = map[string]*rsa.PublicKey{"real-key": &key.PublicKey}
	verifier.fetchedAt = time.Now()

	for range 20 {
		if _, err := verifier.keyFor(context.Background(), "made-up"); err == nil {
			t.Fatal("a made up key id was accepted")
		}
	}
	if reads.Load() != 0 {
		t.Errorf("the key set was read %d times inside the cooldown, want 0", reads.Load())
	}
}
