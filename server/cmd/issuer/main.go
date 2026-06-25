// Command issuer is the Ouro Pass Issuer Service entrypoint: it loads config,
// opens and migrates the database, assembles the HTTP router with its services,
// and serves until SIGINT/SIGTERM, then shuts down gracefully.
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"ouro-pass/server/internal/config"
	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/membership"
	"ouro-pass/server/internal/core/oauth"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/httpapi"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
	"ouro-pass/server/internal/worker/push"
	"ouro-pass/server/internal/worker/reconciliation"
	"ouro-pass/server/internal/worker/telegram"
)

// epochPollInterval is how often the reconciler checks for an epoch change.
const epochPollInterval = 5 * time.Minute

// pushPollInterval is how often the push worker drains due scheduled jobs.
const pushPollInterval = 15 * time.Second

const (
	nonceTTL        = 5 * time.Minute
	nonceGCInterval = 10 * time.Minute // independent of epoch; nonces are minute-scale
	accessTTL       = 24 * time.Hour
	refreshTTL      = 30 * 24 * time.Hour
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx := context.Background()
	st, err := store.Open(ctx, store.Driver(cfg.DBDriver), cfg.DBDSN)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	slog.Info("database ready", "driver", cfg.DBDriver)

	deps, chainSrc, err := buildServices(cfg, st)
	if err != nil {
		return err
	}
	walletSvc := deps.Wallet

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: httpapi.NewRouter(deps),
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// All background workers are tracked so shutdown can join them (p12-4).
	var workers sync.WaitGroup
	startWorker := func(name string, fn func()) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			fn()
		}()
		slog.Info("worker started", "worker", name)
	}

	// Periodic GC of expired wallet-signing nonces (short TTL → short interval,
	// not tied to epoch).
	startWorker("nonce-gc", func() { runNonceGC(sigCtx, walletSvc) })

	// Background workers run while issuance is enabled (they need eligibility).
	if deps.OAuth != nil {
		if cfg.TelegramToken != "" {
			transport := telegram.NewBotAPITransport(cfg.TelegramToken)
			proc := telegram.NewProcessor(st, deps.OAuth, cfg.PoolID)
			tgWorker := telegram.NewWorker(proc, transport)
			startWorker("telegram", func() { tgWorker.Run(sigCtx) })
			// The push worker delivers admin-created PushJobs over the same
			// Telegram transport (the missing runtime driver, p12-4).
			pushWorker := push.NewWorker(st, transport, pushPollInterval, push.Options{})
			startWorker("push", func() { pushWorker.Run(sigCtx) })
		}
		recon := reconciliation.New(st, deps.OAuth, chainSrc, cfg.PoolID)
		startWorker("reconciliation", func() { recon.Run(sigCtx, epochPollInterval) })
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("issuer listening", "addr", cfg.Addr, "network", cfg.Network)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-sigCtx.Done():
		slog.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}

	// Join the workers (they observe sigCtx cancellation) so in-flight work
	// finishes before exit, bounded by ShutdownTimeout (p12-4).
	stop() // ensure sigCtx is cancelled so workers wind down
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
		slog.Info("all workers stopped")
	case <-shutdownCtx.Done():
		slog.Warn("workers did not stop within shutdown timeout")
	}
	return nil
}

// buildServices assembles the handler dependencies and the staking source from
// config against an open store. Services degrade gracefully: without a field key
// the signing-key/JWKS routes are disabled; without field key + server salt the
// OAuth issuance routes are disabled. Extracted from run() so wiring is testable.
func buildServices(cfg *config.Config, st *store.Store) (httpapi.Deps, chain.Source, error) {
	walletSvc := walletauth.New(st, nonceTTL)
	var serverSalt []byte
	if cfg.ServerSaltHex != "" {
		var err error
		if serverSalt, err = hex.DecodeString(cfg.ServerSaltHex); err != nil {
			return httpapi.Deps{}, nil, fmt.Errorf("OUROPASS_SERVER_SALT: %w", err)
		}
	}
	deps := httpapi.Deps{
		Wallet:        walletSvc,
		Store:         st,
		PoolID:        cfg.PoolID,
		TelegramBot:   cfg.TelegramBot,
		Network:       cfg.Network,
		TrustedProxy:  cfg.TrustedProxy,
		SecureCookies: cfg.TLS,
		Admin: admin.New(admin.Config{
			Wallet: walletSvc, Store: st, OwnerKeyHash: cfg.OwnerKeyHashes, PoolID: cfg.PoolID,
		}),
	}
	if cfg.FieldKeyHex != "" {
		cipher, err := crypto.NewFieldCipherHex(cfg.FieldKeyHex)
		if err != nil {
			return httpapi.Deps{}, nil, err
		}
		deps.Keys = keys.New(st, cipher)
	} else {
		slog.Warn("OUROPASS_FIELD_KEY not set; signing-key/JWKS routes disabled")
	}

	rawChain, err := chain.NewSource(chain.Config{
		Kind: cfg.ChainKind, KoiosBaseURL: cfg.KoiosBaseURL, APIKey: cfg.ChainAPIKey,
		NodeSocket: cfg.NodeSocket, CardanoCLI: cfg.CardanoCLI, Network: cfg.Network,
	})
	if err != nil {
		return httpapi.Deps{}, nil, err
	}
	// Wrap with the active-membership cache (S0004 §2.3): same-epoch `active`
	// lookups skip the chain; pending/none stay live. Both the issuance path and
	// the reconciler share it (the reconciler warms it across epoch boundaries).
	chainSrc := membership.NewCachedSource(rawChain, st.SnapshotCache(), cfg.PoolID, cfg.Network, 10*time.Second)
	if deps.Keys != nil && len(serverSalt) > 0 {
		deps.OAuth = oauth.New(oauth.Config{
			Store: st, Wallet: deps.Wallet, Keys: deps.Keys, Chain: chainSrc,
			PoolID: cfg.PoolID, Network: cfg.Network, Issuer: cfg.Issuer, ServerSalt: serverSalt,
			AccessTTL: accessTTL, RefreshTTL: refreshTTL,
		})
	} else {
		slog.Warn("OAuth issuance disabled (need OUROPASS_FIELD_KEY + OUROPASS_SERVER_SALT)")
	}
	return deps, chainSrc, nil
}

// runNonceGC periodically purges expired wallet-signing nonces until ctx ends.
func runNonceGC(ctx context.Context, wallet *walletauth.Service) {
	ticker := time.NewTicker(nonceGCInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := wallet.PurgeExpiredNonces(ctx); err != nil {
				slog.Warn("nonce GC failed", "err", err)
			} else if n > 0 {
				slog.Info("nonce GC", "removed", n)
			}
		}
	}
}
