package crypto

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// TestCOSEVerify_RealWalletVespr is a captured real-wallet golden vector (the
// gap D25 flagged): a CIP-30 signData response from the Vespr wallet, taken
// during S0003 manual testing. Unlike synthetic / go-cose vectors, a real wallet
// puts the signing address under a STRING label "address" in the protected
// header, making it a mixed int/string-keyed map — which previously broke
// checkAlg and rejected every real signature as "unexpected algorithm".
func TestCOSEVerify_RealWalletVespr(t *testing.T) {
	const (
		nonce      = "kHKtJTnzm3SvSWrgEPvYi9frTxqwFas0aD_DwwTNVtE"
		coseKeyHex = "a4010103272006215820649d8cf0d3c80705cc312a74185c7dc19a9e9408b41a8f4a5406fa3b0a869da4"
		sigHex     = "84582aa201276761646472657373581de0be096cb2a5a0a54208ebac4114a33add08e47bc35b1b84da83c46859a166686173686564f4582b6b484b744a546e7a6d335376535772674550765969396672547871774661733061445f447777544e5674455840b586e0b56fe56b483644f3cbcec27a828051750ad11eaec4d836ca956e4e3c09d683e0790ecb6e915018b4ecc07862cc74a9884baa6149ae04e0b10c19daf006"
		// credential embedded in the wallet's reward address (header e0 + 28 bytes)
		wantStakeHash = "be096cb2a5a0a54208ebac4114a33add08e47bc35b1b84da83c46859"
	)

	pub, err := StakeVkeyFromCOSEKey(coseKeyHex)
	if err != nil {
		t.Fatalf("extract vkey: %v", err)
	}
	raw, _ := hex.DecodeString(sigHex)
	c, err := ParseCOSESign1(raw)
	if err != nil {
		t.Fatalf("parse COSE_Sign1: %v", err)
	}
	if err := c.Verify(pub, []byte(nonce)); err != nil {
		t.Fatalf("verify real Vespr signature: %v", err)
	}
	// The recovered vkey must hash to the credential the wallet's address claims
	// (the nonce↔key binding walletauth enforces).
	if got := hex.EncodeToString(Blake2b224(pub)); got != wantStakeHash {
		t.Fatalf("blake2b224(vkey) = %s, want %s", got, wantStakeHash)
	}
	// Negative: a different payload must not verify against this signature.
	if err := c.Verify(pub, []byte("not-the-nonce")); err == nil {
		t.Fatal("tampered payload must be rejected")
	}
	// Sanity: the protected header really does carry the mixed-key "address" label.
	if !bytes.Contains(c.Protected, []byte("address")) {
		t.Fatal("expected the real wallet protected header to contain an address label")
	}
}
