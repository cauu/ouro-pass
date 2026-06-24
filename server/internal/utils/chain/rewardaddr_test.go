package chain

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestStakeHashFromRewardAddress_ThreeForms(t *testing.T) {
	credHex := strings.Repeat("ab", 28) // 28-byte stake credential
	cred, _ := hex.DecodeString(credHex)
	raw29 := append([]byte{0xe1}, cred...) // mainnet reward header 0xe0|1 + credential

	bech, err := stakeAddressFromCredential(credHex, "mainnet")
	if err != nil {
		t.Fatalf("build bech32: %v", err)
	}

	// CBOR byte-string wrap of the 29-byte address: 0x58 (bstr, 1-byte len) 0x1d (29).
	wrapped := append([]byte{0x58, 0x1d}, raw29...)

	forms := map[string]string{
		"bech32":      bech,
		"raw hex":     hex.EncodeToString(raw29),
		"cbor-bstr":   hex.EncodeToString(wrapped),
		"bech32 caps": strings.ToUpper(bech),
		"whitespace":  "  " + bech + "  ",
	}
	for name, in := range forms {
		got, err := StakeHashFromRewardAddress(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != credHex {
			t.Fatalf("%s: hash = %s, want %s", name, got, credHex)
		}
	}
}

func TestStakeHashFromRewardAddress_Rejects(t *testing.T) {
	credHex := strings.Repeat("cd", 28)
	cred, _ := hex.DecodeString(credHex)
	good, _ := stakeAddressFromCredential(credHex, "mainnet")

	cases := map[string]string{
		"empty":            "",
		"not hex/bech32":   "@@@@",
		"too short hex":    hex.EncodeToString(append([]byte{0xe1}, cred[:20]...)),
		"base addr header": hex.EncodeToString(append([]byte{0x01}, append(cred, cred...)...)), // type 0, 57 bytes
		"bad checksum":     good[:len(good)-1] + "q",
		"mixed case":       "Stake1" + good[6:],
	}
	for name, in := range cases {
		if _, err := StakeHashFromRewardAddress(in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}
