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

	// Identity / network
	Network string // default network for NEW attestors (mainnet | preprod | preview); per-attestor network lives in AttestorConfig (S0006 D4)
	Issuer  string // token `iss` + issuer deployment identity (S0006 D3): a public base URL, e.g. https://pass.example.com
	Scope   string // first-party subscription/channel/admin namespace; derived from Issuer (S0006: replaces the pool-scoped OUROPASS_POOL_ID)

	// Persistence
	DBDriver string // "sqlite" | "postgres"
	DBDSN    string // driver-specific data source name

	// Crypto material (hex/base64 from env; decoded by crypto pkg)
	FieldKeyHex   string // 32-byte AES-256-GCM master key for 🔒 fields
	ServerSaltHex string // HMAC salt for deriving the pseudonymous `sub`

	// Staking Index Adapter
	ChainKind    string // mock | node_lsq | db_sync | koios | blockfrost
	KoiosBaseURL string // DEPRECATED (S0014): use per-network overrides; ignored if set, warned at load
	// KoiosBaseURLByNetwork holds optional per-network koios endpoint overrides
	// (OUROPASS_KOIOS_BASE_URL_MAINNET|_PREPROD|_PREVIEW); empty → public default per network.
	KoiosBaseURLByNetwork map[string]string
	ChainAPIKey           string
	NodeSocket            string
	CardanoCLI            string

	// Telegram
	TelegramBot   string // bot username (for deep links)
	TelegramToken string // bot API token (🔒, env only)

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
	defaultNetwork         = "preview"
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
		Network:         env("OUROPASS_NETWORK", defaultNetwork),
		DBDriver:        env("OUROPASS_DB_DRIVER", defaultDBDriver),
		DBDSN:           env("OUROPASS_DB_DSN", defaultDBDSN),
		FieldKeyHex:     env("OUROPASS_FIELD_KEY", ""),
		ServerSaltHex:   env("OUROPASS_SERVER_SALT", ""),
		ChainKind:       env("OUROPASS_CHAIN_KIND", "mock"),
		KoiosBaseURL:    env("OUROPASS_KOIOS_BASE_URL", ""),
		ChainAPIKey:     env("OUROPASS_CHAIN_API_KEY", ""),
		NodeSocket:      env("OUROPASS_NODE_SOCKET", ""),
		CardanoCLI:      env("OUROPASS_CARDANO_CLI", ""),
		TelegramBot:     env("OUROPASS_TELEGRAM_BOT", ""),
		TelegramToken:   env("OUROPASS_TELEGRAM_TOKEN", ""),
		OwnerKeyHashes:  splitCSV(env("OUROPASS_OWNER_KEYS", "")),
		TrustedProxy:    envBool("OUROPASS_TRUSTED_PROXY", false),
		TLS:             envBool("OUROPASS_TLS", true),
	}
	c.Issuer = env("OUROPASS_ISSUER", "")
	c.Scope = c.Issuer // one first-party namespace per deployment, keyed by issuer identity

	// Per-network koios endpoint overrides (S0014 p1-1). koios endpoints are per-network;
	// a single global URL is wrong for multi-network and caused false "not eligible".
	c.KoiosBaseURLByNetwork = map[string]string{}
	for _, n := range []string{"mainnet", "preprod", "preview"} {
		if v := env("OUROPASS_KOIOS_BASE_URL_"+strings.ToUpper(n), ""); v != "" {
			c.KoiosBaseURLByNetwork[n] = v
		}
	}
	if c.KoiosBaseURL != "" {
		slog.Warn("OUROPASS_KOIOS_BASE_URL is deprecated and ignored: koios endpoints are now resolved per network (built-in defaults; override via OUROPASS_KOIOS_BASE_URL_MAINNET|_PREPROD|_PREVIEW)")
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
	switch c.Network {
	case "mainnet", "preprod", "preview":
	default:
		return fmt.Errorf("invalid OUROPASS_NETWORK %q (want mainnet|preprod|preview)", c.Network)
	}
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
