package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/fxamacker/cbor/v2"
	cose "github.com/veraison/go-cose"
)

// TestStakeVkeyFromCOSEKey_InteropWithGoCose feeds a COSE_Key produced by an
// INDEPENDENT implementation (veraison/go-cose) and asserts our extractor
// recovers the exact 32-byte vkey — the cross-impl check for the `key` field,
// the analogue of the COSE_Sign1 interop test (p14-7) for S0003.
func TestStakeVkeyFromCOSEKey_InteropWithGoCose(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)

	k, err := cose.NewKeyFromPublic(pub)
	if err != nil {
		t.Fatalf("go-cose NewKeyFromPublic: %v", err)
	}
	raw, err := k.MarshalCBOR()
	if err != nil {
		t.Fatalf("go-cose Key MarshalCBOR: %v", err)
	}

	got, err := StakeVkeyFromCOSEKey(hex.EncodeToString(raw))
	if err != nil {
		t.Fatalf("extract from independent COSE_Key: %v", err)
	}
	if !bytes.Equal(got, pub) {
		t.Fatalf("extracted vkey mismatch:\n got %x\nwant %x", got, pub)
	}
}

// TestStakeVkeyFromCOSEKey_LabelOrderAndExtras covers wallet encoding variance:
// label order must not matter and unknown labels (e.g. kid) must be ignored —
// the reason we decode into a map rather than fixed byte offsets.
func TestStakeVkeyFromCOSEKey_LabelOrderAndExtras(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	x := []byte(pub)

	// fxamacker sorts map keys canonically on marshal, but the decoder is
	// order-agnostic; include alg(3) and a kid(4) the parser must ignore.
	raw, err := cbor.Marshal(map[int]any{
		coseKeyLabelX:   x,
		coseKeyLabelCrv: coseCrvEd25519,
		coseKeyLabelKty: coseKtyOKP,
		3:               algEdDSA,
		4:               []byte("some-kid"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := StakeVkeyFromCOSEKey(hex.EncodeToString(raw))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !bytes.Equal(got, pub) {
		t.Fatalf("vkey mismatch with extra labels")
	}
}

func TestStakeVkeyFromCOSEKey_Rejects(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	x := []byte(pub)
	mk := func(m map[int]any) string {
		b, err := cbor.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(b)
	}

	cases := map[string]string{
		"wrong kty (EC2)":  mk(map[int]any{coseKeyLabelKty: 2, coseKeyLabelCrv: coseCrvEd25519, coseKeyLabelX: x}),
		"wrong crv (P256)": mk(map[int]any{coseKeyLabelKty: coseKtyOKP, coseKeyLabelCrv: 1, coseKeyLabelX: x}),
		"missing kty":      mk(map[int]any{coseKeyLabelCrv: coseCrvEd25519, coseKeyLabelX: x}),
		"missing x":        mk(map[int]any{coseKeyLabelKty: coseKtyOKP, coseKeyLabelCrv: coseCrvEd25519}),
		"short x":          mk(map[int]any{coseKeyLabelKty: coseKtyOKP, coseKeyLabelCrv: coseCrvEd25519, coseKeyLabelX: x[:31]}),
		"not hex":          "zzzz",
		"empty":            "",
		"truncated cbor":   "a101",
		"not a map":        hex.EncodeToString(mustCBOR(t, "a string")),
	}
	for name, in := range cases {
		if _, err := StakeVkeyFromCOSEKey(in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func mustCBOR(t *testing.T, v any) []byte {
	t.Helper()
	b, err := cbor.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
