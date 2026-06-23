package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// NodeLSQSource queries a local relay node via Local State Query
// (`cardano-cli query stake-address-info` / `tip`). It is zero-third-party but
// cannot supply per-credential active stake. The exec runner is injectable so
// the parsing logic is unit-tested without a live node (decision D5).
type NodeLSQSource struct {
	cli     string
	socket  string
	network string
	// run executes a cardano-cli invocation; overridable in tests.
	run func(ctx context.Context, args ...string) ([]byte, error)
}

// NewNodeLSQSource builds a node_lsq source. cli defaults to "cardano-cli".
func NewNodeLSQSource(cli, socket, network string) *NodeLSQSource {
	if cli == "" {
		cli = "cardano-cli"
	}
	s := &NodeLSQSource{cli: cli, socket: socket, network: network}
	s.run = s.execCLI
	return s
}

// Name returns "node_lsq".
func (n *NodeLSQSource) Name() string { return "node_lsq" }

func (n *NodeLSQSource) execCLI(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, n.cli, args...)
	cmd.Env = append(cmd.Env, "CARDANO_NODE_SOCKET_PATH="+n.socket)
	return cmd.Output()
}

// stakeAddressInfo mirrors the relevant fields of `cardano-cli query
// stake-address-info` output (a JSON array).
type stakeAddressInfo struct {
	Address          string `json:"address"`
	StakeDelegation  string `json:"stakeDelegation"`
	RewardAccountBal int64  `json:"rewardAccountBalance"`
}

// parseStakeAddressInfo maps cardano-cli output to a Snapshot (pure; unit-tested).
func parseStakeAddressInfo(hash string, epoch uint64, raw []byte) (*Snapshot, error) {
	var infos []stakeAddressInfo
	if err := json.Unmarshal(raw, &infos); err != nil {
		return nil, fmt.Errorf("node_lsq: parse stake-address-info: %w", err)
	}
	s := &Snapshot{StakeCredentialHash: hash, Epoch: epoch, Source: "node_lsq", FetchedAt: time.Now().UTC()}
	if len(infos) > 0 {
		s.DelegatedPoolID = infos[0].StakeDelegation
		s.RewardsLovelace = fmt.Sprintf("%d", infos[0].RewardAccountBal)
		// node_lsq cannot supply per-credential active stake (left empty, D-note §3.3).
	}
	return s, nil
}

// Snapshot queries the node for stake-address-info (integration path).
func (n *NodeLSQSource) Snapshot(ctx context.Context, stakeCredentialHash string) (*Snapshot, error) {
	epoch, err := n.Epoch(ctx)
	if err != nil {
		return nil, err
	}
	out, err := n.run(ctx, "query", "stake-address-info", "--address", stakeCredentialHash, n.networkFlag())
	if err != nil {
		return nil, fmt.Errorf("node_lsq: query stake-address-info: %w", err)
	}
	return parseStakeAddressInfo(stakeCredentialHash, epoch, out)
}

// tipInfo mirrors `cardano-cli query tip`.
type tipInfo struct {
	Epoch uint64 `json:"epoch"`
}

// Epoch queries the node tip for the current epoch.
func (n *NodeLSQSource) Epoch(ctx context.Context) (uint64, error) {
	out, err := n.run(ctx, "query", "tip", n.networkFlag())
	if err != nil {
		return 0, fmt.Errorf("node_lsq: query tip: %w", err)
	}
	return parseTip(out)
}

func parseTip(raw []byte) (uint64, error) {
	var t tipInfo
	if err := json.Unmarshal(raw, &t); err != nil {
		return 0, fmt.Errorf("node_lsq: parse tip: %w", err)
	}
	return t.Epoch, nil
}

func (n *NodeLSQSource) networkFlag() string {
	if n.network == "mainnet" {
		return "--mainnet"
	}
	return "--testnet-magic" // magic value supplied by node config in real runs
}
