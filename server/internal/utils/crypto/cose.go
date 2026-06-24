package crypto

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// CIP-30 wallets sign a challenge via `signData`, producing a COSE_Sign1
// (CIP-8). Verification reconstructs the COSE Sig_structure and checks the
// Ed25519 signature over it. We implement this directly on cbor + ed25519
// rather than via go-cose (decision D4): the Sig_structure assembly must be
// hand-controlled anyway, and a small explicit implementation is easier to
// audit for a trust-root primitive.

// coseSign1TagByte is the single-byte CBOR encoding of tag 18 (COSE_Sign1).
const coseSign1TagByte = 0xd2

// algEdDSA is the COSE algorithm identifier for EdDSA.
const algEdDSA = -8

var (
	ErrCOSEMalformed   = errors.New("cose: malformed COSE_Sign1")
	ErrCOSEAlg         = errors.New("cose: unexpected algorithm (want EdDSA)")
	ErrCOSEPayload     = errors.New("cose: payload mismatch")
	ErrCOSESignature   = errors.New("cose: signature verification failed")
	ErrCOSENoPayload   = errors.New("cose: missing payload")
)

// COSESign1 holds the parsed, relevant parts of a COSE_Sign1 message.
type COSESign1 struct {
	Protected []byte // raw protected-header bstr (signed verbatim)
	Payload   []byte // signed payload (nil if detached)
	Signature []byte
}

// ParseCOSESign1 decodes a (possibly tag-18-wrapped) COSE_Sign1 message.
func ParseCOSESign1(raw []byte) (*COSESign1, error) {
	if len(raw) > 0 && raw[0] == coseSign1TagByte {
		raw = raw[1:] // strip the single-byte COSE_Sign1 tag
	}
	var arr []cbor.RawMessage
	if err := cbor.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCOSEMalformed, err)
	}
	if len(arr) != 4 {
		return nil, fmt.Errorf("%w: expected 4 elements, got %d", ErrCOSEMalformed, len(arr))
	}
	var protected, payload, signature []byte
	if err := cbor.Unmarshal(arr[0], &protected); err != nil {
		return nil, fmt.Errorf("%w: protected: %v", ErrCOSEMalformed, err)
	}
	// arr[1] is the unprotected header map — not signed, ignored.
	// Payload may be a bstr or CBOR null (detached).
	if !isCBORNull(arr[2]) {
		if err := cbor.Unmarshal(arr[2], &payload); err != nil {
			return nil, fmt.Errorf("%w: payload: %v", ErrCOSEMalformed, err)
		}
	}
	if err := cbor.Unmarshal(arr[3], &signature); err != nil {
		return nil, fmt.Errorf("%w: signature: %v", ErrCOSEMalformed, err)
	}
	return &COSESign1{Protected: protected, Payload: payload, Signature: signature}, nil
}

// sigStructure is the COSE structure that is actually signed:
//
//	Sig_structure = ["Signature1", body_protected, external_aad, payload]
type sigStructure struct {
	_           struct{} `cbor:",toarray"`
	Context     string
	BodyProtect []byte
	ExternalAAD []byte
	Payload     []byte
}

// Verify checks the COSE_Sign1 against pubKey and, if expectedPayload is
// non-nil, that the signed payload equals it (e.g. the challenge nonce). It
// also enforces alg=EdDSA when the protected header declares one.
func (c *COSESign1) Verify(pubKey ed25519.PublicKey, expectedPayload []byte) error {
	if len(pubKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: bad public key size %d", ErrCOSEMalformed, len(pubKey))
	}
	if err := c.checkAlg(); err != nil {
		return err
	}
	if c.Payload == nil {
		return ErrCOSENoPayload
	}
	if expectedPayload != nil && !bytesEqual(c.Payload, expectedPayload) {
		return ErrCOSEPayload
	}

	toSign, err := cbor.Marshal(sigStructure{
		Context:     "Signature1",
		BodyProtect: c.Protected,
		ExternalAAD: []byte{},
		Payload:     c.Payload,
	})
	if err != nil {
		return fmt.Errorf("cose: encode Sig_structure: %w", err)
	}
	if !ed25519.Verify(pubKey, toSign, c.Signature) {
		return ErrCOSESignature
	}
	return nil
}

// checkAlg enforces the algorithm declared in the COSE protected header. CIP-8
// signs the protected header, so a CIP-30 signData message carries alg there. A
// non-empty protected header MUST declare alg=EdDSA (-8); anything else is
// rejected (p12-12/D20). An entirely empty protected header is still tolerated
// (rare/unprotected case) — the signature itself is independently verified by
// ed25519.Verify regardless.
//
// Real wallets (e.g. Vespr) also place the signing address under a STRING label
// "address" in the protected header (CIP-30 signData / CIP-8), so the header is a
// mixed int/string-keyed map; it is decoded with any-typed keys rather than
// map[int] (which would choke on the string key and be misread as a bad alg).
func (c *COSESign1) checkAlg() error {
	if len(c.Protected) == 0 {
		return nil
	}
	var hdr map[any]cbor.RawMessage
	if err := cbor.Unmarshal(c.Protected, &hdr); err != nil {
		return ErrCOSEAlg // non-empty but not a COSE header map
	}
	var algRaw cbor.RawMessage
	for k, v := range hdr {
		if n, ok := cborKeyInt(k); ok && n == 1 { // label 1 = alg
			algRaw = v
			break
		}
	}
	if algRaw == nil {
		return ErrCOSEAlg // CIP-8 signs alg in the protected header; require it
	}
	var alg int
	if err := cbor.Unmarshal(algRaw, &alg); err != nil {
		return ErrCOSEAlg
	}
	if alg != algEdDSA {
		return ErrCOSEAlg
	}
	return nil
}

// cborKeyInt coerces a CBOR map key decoded into an any to its integer label.
// fxamacker yields uint64 for non-negative and int64 for negative CBOR ints.
func cborKeyInt(k any) (int64, bool) {
	switch v := k.(type) {
	case int64:
		return v, true
	case uint64:
		return int64(v), true
	case int:
		return int64(v), true
	default:
		return 0, false
	}
}

func isCBORNull(b []byte) bool { return len(b) == 1 && (b[0] == 0xf6 || b[0] == 0xf7) }

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
