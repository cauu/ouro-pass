package chain

import (
	"strings"
	"testing"
)

// TestCanonicalPoolID covers S0014 p4-1 / TC-8: a hex pool hash and its bech32 form
// canonicalize to the same value (so DeriveState matches either against koios's bech32).
func TestCanonicalPoolID(t *testing.T) {
	hexPool := strings.Repeat("ab", 28) // 28-byte pool hash in hex
	bech := CanonicalPoolID(hexPool)

	if !strings.HasPrefix(bech, "pool1") || len(bech) != 56 {
		t.Fatalf("CanonicalPoolID(hex) = %q (len %d), want a 56-char pool1… id", bech, len(bech))
	}
	if CanonicalPoolID(bech) != bech {
		t.Errorf("not idempotent on bech32: %q -> %q", bech, CanonicalPoolID(bech))
	}
	if CanonicalPoolID(hexPool) != CanonicalPoolID(bech) {
		t.Error("hex and its bech32 form must canonicalize equal")
	}
	if CanonicalPoolID(strings.ToUpper(bech)) != bech {
		t.Error("bech32 comparison must be case-insensitive")
	}
	if CanonicalPoolID("  "+hexPool+"  ") != bech {
		t.Error("surrounding whitespace must be trimmed")
	}
	if CanonicalPoolID("") != "" {
		t.Error("empty stays empty")
	}
}
