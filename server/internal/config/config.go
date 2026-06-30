// Package config loads runtime configuration from the environment.
//
// Secrets (DB DSN, encryption master key, staking-source API keys, bot tokens)
// arrive via environment variables only — never committed, never in the DB
// (see spec C3/C5). A local .env may be sourced by the operator's shell; this
// package does not parse .env files itself.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	// HTTP
	Addr            string        // listen address, e.g. ":8080"
	ShutdownTimeout time.Duration // graceful-shutdown grace period

	// Identity (network is purely an attestor property — S0014 p1-2; no global network)
	Issuer string // token `iss` + issuer deployment identity (S0006 D3): a public base URL, e.g. https://pass.example.com
	Scope  string // first-party subscription/channel/admin namespace; derived from Issuer (S0006: replaces the pool-scoped OUROPASS_POOL_ID)

	// Persistence
	DBDriver string // "sqlite" | "postgres"
	DBDSN    string // driver-specific data source name

	// Crypto material (hex/base64 from env; decoded by crypto pkg)
	FieldKeyHex   string // 32-byte AES-256-GCM master key for 🔒 fields
	ServerSaltHex string // HMAC salt for deriving the pseudonymous `sub`

	// Staking Index Adapter. Koios is the single chain origin (S0015): the issuer
	// always builds CachedSource(KoiosSource(public per-network default)); there is
	// no source-selector env. ChainAPIKey is the only optional chain env (a koios-tier
	// credential, not a source selector). Self-hosting koios is a future admin-UI
	// setting, not deploy-time env (see [[installer-scope-boundary]]).
	ChainAPIKey string

	// Telegram bots are configured in the admin console (S0017): each instance stores
	// its own token + public username. There is no telegram env (OUROPASS_TELEGRAM_BOT
	// / OUROPASS_TELEGRAM_TOKEN are removed and ignored if still set).

	// Admin
	OwnerKeyHashes []string // on-chain pool owner stake key hashes (D9)

	// Edge / security
	TrustedProxy bool // OUROPASS_TRUSTED_PROXY: parse X-Forwarded-For only when true (D15)
	TLS          bool // OUROPASS_TLS: set Secure on admin cookies (default true; D17)
}

// Default values for non-secret knobs.
const (
	defaultAddr            = ":8080"
	defaultShutdownTimeout = 15 * time.Second
	defaultDBDriver        = "sqlite"
	defaultDBDSN           = "file:ouropass.db?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
)

// Load reads configuration from the environment, applies defaults, and
// validates required fields. It returns an error rather than panicking so the
// caller controls the exit path.
func Load() (*Config, error) {
	c := &Config{
		Addr:            env("OUROPASS_ADDR", defaultAddr),
		ShutdownTimeout: defaultShutdownTimeout,
		DBDriver:        env("OUROPASS_DB_DRIVER", defaultDBDriver),
		DBDSN:           env("OUROPASS_DB_DSN", defaultDBDSN),
		FieldKeyHex:     env("OUROPASS_FIELD_KEY", ""),
		ServerSaltHex:   env("OUROPASS_SERVER_SALT", ""),
		ChainAPIKey:     env("OUROPASS_CHAIN_API_KEY", ""),
		OwnerKeyHashes:  splitCSV(env("OUROPASS_OWNER_KEYS", "")),
		TrustedProxy:    envBool("OUROPASS_TRUSTED_PROXY", false),
		TLS:             envBool("OUROPASS_TLS", true),
	}
	c.Issuer = env("OUROPASS_ISSUER", "")
	c.Scope = c.Issuer // one first-party namespace per deployment, keyed by issuer identity

	// Chain-source env was removed (S0015): Koios is the single origin (public
	// per-network defaults), so OUROPASS_CHAIN_KIND / OUROPASS_KOIOS_BASE_URL[_<NET>] /
	// OUROPASS_NODE_SOCKET / OUROPASS_CARDANO_CLI no longer exist and any legacy value
	// is silently ignored. A one-line deprecation note helps operators with stale .env.
	for _, k := range []string{
		"OUROPASS_CHAIN_KIND", "OUROPASS_KOIOS_BASE_URL",
		"OUROPASS_KOIOS_BASE_URL_MAINNET", "OUROPASS_KOIOS_BASE_URL_PREPROD", "OUROPASS_KOIOS_BASE_URL_PREVIEW",
		"OUROPASS_NODE_SOCKET", "OUROPASS_CARDANO_CLI",
	} {
		if _, ok := os.LookupEnv(k); ok {
			slog.Warn("chain-source env is deprecated and ignored (S0015): Koios is the single origin with built-in per-network endpoints", "var", k)
		}
	}
	// Telegram env was removed (S0017): bots are configured in the admin console
	// (token + username per instance), so these are ignored if still set.
	for _, k := range []string{"OUROPASS_TELEGRAM_BOT", "OUROPASS_TELEGRAM_TOKEN"} {
		if _, ok := os.LookupEnv(k); ok {
			slog.Warn("telegram env is deprecated and ignored (S0017): configure Telegram bots in the admin console (/admin → Channels)", "var", k)
		}
	}
	if _, ok := os.LookupEnv("OUROPASS_NETWORK"); ok {
		slog.Warn("OUROPASS_NETWORK is deprecated and ignored: network is now a per-attestor property set in the admin console (defaults to mainnet)")
	}

	if d := env("OUROPASS_SHUTDOWN_TIMEOUT", ""); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			return nil, fmt.Errorf("OUROPASS_SHUTDOWN_TIMEOUT: %w", err)
		}
		c.ShutdownTimeout = v
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate() error {
	switch c.DBDriver {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("invalid OUROPASS_DB_DRIVER %q (want sqlite|postgres)", c.DBDriver)
	}
	if strings.TrimSpace(c.DBDSN) == "" {
		return fmt.Errorf("OUROPASS_DB_DSN must not be empty")
	}
	if strings.TrimSpace(c.Issuer) == "" {
		return fmt.Errorf("OUROPASS_ISSUER must not be empty (token iss / issuer identity, e.g. https://pass.example.com)")
	}
	return nil
}

// envBool reads a boolean env var (1/true/yes → true), falling back to def.
func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// splitCSV splits a comma-separated env value into trimmed, non-empty entries.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
