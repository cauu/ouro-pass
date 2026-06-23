//go:build integration

// Package inttest holds Postgres-backed integration tests, isolated from the
// SQLite-oriented store unit tests. This is the only place the compare-and-swap
// one-time-use guards (p12-1) and refresh rotation (p12-2) can be proven under
// genuine concurrency — SQLite serializes all writers via MaxOpenConns(1).
//
// Run with:  make test-integration          (testcontainers spins an ephemeral PG)
//
//	or:  OUROPASS_TEST_PG_DSN=... make test-integration   (existing PG)
package inttest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
)

// uk returns a per-run unique key so the suite is re-runnable against a
// persistent PG (OUROPASS_TEST_PG_DSN) without primary-key collisions.
func uk(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// pgStore returns a migrated store backed by PostgreSQL: an existing instance
// via OUROPASS_TEST_PG_DSN, or an ephemeral testcontainers Postgres otherwise.
func pgStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("OUROPASS_TEST_PG_DSN")
	if dsn == "" {
		c, err := postgres.Run(ctx, "postgres:16-alpine",
			postgres.WithDatabase("ouro"),
			postgres.WithUsername("ouro"),
			postgres.WithPassword("ouro"),
			testcontainers.WithWaitStrategy(
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(90*time.Second)),
		)
		if err != nil {
			t.Fatalf("start postgres container (is Docker running?): %v", err)
		}
		t.Cleanup(func() { _ = c.Terminate(ctx) })
		if dsn, err = c.ConnectionString(ctx, "sslmode=disable"); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(ctx, store.Postgres, dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate pg: %v", err)
	}
	return st
}

// raceN runs fn from n goroutines simultaneously and returns how many returned
// nil (i.e. "won").
func raceN(n int, fn func() error) int64 {
	var wg, start sync.WaitGroup
	start.Add(1)
	var won int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait() // line everyone up so they truly contend
			if fn() == nil {
				atomic.AddInt64(&won, 1)
			}
		}()
	}
	start.Done()
	wg.Wait()
	return won
}

const racers = 24

func TestPG_ConcurrentConsume_NonceExactlyOnce(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	now := time.Now()
	nk := uk("race-n")
	if err := st.AuthNonces().Create(ctx, domain.AuthNonce{
		Nonce: nk, Purpose: domain.NonceIssue, ExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	won := raceN(racers, func() error {
		_, err := st.AuthNonces().Consume(ctx, nk, domain.NonceIssue, now)
		return err
	})
	if won != 1 {
		t.Fatalf("nonce consumed by %d concurrent callers, want exactly 1 (CAS broken on PG)", won)
	}
}

func TestPG_ConcurrentConsume_AuthCodeExactlyOnce(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	now := time.Now()
	ck := uk("race-c")
	if err := st.AuthCodes().Create(ctx, domain.AuthorizationCode{
		Code: ck, ClientID: "c1", StakeCredentialHash: "h1", Aud: "app",
		RedirectURI: "https://cb", ExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	won := raceN(racers, func() error {
		_, err := st.AuthCodes().Consume(ctx, ck, now)
		return err
	})
	if won != 1 {
		t.Fatalf("auth code redeemed by %d concurrent callers, want exactly 1", won)
	}
}

func TestPG_ConcurrentConsume_ActivationExactlyOnce(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	now := time.Now()
	ak := uk("race-a")
	if err := st.ActivationCodes().Create(ctx, domain.ActivationCode{
		Code: ak, StakeCredentialHash: "h1", ChannelType: "telegram",
		Status: domain.ActivationActive, ExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	won := raceN(racers, func() error {
		_, err := st.ActivationCodes().Consume(ctx, ak, "telegram", now)
		return err
	})
	if won != 1 {
		t.Fatalf("activation code redeemed by %d concurrent callers, want exactly 1", won)
	}
}

func TestPG_ConcurrentRefreshRotate_ExactlyOnce(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	gk := uk("race-g")
	if err := st.RefreshGrants().Create(ctx, nil, domain.RefreshGrant{
		RefreshGrantID: gk, StakeCredentialHash: "h1", Audience: "app",
		ClientType: domain.ClientPublic, Status: domain.GrantActive, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	var won int64
	_ = raceN(racers, func() error {
		ok, err := st.RefreshGrants().RotateIfActive(ctx, nil, gk)
		if err == nil && ok {
			atomic.AddInt64(&won, 1)
		}
		return nil
	})
	if won != 1 {
		t.Fatalf("refresh grant rotated by %d concurrent callers, want exactly 1", won)
	}
}

// TestPG_DialectRoundTrip exercises a representative repo on PG so the ?->$n
// rebind, TEXT/JSON encoding and timestamp formats are validated on the prod
// dialect, and the real embedded migrations apply on Postgres (closes the
// SQLite-only gap, TC-2).
func TestPG_DialectRoundTrip(t *testing.T) {
	st := pgStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	cid := uk("pg-c")
	if err := st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: cid, Name: "PG", ClientType: domain.ClientConfidential, Party: domain.FirstParty,
		RedirectURIs: []string{"https://a/cb", "https://b/cb"}, AllowedAudiences: []string{"app:ouro"},
		AllowedScopes: []string{"read", "push"}, PKCERequired: true, Status: "active", CreatedAt: now,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := st.OAuthClients().Get(ctx, cid)
	if err != nil || len(got.RedirectURIs) != 2 || len(got.AllowedScopes) != 2 || !got.PKCERequired {
		t.Fatalf("roundtrip mismatch: %+v err=%v", got, err)
	}
	// List must succeed on PG (Rebind path) and include our client (the DSN may
	// point at a shared instance, so don't assume an empty table).
	list, err := st.OAuthClients().List(ctx)
	if err != nil {
		t.Fatalf("list on pg: %v", err)
	}
	var found bool
	for _, c := range list {
		if c.ClientID == cid {
			found = true
		}
	}
	if !found {
		t.Fatalf("list did not include %s (n=%d)", cid, len(list))
	}
}
