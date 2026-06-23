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
	"syscall"
	"time"

	"ouro-pass/server/internal/config"
	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/oauth"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/httpapi"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
	"ouro-pass/server/internal/worker/reconciliation"
	"ouro-pass/server/internal/worker/telegram"
)

// epochPollInterval is how often the reconciler checks for an epoch change.
const epochPollInterval = 5 * time.Minute

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

	walletSvc := walletauth.New(st, nonceTTL)
	var serverSalt []byte
	if cfg.ServerSaltHex != "" {
		if serverSalt, err = hex.DecodeString(cfg.ServerSaltHex); err != nil {
			return fmt.Errorf("OUROPASS_SERVER_SALT: %w", err)
		}
	}
	deps := httpapi.Deps{
		Wallet:      walletSvc,
		Store:       st,
		PoolID:      cfg.PoolID,
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
		slog.Warn("OUROPASS_FIELD_KEY not set; signing-key/JWKS routes disabled")
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
	if deps.Keys != nil && len(serverSalt) > 0 {
		deps.OAuth = oauth.New(oauth.Config{
			Store: st, Wallet: deps.Wallet, Keys: deps.Keys, Chain: chainSrc,
			PoolID: cfg.PoolID, Issuer: cfg.Issuer, ServerSalt: serverSalt,
			AccessTTL: accessTTL, RefreshTTL: refreshTTL,
		})
	} else {
		slog.Warn("OAuth issuance disabled (need OUROPASS_FIELD_KEY + OUROPASS_SERVER_SALT)")
	}

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: httpapi.NewRouter(deps),
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Periodic GC of expired wallet-signing nonces (short TTL → short interval,
	// not tied to epoch).
	go runNonceGC(sigCtx, walletSvc)

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
