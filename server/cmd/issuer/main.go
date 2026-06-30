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
	"ouro-pass/server/internal/domain"
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

// telegramReconcileInterval is how often the supervisor reconciles the running
// telegram workers against the active channel-instance set (S0005 p2-1).
const telegramReconcileInterval = 5 * time.Second

// envInstanceID is the synthetic instance id for the OUROPASS_TELEGRAM_TOKEN
// fallback "default" instance (D6).
const envInstanceID = "env-default"

const (
	nonceTTL        = 5 * time.Minute
	nonceGCInterval = 10 * time.Minute // independent of epoch; nonces are minute-scale
	accessTTL       = 24 * time.Hour
	refreshTTL      = 30 * 24 * time.Hour
)

func main() {
	// CLI subcommands run before server setup so they emit no log noise. This lets
	// operators running only the published image compute their owner key hash:
	//   docker run --rm ghcr.io/cauu/ouro-pass stake-hash stake1...
	if len(os.Args) > 1 && os.Args[1] == "stake-hash" {
		stakeHashCmd(os.Args[2:])
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// stakeHashCmd prints the 28-byte stake credential hash for a CIP-19 reward
// (stake) address — the value to put in OUROPASS_OWNER_KEYS. Mirrors the
// standalone cmd/stakehash so the issuer image alone is enough.
func stakeHashCmd(args []string) {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: issuer stake-hash <stake1...|reward-address-hex>")
		os.Exit(2)
	}
	hash, err := chain.StakeHashFromRewardAddress(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(hash)
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

	deps, err := buildServices(cfg, st, nil)
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
		// S0005 p2-1: a supervisor runs one telegram worker per active DB instance,
		// each bound to its own decrypted token, processor (instance-scoped), and
		// long-poll offset. It reconciles every tick, so adding/removing/disabling
		// or re-tokening an instance takes effect with no restart (D3/C4). The env
		// token OUROPASS_TELEGRAM_TOKEN remains an implicit "default" instance that
		// runs only while no DB instance exists (D6/C1).
		factory := func(inst domain.ChannelConfig) (telegram.Runner, error) {
			token, err := instanceToken(cfg, deps.Cipher, inst)
			if err != nil {
				return nil, err
			}
			tok := token // capture per instance (static token, independent offset)
			transport := telegram.NewBotAPITransport(func() string { return tok })
			proc := telegram.NewInstanceProcessor(st, deps.OAuth, cfg.Scope, inst.ChannelID)
			return telegram.NewWorker(proc, transport), nil
		}
		var envInstance *domain.ChannelConfig
		if cfg.TelegramToken != "" {
			envInstance = &domain.ChannelConfig{
				ChannelID: envInstanceID, PoolID: cfg.Scope, ChannelType: "telegram", Name: "default", Status: "active",
			}
		}
		supervisor := telegram.NewSupervisor(st, factory, cfg.Scope, telegramReconcileInterval, envInstance)
		startWorker("telegram-supervisor", func() { supervisor.Run(sigCtx) })

		// The push worker delivers admin-created PushJobs. A channel-scoped job is
		// routed through its target instance's transport; an unscoped (legacy) job
		// uses a token resolved live (env first, else the default DB instance).
		pushDefaultTokenFn := func() string {
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
		defaultSender := telegram.NewBotAPITransport(pushDefaultTokenFn)
		pushRoute := func(job domain.PushJob) (push.Sender, error) {
			if job.ChannelID == nil || *job.ChannelID == "" {
				return defaultSender, nil
			}
			inst, err := st.Channels().Get(context.Background(), *job.ChannelID)
			if err != nil {
				return nil, err
			}
			tok, err := instanceToken(cfg, deps.Cipher, *inst)
			if err != nil {
				return nil, err
			}
			return telegram.NewBotAPITransport(func() string { return tok }), nil
		}
		pushWorker := push.NewWorker(st, defaultSender, pushPollInterval, push.Options{Route: pushRoute})
		startWorker("push", func() { pushWorker.Run(sigCtx) })

		// Watch each in-use network's epoch (S0014 p1-3): epoch is per-network, so the
		// reconcile trigger must follow whichever networks the active attestors use.
		networksFor := func(ctx context.Context) ([]string, error) {
			cfgs, err := st.Attestors().ListActive(ctx)
			if err != nil {
				return nil, err
			}
			return attestor.DistinctNetworks(cfgs), nil
		}
		recon := reconciliation.New(st, deps.OAuth, deps.SrcFor, networksFor, cfg.Scope)
		startWorker("reconciliation", func() { recon.Run(sigCtx, epochPollInterval) })
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("issuer listening", "addr", cfg.Addr)
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
//
// chainOverride is the test seam (S0015): when non-nil it is used as the raw
// chain source for every network (still wrapped with the active-membership
// cache), so tests inject a deterministic chain.MockSource without env. In
// production it is nil and each network builds its public Koios source.
func buildServices(cfg *config.Config, st *store.Store, chainOverride chain.Source) (httpapi.Deps, error) {
	walletSvc := walletauth.New(st, nonceTTL)
	var serverSalt []byte
	if cfg.ServerSaltHex != "" {
		var err error
		if serverSalt, err = hex.DecodeString(cfg.ServerSaltHex); err != nil {
			return httpapi.Deps{}, fmt.Errorf("OUROPASS_SERVER_SALT: %w", err)
		}
	}
	deps := httpapi.Deps{
		Wallet:        walletSvc,
		Store:         st,
		PoolID:        cfg.Scope,
		TelegramBot:   cfg.TelegramBot,
		TrustedProxy:  cfg.TrustedProxy,
		SecureCookies: cfg.TLS,
		Admin: admin.New(admin.Config{
			Wallet: walletSvc, Store: st, OwnerKeyHash: cfg.OwnerKeyHashes, PoolID: cfg.Scope,
		}),
	}
	if cfg.FieldKeyHex != "" {
		cipher, err := crypto.NewFieldCipherHex(cfg.FieldKeyHex)
		if err != nil {
			return httpapi.Deps{}, err
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
			network = "mainnet" // default for attestors that omit it (S0014 p1-2)
		}
		srcMu.Lock()
		defer srcMu.Unlock()
		if s, ok := srcCache[network]; ok {
			return s, nil
		}
		// Koios is the single chain origin (S0015): build the per-network public Koios
		// source directly (DefaultKoiosBaseURL resolves the right endpoint per network).
		// Tests inject a deterministic source via chainOverride instead.
		var raw chain.Source
		if chainOverride != nil {
			raw = chainOverride
		} else {
			raw = chain.NewKoiosSource(chain.DefaultKoiosBaseURL(network), cfg.ChainAPIKey, network)
		}
		s := membership.NewCachedSource(raw, st.SnapshotCache(), network, 10*time.Second)
		srcCache[network] = s
		return s, nil
	}
	chainSrc, err := srcFor("mainnet") // fallback default-network source for admin delegator roster
	if err != nil {
		return httpapi.Deps{}, err
	}
	deps.Chain = chainSrc
	deps.SrcFor = srcFor // per-network source resolution (S0014 p1-3)
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
	return deps, nil
}

// instanceToken resolves the plaintext bot token for one telegram instance: the
// env token for the synthetic "default" instance (D6), else the instance's own
// field-encrypted token from its stored config. An empty token is an error so
// the supervisor skips the instance and retries once it is configured.
func instanceToken(cfg *config.Config, cipher *crypto.FieldCipher, inst domain.ChannelConfig) (string, error) {
	if inst.ChannelID == envInstanceID {
		if cfg.TelegramToken == "" {
			return "", fmt.Errorf("env telegram token empty")
		}
		return cfg.TelegramToken, nil
	}
	if cipher == nil {
		return "", fmt.Errorf("field cipher unavailable; cannot decrypt instance token")
	}
	tok, err := telegram.DecodeToken(cipher, inst.Config)
	if err != nil {
		return "", err
	}
	if tok == "" {
		return "", fmt.Errorf("instance %s has no bot token", inst.ChannelID)
	}
	return tok, nil
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
