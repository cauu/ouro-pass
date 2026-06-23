package chain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// KoiosSource queries the Koios REST API (a third-party convenience source with
// the same capability as db-sync). Blockfrost is structurally identical and can
// reuse this shape.
type KoiosSource struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewKoiosSource builds a Koios source; baseURL defaults to mainnet if empty.
func NewKoiosSource(baseURL, apiKey string) *KoiosSource {
	if baseURL == "" {
		baseURL = "https://api.koios.rest/api/v1"
	}
	return &KoiosSource{baseURL: baseURL, apiKey: apiKey, client: &http.Client{Timeout: 15 * time.Second}}
}

// Name returns "koios".
func (k *KoiosSource) Name() string { return "koios" }

// koiosAccountInfo is the relevant subset of Koios /account_info.
type koiosAccountInfo struct {
	StakeAddress     string `json:"stake_address"`
	Status           string `json:"status"`
	DelegatedPool    string `json:"delegated_pool"`
	TotalBalance     string `json:"total_balance"`
	Rewards          string `json:"rewards_available"`
}

// koiosTip is the relevant subset of Koios /tip.
type koiosTip struct {
	Epoch uint64 `json:"epoch_no"`
}

// Snapshot fetches account info for a stake credential. The credential is
// passed to Koios as the `_stake_addresses` parameter (callers supply the
// bech32 stake address form for real queries).
func (k *KoiosSource) Snapshot(ctx context.Context, stakeCredentialHash string) (*Snapshot, error) {
	body, _ := json.Marshal(map[string][]string{"_stake_addresses": {stakeCredentialHash}})
	var infos []koiosAccountInfo
	if err := k.post(ctx, "/account_info", body, &infos); err != nil {
		return nil, err
	}
	epoch, _ := k.Epoch(ctx)
	if len(infos) == 0 {
		return &Snapshot{StakeCredentialHash: stakeCredentialHash, Epoch: epoch, Source: "koios", FetchedAt: time.Now().UTC()}, nil
	}
	return koiosToSnapshot(stakeCredentialHash, epoch, infos[0]), nil
}

// koiosToSnapshot maps a Koios account_info row to a Snapshot (pure; unit-tested).
func koiosToSnapshot(hash string, epoch uint64, in koiosAccountInfo) *Snapshot {
	pool := in.DelegatedPool
	if in.Status != "registered" {
		pool = ""
	}
	return &Snapshot{
		StakeCredentialHash: hash,
		Epoch:               epoch,
		DelegatedPoolID:     pool,
		ActiveStakeLovelace: in.TotalBalance,
		RewardsLovelace:     in.Rewards,
		Source:              "koios",
		FetchedAt:           time.Now().UTC(),
	}
}

// Epoch fetches the current epoch from Koios /tip.
func (k *KoiosSource) Epoch(ctx context.Context) (uint64, error) {
	var tips []koiosTip
	if err := k.get(ctx, "/tip", &tips); err != nil {
		return 0, err
	}
	if len(tips) == 0 {
		return 0, fmt.Errorf("koios: empty /tip")
	}
	return tips[0].Epoch, nil
}

func (k *KoiosSource) get(ctx context.Context, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, k.baseURL+path, nil)
	return k.do(req, out)
}

func (k *KoiosSource) post(ctx context.Context, path string, body []byte, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, k.baseURL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return k.do(req, out)
}

func (k *KoiosSource) do(req *http.Request, out any) error {
	if k.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+k.apiKey)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("koios %s: status %d: %s", req.URL.Path, resp.StatusCode, string(data))
	}
	return json.Unmarshal(data, out)
}
