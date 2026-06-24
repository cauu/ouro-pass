// Command stakehash prints the 28-byte stake credential hash for a CIP-19 reward
// (stake) address — the value to put in OUROPASS_OWNER_KEYS to admit a wallet as
// an admin owner. Usage: stakehash <stake1...|reward-address-hex>
package main

import (
	"fmt"
	"os"

	"ouro-pass/server/internal/utils/chain"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] == "" {
		fmt.Fprintln(os.Stderr, "usage: stakehash <stake1...|reward-address-hex>")
		os.Exit(2)
	}
	hash, err := chain.StakeHashFromRewardAddress(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(hash)
}
