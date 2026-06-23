// Command issuer is the PoolOps Issuer Service entrypoint: it loads config,
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
	"syscall"
	"time"

	"github.com/poolops/issuer/internal/config"
	"github.com/poolops/issuer/internal/core/admin"
	"github.com/poolops/issuer/internal/core/keys"
	"github.com/poolops/issuer/internal/core/oauth"
	"github.com/poolops/issuer/internal/core/walletauth"
	"github.com/poolops/issuer/internal/httpapi"
	"github.com/poolops/issuer/internal/store"
	"github.com/poolops/issuer/internal/utils/chain"
	"github.com/poolops/issuer/internal/utils/crypto"
	"github.com/poolops/issuer/internal/worker/reconciliation"
	"github.com/poolops/issuer/internal/worker/telegram"
)

// epochPollInterval is how often the reconciler checks for an epoch change.
const epochPollInterval = 5 * time.Minute

const (
	nonceTTL   = 5 * time.Minute
	accessTTL  = 24 * time.Hour
	refreshTTL = 30 * 24 * time.Hour
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

	walletSvc := walletauth.New(st, nonceTTL)
	deps := httpapi.Deps{
		Wallet:      walletSvc,
		TelegramBot: cfg.TelegramBot,
		Admin: admin.New(admin.Config{
			Wallet: walletSvc, Store: st, OwnerKeyHash: cfg.OwnerKeyHashes, PoolID: cfg.PoolID,
		}),
	}
	// The signing-key service (and any 🔒-field handling) needs the field key.
	// Without it the service still boots; key/JWKS routes degrade to 501.
	if cfg.FieldKeyHex != "" {
		cipher, err := crypto.NewFieldCipherHex(cfg.FieldKeyHex)
		if err != nil {
			return err
		}
		deps.Keys = keys.New(st, cipher)
	} else {
		slog.Warn("POOLOPS_FIELD_KEY not set; signing-key/JWKS routes disabled")
	}

	// The OAuth authorization server needs the signing keys, the `sub` salt, and
	// a staking source. Missing any → issuance routes degrade to 501.
	chainSrc, err := chain.NewSource(chain.Config{
		Kind: cfg.ChainKind, KoiosBaseURL: cfg.KoiosBaseURL, APIKey: cfg.ChainAPIKey,
		NodeSocket: cfg.NodeSocket, CardanoCLI: cfg.CardanoCLI, Network: cfg.Network,
	})
	if err != nil {
		return err
	}
	if deps.Keys != nil && cfg.ServerSaltHex != "" {
		salt, err := hex.DecodeString(cfg.ServerSaltHex)
		if err != nil {
			return fmt.Errorf("POOLOPS_SERVER_SALT: %w", err)
		}
		deps.OAuth = oauth.New(oauth.Config{
			Store: st, Wallet: deps.Wallet, Keys: deps.Keys, Chain: chainSrc,
			PoolID: cfg.PoolID, Issuer: cfg.Issuer, ServerSalt: salt,
			AccessTTL: accessTTL, RefreshTTL: refreshTTL,
		})
	} else {
		slog.Warn("OAuth issuance disabled (need POOLOPS_FIELD_KEY + POOLOPS_SERVER_SALT)")
	}

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: httpapi.NewRouter(deps),
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Background workers run while issuance is enabled (they need eligibility).
	if deps.OAuth != nil {
		if cfg.TelegramToken != "" {
			proc := telegram.NewProcessor(st, deps.OAuth, cfg.PoolID)
			worker := telegram.NewWorker(proc, telegram.NewBotAPITransport(cfg.TelegramToken))
			go worker.Run(sigCtx)
			slog.Info("telegram worker started")
		}
		recon := reconciliation.New(st, deps.OAuth, chainSrc, cfg.PoolID)
		go recon.Run(sigCtx, epochPollInterval)
		slog.Info("reconciliation worker started")
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
	return srv.Shutdown(shutdownCtx)
}
