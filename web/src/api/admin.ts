import { api } from "./client";
import type {
  AuditEntry,
  ClientRegister,
  Me,
  Member,
  OAuthClient,
  PushCreate,
  PushJob,
  Role,
  Rule,
  RuleUpsert,
  StepUpBody,
  Subscription,
} from "@/lib/types";

// ---- auth / session ----
export const adminChallenge = (owner_stake_address: string) =>
  api.post<{ nonce: string; expires_at: string }>("/api/admin/auth/challenge", {
    owner_stake_address,
  });

export const adminVerify = (body: { nonce: string; cose_key: string; signature: string }) =>
  api.post<{ role: Role }>("/api/admin/auth/verify", body);

export const adminLogout = () => api.post("/api/admin/auth/logout");

export const adminMe = () => api.get<Me>("/api/admin/me");

// Step-up nonce for a sensitive op (issued behind the session). Backend route
// added by S0002 p2-0 (wires admin.ChallengeStepUp, previously test-only).
export const stepUpChallenge = (owner_stake_address: string) =>
  api.post<{ nonce: string }>("/api/admin/auth/step-up/challenge", { owner_stake_address });

// ---- members & subscriptions ----
export const listMembers = () => api.get<{ members: Member[] }>("/api/admin/members");

export const revokeMember = (sch: string, stepUp: StepUpBody) =>
  api.post<{ revoked: boolean; tokens_revoked: number; grants_revoked: number; sessions_cancelled: number }>(
    `/api/admin/members/${encodeURIComponent(sch)}/revoke`,
    stepUp,
  );

export const listSubscriptions = () =>
  api.get<{ subscriptions: Subscription[] }>("/api/admin/subscriptions");

export const cancelSubscription = (id: string) =>
  api.post<{ cancelled: boolean }>(`/api/admin/subscriptions/${encodeURIComponent(id)}/cancel`);

// ---- rules ----
export const listRules = () => api.get<{ rules: Rule[] }>("/api/admin/rules");
export const upsertRule = (body: RuleUpsert) =>
  api.post<{ rule_id: string }>("/api/admin/rules", body);

// ---- channels ----
export const configureChannel = (type: string, config: unknown) =>
  api.post<{ channel_id: string }>(`/api/admin/channels/${encodeURIComponent(type)}/configure`, {
    config,
  });

// ---- push ----
export const listPushJobs = () => api.get<{ jobs: PushJob[] }>("/api/admin/push/jobs");
export const createPushJob = (body: PushCreate) =>
  api.post<{ job_id: string; status: string }>("/api/admin/push/jobs", body);

// ---- oauth clients ----
export const listClients = () => api.get<{ clients: OAuthClient[] }>("/api/admin/oauth-clients");
export const registerClient = (body: ClientRegister & StepUpBody) =>
  api.post<{ client_id: string; client_secret?: string }>("/api/admin/oauth-clients", body);

// ---- signing keys ----
export const rotateKey = (stepUp: StepUpBody) =>
  api.post<{ new_kid: string; status: string; jwks_updated: boolean }>(
    "/api/admin/keys/issuer/rotate",
    stepUp,
  );
export const generateKey = (stepUp: StepUpBody) =>
  api.post<{ new_kid: string; status: string; jwks_updated: boolean }>(
    "/api/admin/keys/issuer/generate",
    stepUp,
  );

// ---- audit ----
export const listAudit = () => api.get<{ audit: AuditEntry[] }>("/api/admin/audit");

// ---- JWKS (public, read-only — for the keys page status view) ----
export interface Jwk {
  kid: string;
  kty: string;
  crv?: string;
  alg?: string;
  x?: string;
}
export const fetchJwks = () => api.get<{ keys: Jwk[] }>("/.well-known/ouropass/jwks.json");
