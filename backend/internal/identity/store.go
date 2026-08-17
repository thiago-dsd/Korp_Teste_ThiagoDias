package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const uniqueViolation = "23505"

// Store persists accounts and refresh tokens in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds an identity store on top of the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const userColumns = `id, email, name, password_hash, created_at, updated_at`

// CreateUser stores a new account, reporting a repeated address as ErrEmailTaken.
func (s *Store) CreateUser(ctx context.Context, registration Registration, passwordHash string) (User, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ($1, $2, $3)
		RETURNING `+userColumns,
		registration.Email, registration.Name, passwordHash)

	user, err := scanUser(row)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrEmailTaken.WithCause(err)
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return user, nil
}

// FindUserByEmail returns the account with the given address.
func (s *Store) FindUserByEmail(ctx context.Context, email string) (User, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE lower(email) = lower($1)`, NormalizeEmail(email))

	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("select user by email: %w", err)
	}
	return user, nil
}

// FindUserByID returns a single account.
func (s *Store) FindUserByID(ctx context.Context, id uuid.UUID) (User, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)

	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("select user by id: %w", err)
	}
	return user, nil
}

// DeleteUser removes an account. Its refresh tokens go with it, so every
// session of the account stops working immediately.
func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// RefreshToken is a stored refresh token.
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
}

// StoreRefreshToken saves the hash of a freshly issued refresh token.
func (s *Store) StoreRefreshToken(ctx context.Context, userID, familyID uuid.UUID, hash string, expiresAt time.Time, userAgent string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at, user_agent)
		VALUES ($1, $2, $3, $4, $5)`, userID, familyID, hash, expiresAt, userAgent)
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

// RotateRefreshToken exchanges a refresh token for a new one in the same
// family. It is the heart of the session handling:
//
//   - an unknown or expired token is refused;
//   - a token that was already exchanged means someone is replaying an old
//     one, so the whole family is revoked and nobody keeps the session;
//   - anything else is marked as used and replaced.
func (s *Store) RotateRefreshToken(ctx context.Context, presentedHash, newHash string, expiresAt time.Time, userAgent string) (User, uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, uuid.Nil, fmt.Errorf("begin refresh transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var stored RefreshToken
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, family_id, expires_at, used_at, revoked_at
		FROM refresh_tokens
		WHERE token_hash = $1
		FOR UPDATE`, presentedHash).
		Scan(&stored.ID, &stored.UserID, &stored.FamilyID, &stored.ExpiresAt, &stored.UsedAt, &stored.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, uuid.Nil, ErrInvalidToken
		}
		return User{}, uuid.Nil, fmt.Errorf("select refresh token: %w", err)
	}

	if stored.UsedAt != nil {
		// Two tabs of the same browser share one refresh token and refresh the
		// moment the access token expires, so the loser of that race presents a
		// token that was exchanged a fraction of a second ago. That is not an
		// attack, and ending the session over it signs an honest person out.
		//
		// Inside the window the answer is a plain invalid token: the client
		// reads the token the winner stored and carries on. Outside it, a token
		// that resurfaces long after being spent is treated as stolen.
		if stored.RevokedAt == nil && time.Since(*stored.UsedAt) <= ReuseGracePeriod {
			return User{}, uuid.Nil, ErrInvalidToken.WithDetails(map[string]string{
				"reason": "token_already_rotated",
			})
		}

		// The token was already exchanged: either it leaked or a client is
		// replaying it. Ending the whole family is the safe answer.
		if err := revokeFamily(ctx, tx, stored.FamilyID); err != nil {
			return User{}, uuid.Nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return User{}, uuid.Nil, fmt.Errorf("commit family revocation: %w", err)
		}
		return User{}, uuid.Nil, ErrInvalidToken.WithDetails(map[string]string{"reason": "token_reuse_detected"})
	}
	if stored.RevokedAt != nil || stored.ExpiresAt.Before(time.Now()) {
		return User{}, uuid.Nil, ErrInvalidToken
	}

	if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET used_at = now() WHERE id = $1`, stored.ID); err != nil {
		return User{}, uuid.Nil, fmt.Errorf("mark refresh token as used: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at, user_agent)
		VALUES ($1, $2, $3, $4, $5)`,
		stored.UserID, stored.FamilyID, newHash, expiresAt, userAgent); err != nil {
		return User{}, uuid.Nil, fmt.Errorf("insert rotated refresh token: %w", err)
	}

	user, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, stored.UserID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, uuid.Nil, ErrInvalidToken
		}
		return User{}, uuid.Nil, fmt.Errorf("select user of refresh token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return User{}, uuid.Nil, fmt.Errorf("commit refresh rotation: %w", err)
	}
	return user, stored.FamilyID, nil
}

// RevokeRefreshToken ends the session a token belongs to. Signing out on one
// device therefore ends that session and not the others.
func (s *Store) RevokeRefreshToken(ctx context.Context, presentedHash string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = now()
		WHERE family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1)
		  AND revoked_at IS NULL`, presentedHash)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Signing out with an unknown token is not an error worth reporting.
		return nil
	}
	return nil
}

// RevokeAllForUser ends every session of an account.
func (s *Store) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return fmt.Errorf("revoke sessions of user: %w", err)
	}
	return nil
}

// DeleteExpiredTokens clears tokens that can no longer be used.
func (s *Store) DeleteExpiredTokens(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < now() - interval '30 days'`)
	if err != nil {
		return 0, fmt.Errorf("delete expired refresh tokens: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func revokeFamily(ctx context.Context, tx pgx.Tx, familyID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE family_id = $1 AND revoked_at IS NULL`, familyID); err != nil {
		return fmt.Errorf("revoke refresh token family: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (User, error) {
	var user User
	err := row.Scan(&user.ID, &user.Email, &user.Name, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt)
	return user, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
