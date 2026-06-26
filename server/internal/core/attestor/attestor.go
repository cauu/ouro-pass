// Package attestor abstracts "an on-chain identity credential" (S0006): a
// pluggable predicate over a subject's on-chain state that yields a held/not-held
// verdict plus self-describing facts. The subject is always the wallet's stake
// credential hash. `pool_stake` is the first Kind; an issuer configures a SET of
// attestors (e.g. several pools, future NFT policies), each evaluated per subject
// and their results aggregated (the aggregate drives the thin ANY-of gate and the
// first-party tier). Attestors are built from AttestorConfig via the Registry,
// keyed by Kind — the single place that knows how to turn config into runtime.
package attestor

import "context"

// Kind constants for the configured credential types.
const (
	// KindPoolStake attests a subject's membership in one stake pool.
	KindPoolStake = "pool_stake"
	// KindNFT is reserved (S0006 C5): the config kind exists so a deployment can be
	// modelled multi-platform, but no evaluator is implemented this cycle.
	KindNFT = "nft"
)

// Attestation is one attestor's result for a subject.
type Attestation struct {
	// Kind is the attestor's credential type (e.g. "pool_stake").
	Kind string
	// ID is the configured attestor's stable id.
	ID string
	// Held reports whether the subject satisfies this credential — the input to the
	// thin issuer gate (ANY-of, S0006 D7).
	Held bool
	// Claim is the self-describing entry placed in the token's credentials[] array
	// (S0006 §2.3). Its shape is Kind-specific; relying parties read what they care
	// about.
	Claim map[string]any
	// Facts are the namespaced named facts this attestor contributes to the
	// aggregate used for tier evaluation (S0006 §2.4), e.g. "pool:<id>.state" →
	// "active". Cross-attestor facts (any_active, total_active_stake) are derived by
	// the aggregator, not here.
	Facts map[string]string
}

// Attestor evaluates one configured credential against a subject (the wallet's
// stake credential hash, hex). Implementations may perform read-only chain I/O.
type Attestor interface {
	// Kind returns the credential type.
	Kind() string
	// ID returns the configured attestor's stable id.
	ID() string
	// Attest evaluates the subject and returns its attestation for this credential.
	Attest(ctx context.Context, subject string) (*Attestation, error)
}
