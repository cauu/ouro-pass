// Runtime config injected at build via Vite env. The Admin SPA is served
// same-origin by the issuer (relative API paths), so only the optional wallet
// network guard needs configuring. VITE_ISSUER_NETWORK = mainnet|preprod|preview
// (when unset, the wallet network guard is skipped and the backend's owner-key
// check is the real gate — TC-8).
export const config = {
  issuerNetwork: import.meta.env.VITE_ISSUER_NETWORK as string | undefined,
};

export type RoleRank = 1 | 2 | 3;
export const roleRank: Record<string, RoleRank> = { viewer: 1, operator: 2, owner: 3 };
