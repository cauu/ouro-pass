package telegram

import (
	"encoding/hex"
	"encoding/json"
	"errors"

	"ouro-pass/server/internal/utils/crypto"
)

// ErrNotConfigured marks a transport call attempted before a bot token exists.
var ErrNotConfigured = errors.New("telegram: bot token not configured")

// tgConfig is the stored ChannelConfig blob for Telegram: the bot token is kept
// encrypted at rest (field cipher), never in plaintext.
type tgConfig struct {
	BotTokenEnc string `json:"bot_token_enc"`
}

// EncodeToken encrypts a plaintext bot token into the stored config blob.
func EncodeToken(cipher *crypto.FieldCipher, plain string) (json.RawMessage, error) {
	enc, err := cipher.Encrypt([]byte(plain))
	if err != nil {
		return nil, err
	}
	return json.Marshal(tgConfig{BotTokenEnc: hex.EncodeToString(enc)})
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
