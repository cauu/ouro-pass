package telegram

import (
	"strings"
	"testing"

	"ouro-pass/server/internal/utils/crypto"
)

func TestTokenCodec_RoundTripAndEncrypted(t *testing.T) {
	cipher, err := crypto.NewFieldCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	const token = "123456:ABC-DEF_ghIklmnoPQRstuvwxyz"

	blob, err := EncodeToken(cipher, token)
	if err != nil {
		t.Fatal(err)
	}
	// The stored blob must NOT contain the plaintext token (encrypted at rest).
	if strings.Contains(string(blob), token) {
		t.Fatalf("stored blob leaks plaintext token: %s", blob)
	}
	if !strings.Contains(string(blob), "bot_token_enc") {
		t.Fatalf("blob shape wrong: %s", blob)
	}

	got, err := DecodeToken(cipher, blob)
	if err != nil || got != token {
		t.Fatalf("round-trip: got %q err %v, want %q", got, err, token)
	}

	// Empty/blank blob → empty token, no error.
	if got, err := DecodeToken(cipher, []byte(`{}`)); err != nil || got != "" {
		t.Fatalf("empty blob: got %q err %v", got, err)
	}
}
