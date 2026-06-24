package chain

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// StakeHashFromRewardAddress is the inverse of stakeAddressFromCredential: it
// parses a CIP-30 reward (stake) address in any form a wallet's
// getRewardAddresses may hand back — bech32 ("stake1…"/"stake_test1…"), raw
// address hex, or a CBOR bytestring-wrapped hex — and returns the 28-byte stake
// credential hash (blake2b224(stake_vkey)) as hex. Parsing the address is kept
// server-side (S0003) so the browser forwards whatever the wallet returns and
// the format variance is absorbed here.
func StakeHashFromRewardAddress(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", fmt.Errorf("reward address empty")
	}

	var raw []byte
	if strings.HasPrefix(strings.ToLower(addr), "stake") {
		hrp, data, err := bech32Decode(addr)
		if err != nil {
			return "", err
		}
		if hrp != "stake" && hrp != "stake_test" {
			return "", fmt.Errorf("not a reward-address hrp: %s", hrp)
		}
		raw, err = convertBits(data, 5, 8, false)
		if err != nil {
			return "", err
		}
	} else {
		b, err := hex.DecodeString(addr)
		if err != nil {
			return "", fmt.Errorf("reward address neither bech32 nor hex: %w", err)
		}
		raw = b
	}

	raw = unwrapCBORBytes(raw)

	// CIP-19 reward address: 1 header byte + 28-byte credential. The header's
	// high nibble is 0b1110 (key cred) or 0b1111 (script cred).
	if len(raw) != 29 {
		return "", fmt.Errorf("reward address payload must be 29 bytes, got %d", len(raw))
	}
	if t := raw[0] >> 4; t != 0xe && t != 0xf {
		return "", fmt.Errorf("not a reward address (header type nibble 0x%x)", t)
	}
	return hex.EncodeToString(raw[1:]), nil
}

// bech32Decode validates and decodes a bech32 string into its hrp and 5-bit data
// (checksum stripped). Complements bech32Encode in stakeaddr.go.
func bech32Decode(s string) (string, []byte, error) {
	if s != strings.ToLower(s) && s != strings.ToUpper(s) {
		return "", nil, fmt.Errorf("bech32 mixed case")
	}
	s = strings.ToLower(s)
	pos := strings.LastIndexByte(s, '1')
	if pos < 1 || pos+7 > len(s) {
		return "", nil, fmt.Errorf("bech32 missing separator or too short")
	}
	hrp := s[:pos]
	data := make([]byte, 0, len(s)-pos-1)
	for i := pos + 1; i < len(s); i++ {
		idx := strings.IndexByte(bech32Charset, s[i])
		if idx < 0 {
			return "", nil, fmt.Errorf("bech32 invalid character %q", s[i])
		}
		data = append(data, byte(idx))
	}
	if bech32Polymod(append(bech32HrpExpand(hrp), data...)) != 1 {
		return "", nil, fmt.Errorf("bech32 bad checksum")
	}
	return hrp, data[:len(data)-6], nil
}

// unwrapCBORBytes strips a single CBOR byte-string head (major type 2) if present
// and the encoded length matches, else returns the input unchanged. A raw reward
// address starts with header 0xe?/0xf? (CBOR major type 7), so it is never
// mistaken for a byte-string head — no false unwrap. Minimal and dependency-free.
func unwrapCBORBytes(b []byte) []byte {
	if len(b) == 0 || b[0]>>5 != 2 { // major type 2 = byte string
		return b
	}
	info := b[0] & 0x1f
	switch {
	case info < 24:
		if n := int(info); len(b) == 1+n {
			return b[1:]
		}
	case info == 24: // 1-byte length follows
		if len(b) >= 2 {
			if n := int(b[1]); len(b) == 2+n {
				return b[2:]
			}
		}
	case info == 25: // 2-byte length follows
		if len(b) >= 3 {
			if n := int(b[1])<<8 | int(b[2]); len(b) == 3+n {
				return b[3:]
			}
		}
	}
	return b
}
