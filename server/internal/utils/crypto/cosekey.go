package crypto

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// A CIP-30 signData response carries the signer's public key in its `key` field
// as a COSE_Key (RFC 8152), NOT as a bare verification key — and the wallet
// exposes the stake vkey no other way (getRewardAddresses yields only the
// hash). The issuer therefore recovers the vkey here, server-side, rather than
// trusting a client-sent vkey (S0003: browser stays CBOR-free, one audited COSE
// implementation in Go). Security: extraction alone proves nothing — the caller
// MUST still verify the COSE_Sign1 signature AND that blake2b224(vkey) equals the
// challenge-bound stake hash, because the stake vkey is public once it has
// witnessed an on-chain certificate.

// COSE_Key labels/values for an Ed25519 (OKP) public key (RFC 8152 §7, §13.2).
const (
	coseKeyLabelKty = 1  // kty
	coseKeyLabelCrv = -1 // crv (OKP key-type parameter)
	coseKeyLabelX   = -2 // x   (public key bytes)
	coseKtyOKP      = 1  // kty value: OKP
	coseCrvEd25519  = 6  // crv value: Ed25519

	// maxCOSEKeyBytes bounds a decoded COSE_Key to blunt hostile CBOR; a real
	// Ed25519 COSE_Key is ~45 bytes.
	maxCOSEKeyBytes = 512
)

var (
	ErrCOSEKeyMalformed = errors.New("cose: malformed COSE_Key")
	ErrCOSEKeyType      = errors.New("cose: COSE_Key is not an Ed25519 (OKP) public key")
)

// StakeVkeyFromCOSEKey extracts the raw 32-byte Ed25519 stake verification key
// from a hex-encoded COSE_Key (the `key` field of a CIP-30 signData response).
// It enforces kty=OKP, crv=Ed25519, and a 32-byte x so a hostile or malformed
// key cannot smuggle in a different curve or length. It decodes into a map (not
// fixed byte offsets) so wallet differences in label order / extra labels (e.g.
// kid) / integer encoding are tolerated.
func StakeVkeyFromCOSEKey(coseKeyHex string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(coseKeyHex)
	if err != nil {
		return nil, fmt.Errorf("%w: not hex: %v", ErrCOSEKeyMalformed, err)
	}
	if len(raw) == 0 || len(raw) > maxCOSEKeyBytes {
		return nil, fmt.Errorf("%w: size %d out of range", ErrCOSEKeyMalformed, len(raw))
	}
	var m map[int]cbor.RawMessage
	if err := cbor.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCOSEKeyMalformed, err)
	}
	if err := requireIntLabel(m, coseKeyLabelKty, coseKtyOKP); err != nil {
		return nil, ErrCOSEKeyType
	}
	if err := requireIntLabel(m, coseKeyLabelCrv, coseCrvEd25519); err != nil {
		return nil, ErrCOSEKeyType
	}
	xRaw, ok := m[coseKeyLabelX]
	if !ok {
		return nil, fmt.Errorf("%w: missing x (-2)", ErrCOSEKeyMalformed)
	}
	var x []byte
	if err := cbor.Unmarshal(xRaw, &x); err != nil {
		return nil, fmt.Errorf("%w: x: %v", ErrCOSEKeyMalformed, err)
	}
	if len(x) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: x len %d, want %d", ErrCOSEKeyMalformed, len(x), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(x), nil
}

// requireIntLabel asserts that map label is present and decodes to want.
func requireIntLabel(m map[int]cbor.RawMessage, label, want int) error {
	raw, ok := m[label]
	if !ok {
		return fmt.Errorf("missing label %d", label)
	}
	var got int
	if err := cbor.Unmarshal(raw, &got); err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("label %d = %d, want %d", label, got, want)
	}
	return nil
}
