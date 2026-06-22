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

// checkAlg parses the protected header map and, if it carries an alg label (1),
// requires EdDSA (-8). A protected header that is empty/unparseable for alg is
// tolerated (some wallets put alg only in the unprotected header).
func (c *COSESign1) checkAlg() error {
	if len(c.Protected) == 0 {
		return nil
	}
	var hdr map[int]cbor.RawMessage
	if err := cbor.Unmarshal(c.Protected, &hdr); err != nil {
		return nil // not an int-keyed map; skip strict alg enforcement
	}
	raw, ok := hdr[1] // label 1 = alg
	if !ok {
		return nil
	}
	var alg int
	if err := cbor.Unmarshal(raw, &alg); err != nil {
		return nil
	}
	if alg != algEdDSA {
		return ErrCOSEAlg
	}
	return nil
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
