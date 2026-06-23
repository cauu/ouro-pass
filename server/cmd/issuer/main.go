// Command issuer is the PoolOps Issuer Service entrypoint: it loads config,
// opens and migrates the database, assembles the HTTP router with its services,
// and serves until SIGINT/SIGTERM, then shuts down gracefully.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/poolops/issuer/internal/config"
	"github.com/poolops/issuer/internal/core/keys"
	"github.com/poolops/issuer/internal/core/walletauth"
	"github.com/poolops/issuer/internal/httpapi"
	"github.com/poolops/issuer/internal/store"
	"github.com/poolops/issuer/internal/utils/crypto"
)

// nonceTTL bounds wallet-signing nonce validity.
const nonceTTL = 5 * time.Minute

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

	deps := httpapi.Deps{
		Wallet: walletauth.New(st, nonceTTL),
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

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: httpapi.NewRouter(deps),
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
