package chain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// KoiosSource queries the Koios REST API (a third-party convenience source with
// the same capability as db-sync). Blockfrost is structurally identical and can
// reuse this shape.
type KoiosSource struct {
	baseURL string
	apiKey  string
	network string
	client  *http.Client
}

// NewKoiosSource builds a Koios source; baseURL defaults to mainnet if empty.
// Surrounding whitespace and trailing slashes are trimmed so a value like
// "https://api.koios.rest/api/v1/" doesn't produce "…/v1//account_info" (which koios
// 404s as "Query not found"). p2-1.
func NewKoiosSource(baseURL, apiKey, network string) *KoiosSource {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.koios.rest/api/v1"
	}
	return &KoiosSource{baseURL: baseURL, apiKey: apiKey, network: network, client: &http.Client{Timeout: 15 * time.Second}}
}

// Name returns "koios".
func (k *KoiosSource) Name() string { return "koios" }

// koiosAccountInfo is the relevant subset of Koios /account_info (the *live*
// delegation + registration status).
type koiosAccountInfo struct {
	StakeAddress  string `json:"stake_address"`
	Status        string `json:"status"`
	DelegatedPool string `json:"delegated_pool"`
	Rewards       string `json:"rewards_available"`
}

// koiosStakeHistory is one row of Koios /account_stake_history: the *effective
// active stake* this credential contributed to a pool in a given epoch. This is
// the truth for `active` membership (replaces the total_balance approximation),
// and carries the ~2-epoch leaving tail. Endpoint name per S0004 §2.4 (replaces
// the deprecated /account_history) — confirm shape against live Koios (R1).
type koiosStakeHistory struct {
	PoolID      string `json:"pool_id"`
	Epoch       uint64 `json:"epoch_no"`
	ActiveStake string `json:"active_stake"`
}

// koiosTip is the relevant subset of Koios /tip.
type koiosTip struct {
	Epoch uint64 `json:"epoch_no"`
}

// Snapshot fetches the live delegation (account_info) and the active-stake
// history (account_stake_history) for a stake credential, yielding the raw facts
// DeriveState needs. The credential is passed to Koios as the `_stake_addresses`
// parameter (the bech32 stake address form).
func (k *KoiosSource) Snapshot(ctx context.Context, stakeCredentialHash string) (*Snapshot, error) {
	// Koios keys on the bech32 stake address, not the raw credential hash (p12-8).
	stakeAddr, err := stakeAddressFromCredential(stakeCredentialHash, k.network)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string][]string{"_stake_addresses": {stakeAddr}})
	var infos []koiosAccountInfo
	if err := k.post(ctx, "/account_info", body, &infos); err != nil {
		return nil, err
	}
	epoch, _ := k.Epoch(ctx)
	if len(infos) == 0 {
		return &Snapshot{StakeCredentialHash: stakeCredentialHash, Epoch: epoch, EpochsDelegated: -1, AccountStatus: "not_registered", Source: "koios", FetchedAt: time.Now().UTC()}, nil
	}
	// We fetch stake history for any registered account, not just one whose *live*
	// delegation points at us: a leaving member's live delegation has already moved
	// while their active stake still counts for us — pruning on live pool would
	// drop the leaving tail (S0004 §2.2). Pool-agnostic by design: the source
	// returns raw facts; the pool comparison lives in DeriveState.
	var hist []koiosStakeHistory
	if err := k.post(ctx, "/account_stake_history", body, &hist); err != nil {
		return nil, err
	}
	return koiosToSnapshot(stakeCredentialHash, epoch, infos[0], hist), nil
}

// koiosToSnapshot maps Koios account_info + account_stake_history to a Snapshot
// (pure; unit-tested).
func koiosToSnapshot(hash string, epoch uint64, in koiosAccountInfo, hist []koiosStakeHistory) *Snapshot {
	live := in.DelegatedPool
	if in.Status != "registered" {
		live = ""
	}
	s := &Snapshot{
		StakeCredentialHash: hash,
		Epoch:               epoch,
		DelegatedPoolID:     live,
		RewardsLovelace:     in.Rewards,
		EpochsDelegated:     0, // registered: now derivable from history (0 = no active stake yet)
		AccountStatus:       in.Status,
		Source:              "koios",
		FetchedAt:           time.Now().UTC(),
	}
	if latest, ok := latestStakeEntry(hist); ok {
		s.ActiveStakePoolID = latest.PoolID
		s.ActiveStakeLovelace = latest.ActiveStake
		s.EpochsDelegated = trailingActiveEpochs(hist, latest.PoolID)
	}
	return s
}

// latestStakeEntry returns the highest-epoch stake-history row (Koios row order
// is not guaranteed). ok=false on empty history.
func latestStakeEntry(hist []koiosStakeHistory) (koiosStakeHistory, bool) {
	var latest koiosStakeHistory
	found := false
	for _, h := range hist {
		if !found || h.Epoch > latest.Epoch {
			latest, found = h, true
		}
	}
	return latest, found
}

// trailingActiveEpochs counts how many consecutive epochs (ending at the latest)
// the credential was active with `pool` — the uninterrupted active-stake run for
// that pool, walking epoch-contiguous rows downward from the latest.
func trailingActiveEpochs(hist []koiosStakeHistory, pool string) int {
	byEpoch := make(map[uint64]string, len(hist))
	var top uint64
	have := false
	for _, h := range hist {
		byEpoch[h.Epoch] = h.PoolID
		if !have || h.Epoch > top {
			top, have = h.Epoch, true
		}
	}
	count := 0
	for e := top; ; e-- {
		if byEpoch[e] != pool {
			break
		}
		count++
		if e == 0 {
			break
		}
	}
	return count
}

// koiosDelegator is the relevant subset of Koios /pool_delegators.
type koiosDelegator struct {
	StakeAddress string `json:"stake_address"`
}

// delegatorPageSize is the Koios page size for delegator enumeration.
const delegatorPageSize = 1000

// Delegators enumerates a pool's delegators one page at a time (S0004 §2.7, D9):
// a direct passthrough of Koios /pool_delegators paging (no cache — this is a
// cold, read-only admin query, and the active-membership cache only serves the
// hot authorization path). Stake addresses are mapped to credential hashes (hex).
func (k *KoiosSource) Delegators(ctx context.Context, poolID string, page int) ([]string, error) {
	if page < 0 {
		page = 0
	}
	path := fmt.Sprintf("/pool_delegators?_pool_bech32=%s&offset=%d&limit=%d", poolID, page*delegatorPageSize, delegatorPageSize)
	var rows []koiosDelegator
	if err := k.get(ctx, path, &rows); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, d := range rows {
		hash, err := StakeHashFromRewardAddress(d.StakeAddress)
		if err != nil {
			return nil, err
		}
		out = append(out, hash)
	}
	return out, nil
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
