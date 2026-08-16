package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// UserRepository is the persistence the service depends on.
type UserRepository interface {
	LoginBlockedUntil(ctx context.Context, email string) (time.Time, error)
	RegisterFailedLogin(ctx context.Context, email string, policy LockoutPolicy) error
	ClearLoginFailures(ctx context.Context, email string) error
	CreateUser(ctx context.Context, registration Registration, passwordHash string) (User, error)
	FindUserByEmail(ctx context.Context, email string) (User, error)
	FindUserByID(ctx context.Context, id uuid.UUID) (User, error)
	DeleteUser(ctx context.Context, id uuid.UUID) error
	StoreRefreshToken(ctx context.Context, userID, familyID uuid.UUID, hash string, expiresAt time.Time, userAgent string) error
	RotateRefreshToken(ctx context.Context, presentedHash, newHash string, expiresAt time.Time, userAgent string) (User, uuid.UUID, error)
	RevokeRefreshToken(ctx context.Context, presentedHash string) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// Service holds the identity use cases.
type Service struct {
	users   UserRepository
	issuer  *TokenIssuer
	lockout LockoutPolicy
	now     func() time.Time
}

// NewService builds an identity service.
func NewService(users UserRepository, issuer *TokenIssuer) *Service {
	return &Service{users: users, issuer: issuer, lockout: DefaultLockoutPolicy(), now: time.Now}
}

// WithLockoutPolicy replaces how tolerant sign in is of repeated failures.
func (s *Service) WithLockoutPolicy(policy LockoutPolicy) *Service {
	if policy.MaxFailures > 0 && policy.Lockout > 0 {
		s.lockout = policy
	}
	return s
}

// Register creates an account and signs the person in.
func (s *Service) Register(ctx context.Context, email, name, password, userAgent string) (User, TokenPair, error) {
	registration, err := NewRegistration(email, name, password)
	if err != nil {
		return User{}, TokenPair{}, err
	}

	passwordHash, err := HashPassword(registration.Password)
	if err != nil {
		return User{}, TokenPair{}, err
	}

	user, err := s.users.CreateUser(ctx, registration, passwordHash)
	if err != nil {
		return User{}, TokenPair{}, err
	}

	tokens, err := s.startSession(ctx, user, userAgent)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	return user, tokens, nil
}

// Login checks the credentials and starts a session.
//
// A missing account and a wrong password give the same answer, and the
// password is verified even when the account does not exist, so the time the
// endpoint takes does not reveal which addresses are registered.
func (s *Service) Login(ctx context.Context, email, password, userAgent string) (User, TokenPair, error) {
	// An account under attack is closed to everyone for a while, including the
	// attacker. The check happens before the password is even looked at, and
	// applies to addresses without an account too, so a locked answer never
	// reveals which addresses are registered.
	blockedUntil, err := s.users.LoginBlockedUntil(ctx, email)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	if !blockedUntil.IsZero() {
		return User{}, TokenPair{}, lockedError(blockedUntil)
	}

	user, err := s.users.FindUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			_, _ = VerifyPassword(password, decoyHash)
			if err := s.users.RegisterFailedLogin(ctx, email, s.lockout); err != nil {
				return User{}, TokenPair{}, err
			}
			return User{}, TokenPair{}, ErrInvalidCredentials
		}
		return User{}, TokenPair{}, err
	}

	matches, err := VerifyPassword(password, user.PasswordHash)
	if err != nil {
		return User{}, TokenPair{}, fmt.Errorf("verify password: %w", err)
	}
	if !matches {
		if err := s.users.RegisterFailedLogin(ctx, email, s.lockout); err != nil {
			return User{}, TokenPair{}, err
		}
		return User{}, TokenPair{}, ErrInvalidCredentials
	}

	// A successful sign in clears the count, so an ordinary typo never adds up
	// to a lockout over the course of a day.
	if err := s.users.ClearLoginFailures(ctx, user.Email); err != nil {
		return User{}, TokenPair{}, err
	}

	tokens, err := s.startSession(ctx, user, userAgent)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	return user, tokens, nil
}

// Refresh exchanges a refresh token for a new pair, rotating it.
func (s *Service) Refresh(ctx context.Context, refreshToken, userAgent string) (User, TokenPair, error) {
	if refreshToken == "" {
		return User{}, TokenPair{}, ErrInvalidToken
	}

	newToken, newHash, err := NewRefreshToken()
	if err != nil {
		return User{}, TokenPair{}, err
	}

	expiresAt := s.now().UTC().Add(RefreshTokenTTL)
	user, _, err := s.users.RotateRefreshToken(ctx, HashRefreshToken(refreshToken), newHash, expiresAt, userAgent)
	if err != nil {
		return User{}, TokenPair{}, err
	}

	accessToken, expiresIn, err := s.issuer.IssueAccessToken(user)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	return user, TokenPair{AccessToken: accessToken, RefreshToken: newToken, ExpiresIn: expiresIn}, nil
}

// Logout ends the session the token belongs to.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return s.users.RevokeRefreshToken(ctx, HashRefreshToken(refreshToken))
}

// Profile returns the account of the signed in person.
func (s *Service) Profile(ctx context.Context, userID uuid.UUID) (User, error) {
	return s.users.FindUserByID(ctx, userID)
}

// DeleteAccount removes the account after confirming the password, and ends
// every session it had.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID, password string) error {
	user, err := s.users.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}

	matches, err := VerifyPassword(password, user.PasswordHash)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !matches {
		return ErrInvalidCredentials
	}

	if err := s.users.RevokeAllForUser(ctx, user.ID); err != nil {
		return err
	}
	return s.users.DeleteUser(ctx, user.ID)
}

func (s *Service) startSession(ctx context.Context, user User, userAgent string) (TokenPair, error) {
	accessToken, expiresIn, err := s.issuer.IssueAccessToken(user)
	if err != nil {
		return TokenPair{}, err
	}

	refreshToken, hash, err := NewRefreshToken()
	if err != nil {
		return TokenPair{}, err
	}

	// A new sign in starts its own family, so revoking one session never
	// touches the others.
	familyID := uuid.New()
	expiresAt := s.now().UTC().Add(RefreshTokenTTL)
	if err := s.users.StoreRefreshToken(ctx, user.ID, familyID, hash, expiresAt, userAgent); err != nil {
		return TokenPair{}, err
	}

	return TokenPair{AccessToken: accessToken, RefreshToken: refreshToken, ExpiresIn: expiresIn}, nil
}

// decoyHash is a valid argon2id hash of a random value. Verifying against it
// costs the same as a real check, which keeps sign in attempts against unknown
// accounts indistinguishable from attempts against real ones.
const decoyHash = "$argon2id$v=19$m=19456,t=1,p=1$Y2FudGd1ZXNzdGhpc3NhbHQ$Z8kCk4tS/YQCKTMcCVfN0oUTKz/1TP/2fUohvVKA6MU"
