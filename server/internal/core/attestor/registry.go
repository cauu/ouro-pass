package attestor

import (
	"encoding/json"
	"fmt"

	"ouro-pass/server/internal/utils/chain"
)

// SourceFor resolves a read-only chain.Source for a network (S0006 D4: network is
// per-attestor, so the source is built/cached per network). network is "" for
// network-agnostic kinds. A deployment supplies one when building attestors.
type SourceFor func(network string) (chain.Source, error)

// Builder constructs an Attestor of a given Kind from its stable id and JSON
// params, using srcFor to obtain any chain source it needs.
type Builder func(id string, params json.RawMessage, srcFor SourceFor) (Attestor, error)

// Registry maps a credential Kind to its Builder. It is the single place that
// knows how to turn an AttestorConfig (kind + params) into a runtime Attestor.
type Registry struct {
	builders map[string]Builder
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{builders: map[string]Builder{}} }

// Register binds a Builder to a Kind (later registration overrides an earlier one).
func (r *Registry) Register(kind string, b Builder) { r.builders[kind] = b }

// Build constructs an attestor for kind from params. Unknown kinds (e.g. an "nft"
// config in a build that does not implement it) return an error so callers can
// skip/flag the configured credential rather than silently dropping it.
func (r *Registry) Build(kind, id string, params json.RawMessage, srcFor SourceFor) (Attestor, error) {
	b, ok := r.builders[kind]
	if !ok {
		return nil, fmt.Errorf("attestor: no builder for kind %q", kind)
	}
	return b(id, params, srcFor)
}

// DefaultRegistry returns a registry with the built-in attestors registered.
// pool_stake is the only Kind this cycle; NFT is intentionally absent (S0006 C5).
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(KindPoolStake, BuildPoolStake)
	return r
}
