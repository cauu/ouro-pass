package chain

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Stake (reward) addresses are bech32 of [header || 28-byte credential] per
// CIP-19. The identity we persist is hex(blake2b224(stake_vkey)) — the bare
// credential hash — but staking data sources (Koios, cardano-cli) key on the
// bech32 stake address, so adapters convert before querying (p12-8/D14). A small
// bech32 encoder is implemented here to avoid adding a dependency.

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// stakeAddressFromCredential builds the CIP-19 bech32 reward (stake) address for
// a key-hash stake credential on the given network. credentialHashHex is the
// 28-byte blake2b224(stake_vkey) in hex.
func stakeAddressFromCredential(credentialHashHex, network string) (string, error) {
	cred, err := hex.DecodeString(credentialHashHex)
	if err != nil {
		return "", fmt.Errorf("stake credential not hex: %w", err)
	}
	if len(cred) != 28 {
		return "", fmt.Errorf("stake credential must be 28 bytes, got %d", len(cred))
	}
	// Reward address header: type 14 (0b1110, stake-key credential) in the high
	// nibble, network id in the low nibble (mainnet=1, testnet=0).
	var netID byte
	hrp := "stake_test"
	if network == "mainnet" {
		netID = 1
		hrp = "stake"
	}
	payload := append([]byte{0xe0 | netID}, cred...)
	conv, err := convertBits(payload, 8, 5, true)
	if err != nil {
		return "", err
	}
	return bech32Encode(hrp, conv), nil
}

func bech32Encode(hrp string, data []byte) string {
	combined := append(append([]byte{}, data...), bech32Checksum(hrp, data)...)
	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, v := range combined {
		b.WriteByte(bech32Charset[v])
	}
	return b.String()
}

func bech32Polymod(values []byte) uint32 {
	gen := []uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func bech32HrpExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

func bech32Checksum(hrp string, data []byte) []byte {
	values := append(bech32HrpExpand(hrp), data...)
	values = append(values, 0, 0, 0, 0, 0, 0)
	polymod := bech32Polymod(values) ^ 1
	out := make([]byte, 6)
	for i := 0; i < 6; i++ {
		out[i] = byte((polymod >> uint(5*(5-i))) & 31)
	}
	return out
}

// convertBits regroups bytes between bit widths (8↔5) for bech32.
func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	var acc uint32
	var bits uint
	var out []byte
	maxv := uint32((1 << toBits) - 1)
	for _, b := range data {
		acc = (acc << fromBits) | uint32(b)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			out = append(out, byte((acc>>bits)&maxv))
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, fmt.Errorf("invalid padding in convertBits")
	}
	return out, nil
}
