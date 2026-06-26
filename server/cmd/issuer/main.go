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
	"ouro-pass/server/internal/core/attestor"
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
		// The bot token is resolved live: env first (OUROPASS_TELEGRAM_TOKEN, for
		// ops/back-compat), else the admin-configured ChannelConfig (decrypted).
		// The worker always runs and is a quiet no-op until a token exists, so
		// configuring it via the admin UI takes effect with no restart (S0004 p8-1).
		tokenFn := func() string {
			if cfg.TelegramToken != "" {
				return cfg.TelegramToken
			}
			if deps.Cipher == nil {
				return ""
			}
			c, err := st.Channels().GetByType(context.Background(), "telegram")
			if err != nil {
				return ""
			}
			tok, err := telegram.DecodeToken(deps.Cipher, c.Config)
			if err != nil {
				return ""
			}
			return tok
		}
		transport := telegram.NewBotAPITransport(tokenFn)
		proc := telegram.NewProcessor(st, deps.OAuth, cfg.Scope)
		startWorker("telegram", func() { telegram.NewWorker(proc, transport).Run(sigCtx) })
		// The push worker delivers admin-created PushJobs over the same Telegram
		// transport (the missing runtime driver, p12-4).
		pushWorker := push.NewWorker(st, transport, pushPollInterval, push.Options{})
		startWorker("push", func() { pushWorker.Run(sigCtx) })

		recon := reconciliation.New(st, deps.OAuth, chainSrc, cfg.Scope)
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
		PoolID:        cfg.Scope,
		TelegramBot:   cfg.TelegramBot,
		Network:       cfg.Network,
		TrustedProxy:  cfg.TrustedProxy,
		SecureCookies: cfg.TLS,
		Admin: admin.New(admin.Config{
			Wallet: walletSvc, Store: st, OwnerKeyHash: cfg.OwnerKeyHashes, PoolID: cfg.Scope,
		}),
	}
	if cfg.FieldKeyHex != "" {
		cipher, err := crypto.NewFieldCipherHex(cfg.FieldKeyHex)
		if err != nil {
			return httpapi.Deps{}, nil, err
		}
		deps.Keys = keys.New(st, cipher)
		deps.Cipher = cipher // for channel-secret encryption (telegram bot token)
	} else {
		slog.Warn("OUROPASS_FIELD_KEY not set; signing-key/JWKS routes disabled")
	}

	// Per-network chain source factory (S0006 D4): each attestor declares its own
	// network, so build and cache one source per network. Each is wrapped with the
	// pool-agnostic active-membership cache (S0006 p5-1): same-epoch `active` lookups
	// skip the chain; pending/none stay live.
	srcCache := map[string]chain.Source{}
	var srcMu sync.Mutex
	srcFor := func(network string) (chain.Source, error) {
		if network == "" {
			network = cfg.Network // default for attestors that omit it
		}
		srcMu.Lock()
		defer srcMu.Unlock()
		if s, ok := srcCache[network]; ok {
			return s, nil
		}
		raw, err := chain.NewSource(chain.Config{
			Kind: cfg.ChainKind, KoiosBaseURL: cfg.KoiosBaseURL, APIKey: cfg.ChainAPIKey,
			NodeSocket: cfg.NodeSocket, CardanoCLI: cfg.CardanoCLI, Network: network,
		})
		if err != nil {
			return nil, err
		}
		s := membership.NewCachedSource(raw, st.SnapshotCache(), network, 10*time.Second)
		srcCache[network] = s
		return s, nil
	}
	chainSrc, err := srcFor(cfg.Network) // default-network source: reconciler epoch tick + admin delegator roster
	if err != nil {
		return httpapi.Deps{}, nil, err
	}
	deps.Chain = chainSrc
	// S0006: resolve the attestor set (one pool_stake per AttestorConfig) from the
	// store per call so admin config changes take effect immediately.
	reg := attestor.DefaultRegistry()
	attestorsFor := func(ctx context.Context) (*attestor.Set, error) {
		cfgs, err := st.Attestors().ListActive(ctx)
		if err != nil {
			return nil, err
		}
		return attestor.BuildSet(cfgs, reg, srcFor)
	}
	if deps.Keys != nil && len(serverSalt) > 0 {
		deps.OAuth = oauth.New(oauth.Config{
			Store: st, Wallet: deps.Wallet, Keys: deps.Keys, Attestors: attestorsFor,
			Issuer: cfg.Issuer, ServerSalt: serverSalt,
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
