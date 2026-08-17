package idempotency

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore keeps idempotency records in the service database, so a
// reservation and the work it protects share the same durability guarantees.
type PostgresStore struct {
	pool *pgxpool.Pool
	// staleAfter is how long an unfinished reservation may block a key.
	staleAfter time.Duration
}

// NewPostgresStore builds a store backed by the given pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, staleAfter: StaleReservationAge}
}

// Reserve claims a key, taking over reservations abandoned by crashed requests.
func (s *PostgresStore) Reserve(ctx context.Context, endpoint, key, requestHash string) (*Record, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin idempotency transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		storedHash string
		status     *int
		body       []byte
		completed  *time.Time
		reservedAt time.Time
	)

	// Locking the row serializes concurrent requests carrying the same key.
	err = tx.QueryRow(ctx, `
		SELECT request_hash, status_code, response_body, completed_at, reserved_at
		FROM idempotency_keys
		WHERE endpoint = $1 AND key = $2
		FOR UPDATE`, endpoint, key).Scan(&storedHash, &status, &body, &completed, &reservedAt)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `
			INSERT INTO idempotency_keys (endpoint, key, request_hash)
			VALUES ($1, $2, $3)`, endpoint, key, requestHash); err != nil {
			// Another request inserted the same key between the read above and
			// this insert, so it owns the reservation and is still running.
			if isUniqueViolation(err) {
				return nil, ErrRequestInProgress
			}
			return nil, fmt.Errorf("reserve idempotency key: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit idempotency reservation: %w", err)
		}
		return nil, nil

	case err != nil:
		return nil, fmt.Errorf("read idempotency key: %w", err)
	}

	if storedHash != requestHash {
		return nil, ErrKeyReuse
	}
	if completed != nil && status != nil {
		return &Record{StatusCode: *status, Body: body}, nil
	}
	if time.Since(reservedAt) < s.staleAfter {
		return nil, ErrRequestInProgress
	}

	// The previous attempt never finished: take the reservation over.
	if _, err := tx.Exec(ctx, `
		UPDATE idempotency_keys
		SET reserved_at = now()
		WHERE endpoint = $1 AND key = $2`, endpoint, key); err != nil {
		return nil, fmt.Errorf("take over idempotency reservation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit idempotency reservation: %w", err)
	}
	return nil, nil
}

// Complete stores the response of a finished request.
func (s *PostgresStore) Complete(ctx context.Context, endpoint, key string, record Record) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE idempotency_keys
		SET status_code = $3, response_body = $4, completed_at = now()
		WHERE endpoint = $1 AND key = $2`, endpoint, key, record.StatusCode, record.Body)
	if err != nil {
		return fmt.Errorf("store idempotent response: %w", err)
	}
	return nil
}

// isUniqueViolation reports a PostgreSQL unique constraint error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Release removes a reservation so the client can retry the request.
func (s *PostgresStore) Release(ctx context.Context, endpoint, key string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM idempotency_keys
		WHERE endpoint = $1 AND key = $2 AND completed_at IS NULL`, endpoint, key)
	if err != nil {
		return fmt.Errorf("release idempotency key: %w", err)
	}
	return nil
}

// DeleteCompletedBefore removes replayable answers older than age.
//
// A key is kept so a retry of the same request replays the original answer.
// Once no client could reasonably still be retrying, the row is only cost: the
// table is on the write path of every endpoint that accepts a key.
// Reservations that never completed are left alone; those are released by the
// middleware or taken over when they go stale.
func (s *PostgresStore) DeleteCompletedBefore(ctx context.Context, age time.Duration) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM idempotency_keys
		WHERE completed_at IS NOT NULL AND completed_at < now() - $1::interval`, age.String())
	if err != nil {
		return 0, fmt.Errorf("delete completed idempotency keys: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
