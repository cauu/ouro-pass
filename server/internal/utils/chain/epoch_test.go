package chain

import (
	"testing"
	"time"
)

func TestCurrentEpoch(t *testing.T) {
	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}

	// mainnet: the first Shelley epoch (208) began 2020-07-29T21:44:51Z; the linear
	// Byron+Shelley formula must place that instant exactly at epoch 208.
	if e, ok := CurrentEpoch("mainnet", at("2020-07-29T21:44:51Z")); !ok || e != 208 {
		t.Fatalf("mainnet shelley start: e=%d ok=%v, want 208", e, ok)
	}
	// One second before the boundary is still epoch 207.
	if e, ok := CurrentEpoch("mainnet", at("2020-07-29T21:44:50Z")); !ok || e != 207 {
		t.Fatalf("mainnet pre-boundary: e=%d ok=%v, want 207", e, ok)
	}

	// preview: genesis is epoch 0; +1 day = epoch 1; the day boundary increments.
	if e, ok := CurrentEpoch("preview", at("2022-10-25T00:00:00Z")); !ok || e != 0 {
		t.Fatalf("preview genesis: e=%d ok=%v, want 0", e, ok)
	}
	if e, ok := CurrentEpoch("preview", at("2022-10-26T00:00:00Z")); !ok || e != 1 {
		t.Fatalf("preview +1d: e=%d ok=%v, want 1", e, ok)
	}
	if e, ok := CurrentEpoch("preview", at("2022-10-25T23:59:59Z")); !ok || e != 0 {
		t.Fatalf("preview pre-boundary: e=%d ok=%v, want 0", e, ok)
	}

	// preprod: genesis epoch 0; +5 days = epoch 1.
	if e, ok := CurrentEpoch("preprod", at("2022-06-06T00:00:00Z")); !ok || e != 1 {
		t.Fatalf("preprod +5d: e=%d ok=%v, want 1", e, ok)
	}

	// Unknown network / before genesis → ok=false (caller falls back to live).
	if _, ok := CurrentEpoch("regtest", at("2025-01-01T00:00:00Z")); ok {
		t.Fatal("unknown network must return ok=false")
	}
	if _, ok := CurrentEpoch("preview", at("2000-01-01T00:00:00Z")); ok {
		t.Fatal("pre-genesis must return ok=false")
	}
}
