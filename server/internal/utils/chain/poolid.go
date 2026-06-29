package chain

import (
	"encoding/hex"
	"strings"
)

// CanonicalPoolID normalizes a Cardano pool id to its canonical bech32 form
// ("pool1…"), so a pool configured as a 56-char hex hash compares equal to the
// bech32 form Koios returns (`delegated_pool` / stake-history `pool_id`). S0014 p4-1:
// `DeriveState` previously compared literally, so a hex-configured pool never matched
// koios's bech32 → false `StateNone`. A bech32 input is lower-cased and kept; an
// unrecognized value is returned trimmed/lower-cased (it simply won't match).
func CanonicalPoolID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// hex pool hash (28 bytes) → bech32 "pool1…"
	if h, err := hex.DecodeString(s); err == nil && len(h) == 28 {
		if conv, err := convertBits(h, 8, 5, true); err == nil {
			return bech32Encode("pool", conv)
		}
	}
	return strings.ToLower(s)
}
