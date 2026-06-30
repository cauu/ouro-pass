package chain

// DefaultKoiosBaseURL returns the public Koios endpoint for a Cardano network. Koios
// endpoints are per-network (S0014 p1-1): a single global URL applied to every network
// derives a wrong-network stake address → empty result → false "not eligible". An empty or
// unknown network defaults to mainnet.
func DefaultKoiosBaseURL(network string) string {
	switch network {
	case "preprod":
		return "https://preprod.koios.rest/api/v1"
	case "preview":
		return "https://preview.koios.rest/api/v1"
	default: // mainnet (and unknown)
		return "https://api.koios.rest/api/v1"
	}
}
