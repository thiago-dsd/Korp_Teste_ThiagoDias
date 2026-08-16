// Package identity implements the identity service: accounts, sign in and the
// tokens the other services trust.
package identity

import (
	"net/mail"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

// Limits applied to account data.
const (
	MaxEmailLength    = 254
	MaxNameLength     = 120
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

// User is an account able to sign in.
type User struct {
	ID           uuid.UUID
	Email        string
	Name         string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Registration is validated account data, ready to be stored.
type Registration struct {
	Email    string
	Name     string
	Password string
}

// NewRegistration validates and normalizes the data of a new account.
func NewRegistration(email, name, password string) (Registration, error) {
	details := map[string]string{}

	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		details["email"] = err.Error()
	}
	normalizedName, err := normalizeName(name)
	if err != nil {
		details["name"] = err.Error()
	}
	if err := validatePassword(password); err != nil {
		details["password"] = err.Error()
	}

	if len(details) > 0 {
		return Registration{}, ErrInvalidAccount.WithDetails(details)
	}
	return Registration{Email: normalizedEmail, Name: normalizedName, Password: password}, nil
}

// NormalizeEmail prepares an address for lookups, so "A@B.com" and "a@b.com"
// are the same account.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeEmail(email string) (string, error) {
	email = NormalizeEmail(email)
	switch {
	case email == "":
		return "", errText("must not be empty")
	case len(email) > MaxEmailLength:
		return "", errText("is too long")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", errText("must be a valid email address")
	}
	return email, nil
}

func normalizeName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	switch {
	case name == "":
		return "", errText("must not be empty")
	case len([]rune(name)) > MaxNameLength:
		return "", errText("is too long")
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return "", errText("must not contain control characters")
		}
	}
	return name, nil
}

// validatePassword asks for length above anything else: a long passphrase
// beats a short password full of symbols, and length is what resists guessing.
func validatePassword(password string) error {
	switch {
	case len(password) < MinPasswordLength:
		return errText("must have at least 12 characters")
	case len(password) > MaxPasswordLength:
		return errText("must have at most 128 characters")
	case strings.TrimSpace(password) == "":
		return errText("must not be blank")
	}
	return nil
}

// Errors returned by the identity domain.
var (
	// ErrInvalidAccount reports validation failures on account data.
	ErrInvalidAccount = apperr.Invalid("invalid_account", "Account data is invalid.")
	// ErrEmailTaken reports an address that already has an account. It is only
	// used on registration, where hiding it would prevent anyone from signing up.
	ErrEmailTaken = apperr.Conflict("email_taken", "This email address is already registered.")
	// ErrInvalidCredentials is the single answer to a wrong email or a wrong
	// password, so the endpoint never reveals which accounts exist.
	ErrInvalidCredentials = apperr.Unauthorized("invalid_credentials", "Email or password is incorrect.")
	// ErrUserNotFound reports a missing account.
	ErrUserNotFound = apperr.NotFound("user_not_found", "Account was not found.")
	// ErrInvalidToken reports a refresh token that cannot be accepted.
	ErrInvalidToken = apperr.Unauthorized("invalid_refresh_token", "Your session has expired. Please sign in again.")
)

// errText is a small error carrying a field level message.
type errText string

func (e errText) Error() string { return string(e) }
