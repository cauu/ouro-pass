package telegram

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"ouro-pass/server/internal/utils/crypto"
)

// ErrNotConfigured marks a transport call attempted before a bot token exists.
var ErrNotConfigured = errors.New("telegram: bot token not configured")

// tgConfig is the stored ChannelConfig blob for Telegram: the bot token is kept
// encrypted at rest (field cipher), never in plaintext. The bot username is not
// secret (it is public on Telegram) and is stored in the clear so deep links can
// be built to the right instance (S0005 p2-2).
type tgConfig struct {
	BotTokenEnc string `json:"bot_token_enc"`
	BotUsername string `json:"bot_username,omitempty"`
}

// EncodeToken encrypts a plaintext bot token into the stored config blob.
func EncodeToken(cipher *crypto.FieldCipher, plain string) (json.RawMessage, error) {
	return EncodeConfig(cipher, plain, "")
}

// EncodeConfig encrypts the bot token and stores the public bot username (used
// for per-instance activation deep links) in one config blob.
func EncodeConfig(cipher *crypto.FieldCipher, plain, username string) (json.RawMessage, error) {
	enc, err := cipher.Encrypt([]byte(plain))
	if err != nil {
		return nil, err
	}
	return json.Marshal(tgConfig{BotTokenEnc: hex.EncodeToString(enc), BotUsername: username})
}

// TokenHint returns a non-secret fingerprint of a bot token — its first and last
// four characters (e.g. "1234…wXyZ") — so an operator can recognise which token
// an instance holds, and tell whether it changed, without the full secret being
// returned (S0005 p7-1, a deliberate, bounded relaxation of C2). Empty for no
// token; fully masked when the token is too short to split without overexposure.
func TokenHint(plain string) string {
	if plain == "" {
		return ""
	}
	r := []rune(plain)
	if len(r) <= 8 {
		return strings.Repeat("•", len(r))
	}
	return string(r[:4]) + "…" + string(r[len(r)-4:])
}

// DecodeUsername returns the public bot username from a stored config blob (no
// decryption needed). Returns "" when absent or unparseable.
func DecodeUsername(config []byte) string {
	var c tgConfig
	if json.Unmarshal(config, &c) != nil {
		return ""
	}
	return c.BotUsername
}

// DecodeToken decrypts the bot token from a stored config blob. Returns "" (no
// error) when the blob has no token.
func DecodeToken(cipher *crypto.FieldCipher, config []byte) (string, error) {
	var c tgConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	if c.BotTokenEnc == "" {
		return "", nil
	}
	raw, err := hex.DecodeString(c.BotTokenEnc)
	if err != nil {
		return "", err
	}
	plain, err := cipher.Decrypt(raw)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
