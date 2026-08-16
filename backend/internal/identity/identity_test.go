package identity

import (
	"strings"
	"testing"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

func TestNewRegistrationNormalizesInput(t *testing.T) {
	registration, err := NewRegistration("  Operator@Example.COM ", "  Ada   Lovelace ", "correct horse battery staple")
	if err != nil {
		t.Fatalf("NewRegistration() returned error: %v", err)
	}

	if registration.Email != "operator@example.com" {
		t.Errorf("Email = %q, want it lowercased and trimmed", registration.Email)
	}
	if registration.Name != "Ada Lovelace" {
		t.Errorf("Name = %q, want %q", registration.Name, "Ada Lovelace")
	}
}

func TestNewRegistrationRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		email     string
		userName  string
		password  string
		wantField string
	}{
		{name: "empty email", email: "", userName: "Ada", password: "correct horse battery", wantField: "email"},
		{name: "malformed email", email: "not-an-email", userName: "Ada", password: "correct horse battery", wantField: "email"},
		{name: "email with display name", email: "Ada <ada@example.com>", userName: "Ada", password: "correct horse battery", wantField: "email"},
		{name: "empty name", email: "ada@example.com", userName: "  ", password: "correct horse battery", wantField: "name"},
		{name: "short password", email: "ada@example.com", userName: "Ada", password: "short", wantField: "password"},
		{
			name:      "very long password",
			email:     "ada@example.com",
			userName:  "Ada",
			password:  strings.Repeat("x", MaxPasswordLength+1),
			wantField: "password",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRegistration(tc.email, tc.userName, tc.password)
			if err == nil {
				t.Fatalf("NewRegistration() returned no error, want one for %q", tc.wantField)
			}
			if _, reported := apperr.From(err).Details[tc.wantField]; !reported {
				t.Errorf("details = %v, want a message for %q", apperr.From(err).Details, tc.wantField)
			}
		})
	}
}

func TestHashPasswordProducesVerifiableHashes(t *testing.T) {
	const password = "correct horse battery staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() returned error: %v", err)
	}

	if strings.Contains(hash, password) {
		t.Fatal("the hash contains the password itself")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash = %q, want an argon2id hash", hash)
	}

	matches, err := VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("VerifyPassword() returned error: %v", err)
	}
	if !matches {
		t.Error("VerifyPassword() = false for the right password, want true")
	}

	matches, err = VerifyPassword("another password entirely", hash)
	if err != nil {
		t.Fatalf("VerifyPassword() returned error: %v", err)
	}
	if matches {
		t.Error("VerifyPassword() = true for a wrong password, want false")
	}
}

func TestHashPasswordUsesAFreshSaltEveryTime(t *testing.T) {
	first, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() returned error: %v", err)
	}
	second, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() returned error: %v", err)
	}

	if first == second {
		t.Error("the same password produced the same hash twice, want a random salt each time")
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	for _, hash := range []string{"", "plain-text", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", "$argon2id$v=999$m=1,t=1,p=1$c2FsdA$aGFzaA"} {
		if _, err := VerifyPassword("password", hash); err == nil {
			t.Errorf("VerifyPassword() accepted the hash %q, want an error", hash)
		}
	}
}

func TestDecoyHashIsUsable(t *testing.T) {
	// Login verifies against this hash when the account does not exist, so it
	// has to be a valid one or the timing defence would not run.
	matches, err := VerifyPassword("whatever", decoyHash)
	if err != nil {
		t.Fatalf("VerifyPassword() on the decoy hash returned error: %v", err)
	}
	if matches {
		t.Error("the decoy hash matched a password, want no match")
	}
}
