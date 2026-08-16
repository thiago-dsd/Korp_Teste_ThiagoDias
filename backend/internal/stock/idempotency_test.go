package stock_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/thiagodias/korp-invoices/internal/platform/idempotency"
	"github.com/thiagodias/korp-invoices/internal/platform/postgres/pgtest"
	"github.com/thiagodias/korp-invoices/internal/stock"
)

// The stock database carries the idempotency table, so its PostgreSQL backed
// store is exercised here against a real database.
func newIdempotencyStore(t *testing.T) (context.Context, *idempotency.PostgresStore) {
	t.Helper()

	ctx, pool := pgtest.Pool(t, "STOCK_TEST_DATABASE_URL", stock.MigrationsFS, stock.MigrationsDir)
	return ctx, idempotency.NewPostgresStore(pool)
}

func TestPostgresStoreReplaysCompletedRequest(t *testing.T) {
	ctx, store := newIdempotencyStore(t)
	const endpoint, key, hash = "POST /products", "key-1", "hash-1"

	replay, err := store.Reserve(ctx, endpoint, key, hash)
	if err != nil {
		t.Fatalf("first Reserve() returned error: %v", err)
	}
	if replay != nil {
		t.Fatal("first Reserve() returned a stored response, want none")
	}

	response := idempotency.Record{StatusCode: http.StatusCreated, Body: []byte(`{"code":"P-1"}`)}
	if err := store.Complete(ctx, endpoint, key, response); err != nil {
		t.Fatalf("Complete() returned error: %v", err)
	}

	replay, err = store.Reserve(ctx, endpoint, key, hash)
	if err != nil {
		t.Fatalf("second Reserve() returned error: %v", err)
	}
	if replay == nil {
		t.Fatal("second Reserve() returned no stored response, want the first one")
	}
	if replay.StatusCode != http.StatusCreated || string(replay.Body) != `{"code":"P-1"}` {
		t.Errorf("replay = %+v, want the stored response", replay)
	}
}

func TestPostgresStoreRejectsKeyReuse(t *testing.T) {
	ctx, store := newIdempotencyStore(t)
	const endpoint, key = "POST /products", "key-1"

	if _, err := store.Reserve(ctx, endpoint, key, "hash-1"); err != nil {
		t.Fatalf("Reserve() returned error: %v", err)
	}

	_, err := store.Reserve(ctx, endpoint, key, "hash-2")
	if !errors.Is(err, idempotency.ErrKeyReuse) {
		t.Errorf("Reserve() error = %v, want ErrKeyReuse", err)
	}
}

func TestPostgresStoreReportsRequestInProgress(t *testing.T) {
	ctx, store := newIdempotencyStore(t)
	const endpoint, key, hash = "POST /products", "key-1", "hash-1"

	if _, err := store.Reserve(ctx, endpoint, key, hash); err != nil {
		t.Fatalf("Reserve() returned error: %v", err)
	}

	_, err := store.Reserve(ctx, endpoint, key, hash)
	if !errors.Is(err, idempotency.ErrRequestInProgress) {
		t.Errorf("Reserve() error = %v, want ErrRequestInProgress", err)
	}
}

func TestPostgresStoreReleaseAllowsRetry(t *testing.T) {
	ctx, store := newIdempotencyStore(t)
	const endpoint, key, hash = "POST /products", "key-1", "hash-1"

	if _, err := store.Reserve(ctx, endpoint, key, hash); err != nil {
		t.Fatalf("Reserve() returned error: %v", err)
	}
	if err := store.Release(ctx, endpoint, key); err != nil {
		t.Fatalf("Release() returned error: %v", err)
	}

	replay, err := store.Reserve(ctx, endpoint, key, hash)
	if err != nil {
		t.Fatalf("Reserve() after Release() returned error: %v", err)
	}
	if replay != nil {
		t.Error("Reserve() after Release() returned a stored response, want none")
	}
}

func TestPostgresStoreReleaseKeepsCompletedRecords(t *testing.T) {
	ctx, store := newIdempotencyStore(t)
	const endpoint, key, hash = "POST /products", "key-1", "hash-1"

	if _, err := store.Reserve(ctx, endpoint, key, hash); err != nil {
		t.Fatalf("Reserve() returned error: %v", err)
	}
	if err := store.Complete(ctx, endpoint, key, idempotency.Record{StatusCode: 201, Body: []byte(`{}`)}); err != nil {
		t.Fatalf("Complete() returned error: %v", err)
	}
	if err := store.Release(ctx, endpoint, key); err != nil {
		t.Fatalf("Release() returned error: %v", err)
	}

	replay, err := store.Reserve(ctx, endpoint, key, hash)
	if err != nil {
		t.Fatalf("Reserve() returned error: %v", err)
	}
	if replay == nil {
		t.Error("completed record was released, want it kept for replay")
	}
}

// Two concurrent requests carrying the same key must not both be allowed to
// run: exactly one reserves it, the other is told to retry.
func TestPostgresStoreSerializesConcurrentReservations(t *testing.T) {
	ctx, store := newIdempotencyStore(t)
	const endpoint, key, hash = "POST /products", "concurrent-key", "hash-1"

	const attempts = 8
	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		reserved   int
		inProgress int
		unexpected []error
	)

	wg.Add(attempts)
	for range attempts {
		go func() {
			defer wg.Done()

			attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			replay, err := store.Reserve(attemptCtx, endpoint, key, hash)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && replay == nil:
				reserved++
			case errors.Is(err, idempotency.ErrRequestInProgress):
				inProgress++
			default:
				unexpected = append(unexpected, err)
			}
		}()
	}
	wg.Wait()

	if len(unexpected) > 0 {
		t.Fatalf("unexpected errors: %v", unexpected)
	}
	if reserved != 1 {
		t.Errorf("%d requests reserved the key, want exactly 1", reserved)
	}
	if inProgress != attempts-1 {
		t.Errorf("%d requests were told to retry, want %d", inProgress, attempts-1)
	}
}
