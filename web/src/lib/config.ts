// The Admin SPA is served same-origin by the issuer (relative API paths). Wallet/owner
// login is network-agnostic (S0014 p1-4): the issuer has no single network, so there is no
// VITE_ISSUER_NETWORK guard — eligibility/owner checks are decided by the backend.

export type RoleRank = 1 | 2 | 3;
export const roleRank: Record<string, RoleRank> = { viewer: 1, operator: 2, owner: 3 };
