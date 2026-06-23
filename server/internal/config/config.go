// Package config loads runtime configuration from the environment.
//
// Secrets (DB DSN, encryption master key, staking-source API keys, bot tokens)
// arrive via environment variables only — never committed, never in the DB
// (see spec C3/C5). A local .env may be sourced by the operator's shell; this
// package does not parse .env files itself.
package config

import (
	"fmt"
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
	PoolID  string // bech32 pool id this issuer serves
	Network string // mainnet | preprod | preview
	Issuer  string // token `iss`, e.g. "poolops:<pool_id>"

	// Persistence
	DBDriver string // "sqlite" | "postgres"
	DBDSN    string // driver-specific data source name

	// Crypto material (hex/base64 from env; decoded by crypto pkg)
	FieldKeyHex   string // 32-byte AES-256-GCM master key for 🔒 fields
	ServerSaltHex string // HMAC salt for deriving the pseudonymous `sub`

	// Staking Index Adapter
	ChainKind    string // mock | node_lsq | db_sync | koios | blockfrost
	KoiosBaseURL string
	ChainAPIKey  string
	NodeSocket   string
	CardanoCLI   string
}

// Default values for non-secret knobs.
const (
	defaultAddr            = ":8080"
	defaultShutdownTimeout = 15 * time.Second
	defaultNetwork         = "preview"
	defaultDBDriver        = "sqlite"
	defaultDBDSN           = "file:poolops.db?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
)

// Load reads configuration from the environment, applies defaults, and
// validates required fields. It returns an error rather than panicking so the
// caller controls the exit path.
func Load() (*Config, error) {
	c := &Config{
		Addr:            env("POOLOPS_ADDR", defaultAddr),
		ShutdownTimeout: defaultShutdownTimeout,
		PoolID:          env("POOLOPS_POOL_ID", ""),
		Network:         env("POOLOPS_NETWORK", defaultNetwork),
		DBDriver:        env("POOLOPS_DB_DRIVER", defaultDBDriver),
		DBDSN:           env("POOLOPS_DB_DSN", defaultDBDSN),
		FieldKeyHex:     env("POOLOPS_FIELD_KEY", ""),
		ServerSaltHex:   env("POOLOPS_SERVER_SALT", ""),
		ChainKind:       env("POOLOPS_CHAIN_KIND", "mock"),
		KoiosBaseURL:    env("POOLOPS_KOIOS_BASE_URL", ""),
		ChainAPIKey:     env("POOLOPS_CHAIN_API_KEY", ""),
		NodeSocket:      env("POOLOPS_NODE_SOCKET", ""),
		CardanoCLI:      env("POOLOPS_CARDANO_CLI", ""),
	}
	c.Issuer = env("POOLOPS_ISSUER", "poolops:"+c.PoolID)

	if d := env("POOLOPS_SHUTDOWN_TIMEOUT", ""); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			return nil, fmt.Errorf("POOLOPS_SHUTDOWN_TIMEOUT: %w", err)
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
		return fmt.Errorf("invalid POOLOPS_NETWORK %q (want mainnet|preprod|preview)", c.Network)
	}
	switch c.DBDriver {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("invalid POOLOPS_DB_DRIVER %q (want sqlite|postgres)", c.DBDriver)
	}
	if strings.TrimSpace(c.DBDSN) == "" {
		return fmt.Errorf("POOLOPS_DB_DSN must not be empty")
	}
	return nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
