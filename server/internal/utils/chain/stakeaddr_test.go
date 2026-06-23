package chain

import (
	"strings"
	"testing"
)

// TestBech32Encode_BIP173Vector locks the encoder to a canonical BIP-173 vector.
func TestBech32Encode_BIP173Vector(t *testing.T) {
	if got := bech32Encode("a", nil); got != "a12uel5l" {
		t.Fatalf("bech32Encode(a, empty) = %q, want a12uel5l", got)
	}
}

// TestStakeAddressFromCredential checks network prefixing, length validation, and
// determinism for the CIP-19 reward address (p12-8/TC-20).
func TestStakeAddressFromCredential(t *testing.T) {
	cred := "00112233445566778899aabbccddeeff00112233445566778899aabb" // 28 bytes

	main, err := stakeAddressFromCredential(cred, "mainnet")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(main, "stake1") {
		t.Errorf("mainnet address %q must start with stake1", main)
	}

	test, err := stakeAddressFromCredential(cred, "preview")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(test, "stake_test1") {
		t.Errorf("testnet address %q must start with stake_test1", test)
	}
	if main == test {
		t.Error("mainnet and testnet addresses must differ (network header/hrp)")
	}
	// Deterministic.
	if again, _ := stakeAddressFromCredential(cred, "mainnet"); again != main {
		t.Error("derivation must be deterministic")
	}
	// Invalid credential length is rejected.
	if _, err := stakeAddressFromCredential("aabb", "mainnet"); err == nil {
		t.Error("short credential must error")
	}
	if _, err := stakeAddressFromCredential("nothex!!", "mainnet"); err == nil {
		t.Error("non-hex credential must error")
	}
}
