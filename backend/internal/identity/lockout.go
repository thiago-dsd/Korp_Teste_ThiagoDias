package identity

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

// Defaults for the account lockout.
const (
	DefaultMaxLoginFailures = 10
	DefaultLoginLockout     = 15 * time.Minute
	// loginFailureWindow is how long failures are remembered when they stop.
	loginFailureWindow = 15 * time.Minute
)

// ErrTooManyAttempts reports an account that is locked after repeated failures.
//
// It is returned for addresses that have no account as well, so the answer
// cannot be used to find out which addresses are registered.
var ErrTooManyAttempts = apperr.New(apperr.KindTooManyRequests, "too_many_attempts",
	"Too many sign in attempts for this account. Please wait before trying again.")

// LockoutPolicy is how tolerant sign in is of failures.
type LockoutPolicy struct {
	MaxFailures int
	Lockout     time.Duration
}

// DefaultLockoutPolicy returns the policy used when nothing is configured.
func DefaultLockoutPolicy() LockoutPolicy {
	return LockoutPolicy{MaxFailures: DefaultMaxLoginFailures, Lockout: DefaultLoginLockout}
}

// LoginBlockedUntil reports when the account may be tried again, or the zero
// time when it is not blocked.
func (s *Store) LoginBlockedUntil(ctx context.Context, email string) (time.Time, error) {
	var blockedUntil *time.Time

	err := s.pool.QueryRow(ctx,
		`SELECT blocked_until FROM login_attempts WHERE email = $1`, NormalizeEmail(email)).Scan(&blockedUntil)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return time.Time{}, nil
	case err != nil:
		return time.Time{}, fmt.Errorf("read login attempts: %w", err)
	case blockedUntil == nil || blockedUntil.Before(time.Now()):
		return time.Time{}, nil
	}
	return *blockedUntil, nil
}

// RegisterFailedLogin counts one failure and locks the account once there have
// been too many. Counting happens in the database, so instances share it.
func (s *Store) RegisterFailedLogin(ctx context.Context, email string, policy LockoutPolicy) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO login_attempts (email, failures, first_failure, last_failure, blocked_until)
		VALUES ($1, 1, now(), now(),
			-- A policy that tolerates a single failure locks on this very row.
			CASE WHEN 1 >= $2 THEN now() + $4::interval END)
		ON CONFLICT (email) DO UPDATE SET
			-- Failures that stopped long ago do not count towards a lockout.
			failures = CASE
				WHEN login_attempts.first_failure < now() - $3::interval THEN 1
				ELSE login_attempts.failures + 1
			END,
			first_failure = CASE
				WHEN login_attempts.first_failure < now() - $3::interval THEN now()
				ELSE login_attempts.first_failure
			END,
			last_failure = now(),
			blocked_until = CASE
				WHEN login_attempts.failures + 1 >= $2 THEN now() + $4::interval
				ELSE login_attempts.blocked_until
			END`,
		NormalizeEmail(email), policy.MaxFailures, loginFailureWindow.String(), policy.Lockout.String())
	if err != nil {
		return fmt.Errorf("record failed login: %w", err)
	}
	return nil
}

// ClearLoginFailures forgets the failures of an account that just signed in.
func (s *Store) ClearLoginFailures(ctx context.Context, email string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM login_attempts WHERE email = $1`, NormalizeEmail(email)); err != nil {
		return fmt.Errorf("clear login attempts: %w", err)
	}
	return nil
}

// lockedError builds the answer for a locked account, telling the caller how
// long to wait without saying anything about the account itself.
func lockedError(until time.Time) error {
	wait := max(int(time.Until(until).Seconds()), 1)
	return ErrTooManyAttempts.WithDetails(map[string]string{"retry_after_seconds": strconv.Itoa(wait)})
}
