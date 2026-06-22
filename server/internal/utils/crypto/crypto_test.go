package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func TestBlake2b224_LengthAndDeterminism(t *testing.T) {
	a := Blake2b224([]byte("hello"))
	b := Blake2b224([]byte("hello"))
	if len(a) != 28 {
		t.Fatalf("digest len = %d, want 28", len(a))
	}
	if !bytes.Equal(a, b) {
		t.Fatal("not deterministic")
	}
	if bytes.Equal(a, Blake2b224([]byte("world"))) {
		t.Fatal("collision on distinct inputs")
	}
	// Known vector: blake2b-224("") .
	empty := hex.EncodeToString(Blake2b224(nil))
	if empty != "836cc68931c2e4e3e838602eca1902591d216837bafddfe6f0c8cb07" {
		t.Fatalf("blake2b224(\"\") = %s (unexpected)", empty)
	}
}

func TestDeriveSub_DeterministicAndSaltSensitive(t *testing.T) {
	sch := []byte("stake-credential-hash-bytes")
	s1 := DeriveSub([]byte("salt-A"), sch)
	s2 := DeriveSub([]byte("salt-A"), sch)
	if s1 != s2 {
		t.Fatal("sub not deterministic for same salt+hash")
	}
	if s1 == DeriveSub([]byte("salt-B"), sch) {
		t.Fatal("sub must change with salt")
	}
	if s1 == "" {
		t.Fatal("empty sub")
	}
}

func TestFieldCipher_RoundTripAndTamper(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	c, err := NewFieldCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	pt := []byte("super-secret-bot-token")
	blob, err := c.Encrypt(pt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, pt) {
		t.Fatal("plaintext leaked into ciphertext")
	}
	got, err := c.Decrypt(blob)
	if err != nil || !bytes.Equal(got, pt) {
		t.Fatalf("round-trip failed: %v / %q", err, got)
	}
	// Tamper → auth failure.
	blob[len(blob)-1] ^= 0xff
	if _, err := c.Decrypt(blob); err == nil {
		t.Fatal("tampered ciphertext authenticated")
	}
	if _, err := NewFieldCipher(key[:16]); err == nil {
		t.Fatal("accepted non-32-byte key")
	}
}

// makeCOSESign1 builds a CIP-8 COSE_Sign1 over payload using priv, mirroring
// what a CIP-30 wallet's signData produces. alg goes in the protected header.
func makeCOSESign1(t *testing.T, priv ed25519.PrivateKey, payload []byte, alg int, tagged bool) []byte {
	t.Helper()
	protected, err := cbor.Marshal(map[int]int{1: alg})
	if err != nil {
		t.Fatal(err)
	}
	toSign, err := cbor.Marshal(sigStructure{
		Context: "Signature1", BodyProtect: protected, ExternalAAD: []byte{}, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, toSign)
	msg, err := cbor.Marshal([]any{protected, map[int]int{}, payload, sig})
	if err != nil {
		t.Fatal(err)
	}
	if tagged {
		msg = append([]byte{coseSign1TagByte}, msg...)
	}
	return msg
}

func TestCOSEVerify_ValidAndTampered(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	nonce := []byte("challenge-nonce-12345")

	for _, tagged := range []bool{false, true} {
		raw := makeCOSESign1(t, priv, nonce, algEdDSA, tagged)
		c, err := ParseCOSESign1(raw)
		if err != nil {
			t.Fatalf("tagged=%v parse: %v", tagged, err)
		}
		if err := c.Verify(pub, nonce); err != nil {
			t.Fatalf("tagged=%v verify valid: %v", tagged, err)
		}
		// Wrong expected payload.
		if err := c.Verify(pub, []byte("other")); err != ErrCOSEPayload {
			t.Fatalf("tagged=%v want ErrCOSEPayload, got %v", tagged, err)
		}
		// Wrong key.
		otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
		if err := c.Verify(otherPub, nonce); err != ErrCOSESignature {
			t.Fatalf("tagged=%v want ErrCOSESignature, got %v", tagged, err)
		}
	}

	// Tampered signature byte.
	raw := makeCOSESign1(t, priv, nonce, algEdDSA, false)
	c, _ := ParseCOSESign1(raw)
	c.Signature[0] ^= 0xff
	if err := c.Verify(pub, nonce); err != ErrCOSESignature {
		t.Fatalf("tampered sig: want ErrCOSESignature, got %v", err)
	}

	// Wrong algorithm in protected header → rejected.
	rawBadAlg := makeCOSESign1(t, priv, nonce, -7 /* ES256 */, false)
	cBad, _ := ParseCOSESign1(rawBadAlg)
	if err := cBad.Verify(pub, nonce); err != ErrCOSEAlg {
		t.Fatalf("bad alg: want ErrCOSEAlg, got %v", err)
	}
}
