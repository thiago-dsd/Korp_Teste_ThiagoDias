package identity_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thiagodias/korp-invoices/internal/identity"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/postgres/pgtest"
)

func newTestService(t *testing.T) (context.Context, *identity.Service, *identity.Store, *pgxpool.Pool) {
	t.Helper()

	ctx, pool := pgtest.Pool(t, "IDENTITY_TEST_DATABASE_URL", identity.MigrationsFS, identity.MigrationsDir)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	store := identity.NewStore(pool)
	return ctx, identity.NewService(store, identity.NewTokenIssuer(key)), store, pool
}

const password = "correct horse battery staple"

func register(t *testing.T, ctx context.Context, service *identity.Service, email string) (identity.User, identity.TokenPair) {
	t.Helper()

	user, tokens, err := service.Register(ctx, email, "Ada Lovelace", password, "tests")
	if err != nil {
		t.Fatalf("Register() returned error: %v", err)
	}
	return user, tokens
}

func TestRegisterCreatesAnAccountAndSignsIn(t *testing.T) {
	ctx, service, _, pool := newTestService(t)

	user, tokens := register(t, ctx, service, "Ada@Example.com")

	if user.ID == uuid.Nil {
		t.Error("the account has no id")
	}
	if user.Email != "ada@example.com" {
		t.Errorf("Email = %q, want it normalized", user.Email)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Error("registering did not start a session")
	}

	// The password itself is never stored.
	var storedHash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, user.ID).Scan(&storedHash); err != nil {
		t.Fatalf("read password hash: %v", err)
	}
	if storedHash == password {
		t.Fatal("the password was stored in plain text")
	}
}

func TestRegisterRefusesAnAddressAlreadyTaken(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	register(t, ctx, service, "ada@example.com")

	_, _, err := service.Register(ctx, "ADA@example.com", "Someone Else", password, "tests")
	if !errors.Is(err, identity.ErrEmailTaken) {
		t.Errorf("Register() error = %v, want ErrEmailTaken", err)
	}
}

func TestLoginAcceptsTheRightPasswordOnly(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	register(t, ctx, service, "ada@example.com")

	if _, _, err := service.Login(ctx, "ADA@example.com", password, "tests"); err != nil {
		t.Fatalf("Login() returned error: %v", err)
	}

	_, _, err := service.Login(ctx, "ada@example.com", "wrong password entirely", "tests")
	if !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Errorf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

// An unknown address and a wrong password must be indistinguishable, so a
// stranger cannot use the endpoint to find out who has an account.
func TestLoginDoesNotRevealWhichAccountsExist(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	register(t, ctx, service, "ada@example.com")

	_, _, wrongPassword := service.Login(ctx, "ada@example.com", "wrong password entirely", "tests")
	_, _, unknownAccount := service.Login(ctx, "nobody@example.com", password, "tests")

	if wrongPassword == nil || unknownAccount == nil {
		t.Fatal("one of the failed sign in attempts succeeded")
	}
	if wrongPassword.Error() != unknownAccount.Error() {
		t.Errorf("answers differ: %q and %q, want them identical", wrongPassword, unknownAccount)
	}
}

func TestRefreshRotatesTheToken(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	_, tokens := register(t, ctx, service, "ada@example.com")

	_, rotated, err := service.Refresh(ctx, tokens.RefreshToken, "tests")
	if err != nil {
		t.Fatalf("Refresh() returned error: %v", err)
	}

	if rotated.RefreshToken == tokens.RefreshToken {
		t.Error("the refresh token was reused, want a new one on every exchange")
	}
	if rotated.AccessToken == "" {
		t.Error("no access token came back from the refresh")
	}

	// The new token keeps working.
	if _, _, err := service.Refresh(ctx, rotated.RefreshToken, "tests"); err != nil {
		t.Errorf("Refresh() with the rotated token returned error: %v", err)
	}
}

// The scenario rotation exists for: a token that leaked is replayed after the
// real client already used it. Both sessions must end.
func TestReplayingAnOldRefreshTokenEndsTheWholeSession(t *testing.T) {
	ctx, service, _, pool := newTestService(t)
	_, tokens := register(t, ctx, service, "ada@example.com")

	_, rotated, err := service.Refresh(ctx, tokens.RefreshToken, "legitimate client")
	if err != nil {
		t.Fatalf("Refresh() returned error: %v", err)
	}

	// Past the window where a second presentation is still one honest client
	// racing itself; see ReuseGracePeriod.
	if _, err := pool.Exec(ctx, `
		UPDATE refresh_tokens SET used_at = used_at - $1::interval WHERE used_at IS NOT NULL`,
		(identity.ReuseGracePeriod + time.Minute).String()); err != nil {
		t.Fatalf("age the used token: %v", err)
	}

	// The attacker replays the token the client already exchanged.
	if _, _, err := service.Refresh(ctx, tokens.RefreshToken, "attacker"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("replaying the old token returned %v, want ErrInvalidToken", err)
	}

	// And the token the legitimate client holds is now useless as well.
	if _, _, err := service.Refresh(ctx, rotated.RefreshToken, "legitimate client"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Errorf("the session survived a detected replay: %v", err)
	}
}

func TestRefreshRefusesUnknownAndExpiredTokens(t *testing.T) {
	ctx, service, _, pool := newTestService(t)
	_, tokens := register(t, ctx, service, "ada@example.com")

	if _, _, err := service.Refresh(ctx, "made-up-token", "tests"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Errorf("Refresh() with an unknown token returned %v, want ErrInvalidToken", err)
	}
	if _, _, err := service.Refresh(ctx, "", "tests"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Errorf("Refresh() with an empty token returned %v, want ErrInvalidToken", err)
	}

	// An expired token is one issued long ago; both timestamps move back.
	if _, err := pool.Exec(ctx, `
		UPDATE refresh_tokens
		SET issued_at = now() - interval '8 days', expires_at = now() - interval '1 hour'`); err != nil {
		t.Fatalf("expire tokens: %v", err)
	}
	if _, _, err := service.Refresh(ctx, tokens.RefreshToken, "tests"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Errorf("Refresh() with an expired token returned %v, want ErrInvalidToken", err)
	}
}

func TestLogoutEndsOnlyThatSession(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	register(t, ctx, service, "ada@example.com")

	_, phone, err := service.Login(ctx, "ada@example.com", password, "phone")
	if err != nil {
		t.Fatalf("Login() returned error: %v", err)
	}
	_, laptop, err := service.Login(ctx, "ada@example.com", password, "laptop")
	if err != nil {
		t.Fatalf("Login() returned error: %v", err)
	}

	if err := service.Logout(ctx, phone.RefreshToken); err != nil {
		t.Fatalf("Logout() returned error: %v", err)
	}

	if _, _, err := service.Refresh(ctx, phone.RefreshToken, "phone"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Errorf("the signed out session still works: %v", err)
	}
	if _, _, err := service.Refresh(ctx, laptop.RefreshToken, "laptop"); err != nil {
		t.Errorf("signing out on one device ended the session of another: %v", err)
	}
}

func TestLogoutWithAnUnknownTokenIsHarmless(t *testing.T) {
	ctx, service, _, _ := newTestService(t)

	if err := service.Logout(ctx, "made-up-token"); err != nil {
		t.Errorf("Logout() returned error: %v", err)
	}
	if err := service.Logout(ctx, ""); err != nil {
		t.Errorf("Logout() returned error: %v", err)
	}
}

func TestDeleteAccountRequiresThePasswordAndEndsEverySession(t *testing.T) {
	ctx, service, _, pool := newTestService(t)
	user, tokens := register(t, ctx, service, "ada@example.com")
	_, other, err := service.Login(ctx, "ada@example.com", password, "another device")
	if err != nil {
		t.Fatalf("Login() returned error: %v", err)
	}

	if err := service.DeleteAccount(ctx, user.ID, "wrong password entirely"); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("DeleteAccount() with a wrong password returned %v, want ErrInvalidCredentials", err)
	}
	if _, err := service.Profile(ctx, user.ID); err != nil {
		t.Fatalf("the account was affected by a refused deletion: %v", err)
	}

	if err := service.DeleteAccount(ctx, user.ID, password); err != nil {
		t.Fatalf("DeleteAccount() returned error: %v", err)
	}

	if _, err := service.Profile(ctx, user.ID); !errors.Is(err, identity.ErrUserNotFound) {
		t.Errorf("Profile() after deletion returned %v, want ErrUserNotFound", err)
	}
	for name, token := range map[string]string{"first": tokens.RefreshToken, "second": other.RefreshToken} {
		if _, _, err := service.Refresh(ctx, token, "tests"); !errors.Is(err, identity.ErrInvalidToken) {
			t.Errorf("the %s session survived the account deletion: %v", name, err)
		}
	}

	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens`).Scan(&remaining); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	if remaining != 0 {
		t.Errorf("refresh tokens left after deleting the account = %d, want 0", remaining)
	}
}

func TestProfileReturnsTheAccount(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	user, _ := register(t, ctx, service, "ada@example.com")

	profile, err := service.Profile(ctx, user.ID)
	if err != nil {
		t.Fatalf("Profile() returned error: %v", err)
	}
	if profile.Email != user.Email || profile.Name != user.Name {
		t.Errorf("profile = %+v, want the registered account", profile)
	}

	if _, err := service.Profile(ctx, uuid.New()); !errors.Is(err, identity.ErrUserNotFound) {
		t.Errorf("Profile() of an unknown id returned %v, want ErrUserNotFound", err)
	}
}

func TestDeleteExpiredTokensKeepsLiveSessions(t *testing.T) {
	ctx, service, store, pool := newTestService(t)
	_, tokens := register(t, ctx, service, "ada@example.com")

	removed, err := store.DeleteExpiredTokens(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredTokens() returned error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed %d live tokens, want 0", removed)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE refresh_tokens
		SET issued_at = now() - interval '90 days', expires_at = now() - interval '60 days'`); err != nil {
		t.Fatalf("age tokens: %v", err)
	}
	removed, err = store.DeleteExpiredTokens(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredTokens() returned error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d tokens, want 1", removed)
	}
	if _, _, err := service.Refresh(ctx, tokens.RefreshToken, "tests"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Errorf("a cleaned up token still works: %v", err)
	}
}

func TestSessionsSurviveTheAccessTokenExpiry(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	_, tokens := register(t, ctx, service, "ada@example.com")

	// Refreshing right after the access token would have expired still works,
	// which is what keeps someone signed in through a working day.
	if identity.RefreshTokenTTL <= identity.AccessTokenTTL {
		t.Fatal("the refresh token does not outlive the access token")
	}
	if _, _, err := service.Refresh(ctx, tokens.RefreshToken, "tests"); err != nil {
		t.Errorf("Refresh() returned error: %v", err)
	}
	_ = time.Now
}

func TestAnAccountUnderAttackIsLockedForEveryone(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	register(t, ctx, service, "ada@example.com")

	policy := identity.LockoutPolicy{MaxFailures: 3, Lockout: time.Minute}
	service.WithLockoutPolicy(policy)

	for attempt := 1; attempt <= 3; attempt++ {
		_, _, err := service.Login(ctx, "ada@example.com", "wrong password entirely", "attacker")
		if !errors.Is(err, identity.ErrInvalidCredentials) {
			t.Fatalf("attempt %d returned %v, want the ordinary refusal", attempt, err)
		}
	}

	// The account is now closed, and the right password does not open it.
	_, _, err := service.Login(ctx, "ada@example.com", password, "the real owner")
	if !errors.Is(err, identity.ErrTooManyAttempts) {
		t.Fatalf("Login() returned %v, want ErrTooManyAttempts", err)
	}
	if wait := apperr.From(err).Details["retry_after_seconds"]; wait == "" {
		t.Error("the answer does not say how long to wait")
	}
}

// A locked answer must not depend on the account existing, or it would tell an
// attacker which addresses are registered.
func TestLockingLooksTheSameForUnknownAddresses(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	service.WithLockoutPolicy(identity.LockoutPolicy{MaxFailures: 2, Lockout: time.Minute})

	for range 2 {
		if _, _, err := service.Login(ctx, "nobody@example.com", "guessing", "attacker"); !errors.Is(err, identity.ErrInvalidCredentials) {
			t.Fatalf("Login() returned %v, want the ordinary refusal", err)
		}
	}

	_, _, err := service.Login(ctx, "nobody@example.com", "guessing", "attacker")
	if !errors.Is(err, identity.ErrTooManyAttempts) {
		t.Errorf("Login() returned %v, want an address with no account to lock as well", err)
	}
}

func TestOneAccountBeingAttackedDoesNotAffectAnother(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	register(t, ctx, service, "ada@example.com")
	register(t, ctx, service, "grace@example.com")
	service.WithLockoutPolicy(identity.LockoutPolicy{MaxFailures: 2, Lockout: time.Minute})

	for range 2 {
		service.Login(ctx, "ada@example.com", "wrong password entirely", "attacker")
	}

	if _, _, err := service.Login(ctx, "grace@example.com", password, "someone working"); err != nil {
		t.Errorf("Login() returned %v, want the other account unaffected", err)
	}
}

func TestSigningInSuccessfullyForgetsEarlierTypos(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	register(t, ctx, service, "ada@example.com")
	service.WithLockoutPolicy(identity.LockoutPolicy{MaxFailures: 3, Lockout: time.Minute})

	for range 2 {
		service.Login(ctx, "ada@example.com", "typo", "the owner")
	}
	if _, _, err := service.Login(ctx, "ada@example.com", password, "the owner"); err != nil {
		t.Fatalf("Login() returned %v, want the right password to work", err)
	}

	// The count started over, so two more typos still do not lock the account.
	for range 2 {
		service.Login(ctx, "ada@example.com", "typo", "the owner")
	}
	if _, _, err := service.Login(ctx, "ada@example.com", password, "the owner"); err != nil {
		t.Errorf("Login() returned %v, want the failures to have been forgotten", err)
	}
}

func TestTheLockExpires(t *testing.T) {
	ctx, service, _, pool := newTestService(t)
	register(t, ctx, service, "ada@example.com")
	service.WithLockoutPolicy(identity.LockoutPolicy{MaxFailures: 1, Lockout: time.Hour})

	service.Login(ctx, "ada@example.com", "wrong password entirely", "attacker")
	if _, _, err := service.Login(ctx, "ada@example.com", password, "the owner"); !errors.Is(err, identity.ErrTooManyAttempts) {
		t.Fatalf("Login() returned %v, want the account locked", err)
	}

	// Once the lock has run out, the owner is let back in.
	if _, err := pool.Exec(ctx, `UPDATE login_attempts SET blocked_until = now() - interval '1 minute'`); err != nil {
		t.Fatalf("expire the lock: %v", err)
	}
	if _, _, err := service.Login(ctx, "ada@example.com", password, "the owner"); err != nil {
		t.Errorf("Login() returned %v, want the owner served after the lock expired", err)
	}
}

// Every instance of the service counts against the same row, which is the
// point of keeping the count in the database.
func TestTheCountIsSharedBetweenInstances(t *testing.T) {
	ctx, first, store, _ := newTestService(t)
	register(t, ctx, first, "ada@example.com")

	// A second instance, with its own service but the same database.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	second := identity.NewService(store, identity.NewTokenIssuer(key))

	policy := identity.LockoutPolicy{MaxFailures: 4, Lockout: time.Minute}
	first.WithLockoutPolicy(policy)
	second.WithLockoutPolicy(policy)

	// The attempts are split between the two instances.
	for range 2 {
		first.Login(ctx, "ada@example.com", "guessing", "attacker")
		second.Login(ctx, "ada@example.com", "guessing", "attacker")
	}

	if _, _, err := second.Login(ctx, "ada@example.com", password, "the owner"); !errors.Is(err, identity.ErrTooManyAttempts) {
		t.Errorf("Login() returned %v, want the attempts counted across instances", err)
	}
}

// Concurrent guesses must not slip past the limit by racing each other.
func TestConcurrentGuessesStillLockTheAccount(t *testing.T) {
	ctx, service, _, pool := newTestService(t)
	register(t, ctx, service, "ada@example.com")
	service.WithLockoutPolicy(identity.LockoutPolicy{MaxFailures: 5, Lockout: time.Minute})

	var wg sync.WaitGroup
	wg.Add(20)
	for range 20 {
		go func() {
			defer wg.Done()
			service.Login(ctx, "ada@example.com", "guessing", "attacker")
		}()
	}
	wg.Wait()

	var failures int
	if err := pool.QueryRow(ctx, `SELECT failures FROM login_attempts WHERE email = 'ada@example.com'`).Scan(&failures); err != nil {
		t.Fatalf("read failures: %v", err)
	}
	if failures < 5 {
		t.Errorf("failures = %d, want every attempt counted", failures)
	}
	if _, _, err := service.Login(ctx, "ada@example.com", password, "the owner"); !errors.Is(err, identity.ErrTooManyAttempts) {
		t.Errorf("Login() returned %v, want the account locked", err)
	}
}

// Two browser tabs hold the same refresh token. When the access token expires
// they both refresh at the same moment: one wins and the other presents a token
// that was just exchanged. Treating that as a stolen token ends the session and
// signs the person out of a perfectly honest browser.
func TestConcurrentRefreshDoesNotEndTheSession(t *testing.T) {
	ctx, service, _, _ := newTestService(t)
	_, tokens := register(t, ctx, service, "ada@example.com")

	const tabs = 2
	start := make(chan struct{})
	results := make(chan error, tabs)
	issued := make(chan identity.TokenPair, tabs)

	for range tabs {
		go func() {
			<-start
			_, pair, err := service.Refresh(ctx, tokens.RefreshToken, "tests")
			results <- err
			if err == nil {
				issued <- pair
			}
		}()
	}
	close(start)

	succeeded := 0
	for range tabs {
		if err := <-results; err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d refreshes succeeded, want exactly 1", succeeded)
	}

	// The session must survive: the tab that lost the race reads the token the
	// winner stored and carries on.
	winner := <-issued
	if _, _, err := service.Refresh(ctx, winner.RefreshToken, "tests"); err != nil {
		t.Errorf("the session was ended by an honest concurrent refresh: %v", err)
	}
}

// The grace window must not become a hole: a token that resurfaces long after
// it was spent is still treated as stolen and ends the whole family.
func TestReplayAfterTheGraceWindowStillRevokesTheFamily(t *testing.T) {
	ctx, service, _, pool := newTestService(t)
	_, tokens := register(t, ctx, service, "ada@example.com")

	_, rotated, err := service.Refresh(ctx, tokens.RefreshToken, "tests")
	if err != nil {
		t.Fatalf("Refresh() returned error: %v", err)
	}

	// Age the exchange past the window, as if the token had been stolen and
	// replayed later.
	if _, err := pool.Exec(ctx, `
		UPDATE refresh_tokens SET used_at = used_at - $1::interval WHERE used_at IS NOT NULL`,
		(identity.ReuseGracePeriod + time.Minute).String()); err != nil {
		t.Fatalf("age the used token: %v", err)
	}

	if _, _, err := service.Refresh(ctx, tokens.RefreshToken, "attacker"); err == nil {
		t.Fatal("replaying a spent token was accepted, want it refused")
	}

	// The session the attacker was racing against is gone too, which is the
	// point of detecting reuse.
	if _, _, err := service.Refresh(ctx, rotated.RefreshToken, "tests"); err == nil {
		t.Error("the family survived a detected replay, want every token revoked")
	}
}

// TEMPORARY, alongside the rule in CreateUser: every account registers as an
// administrator, so a clean clone can register a product without a detour
// through the database. When the system learns to grant the role, this goes
// back to asserting that only the first account administers.
func TestEveryAccountRegistersAsAdministrator(t *testing.T) {
	ctx, service, _, _ := newTestService(t)

	first, _ := register(t, ctx, service, "first@example.com")
	if first.Role != identity.RoleAdmin {
		t.Errorf("first account role = %q, want %q", first.Role, identity.RoleAdmin)
	}

	second, _ := register(t, ctx, service, "second@example.com")
	if second.Role != identity.RoleAdmin {
		t.Errorf("second account role = %q, want %q", second.Role, identity.RoleAdmin)
	}
}
