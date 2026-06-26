// Wire types mirror what the issuer admin endpoints actually emit. NOTE: the
// domain structs carry no json tags, so the list endpoints serialize Go field
// names (PascalCase, e.g. SessionID); a couple of handler-local structs use
// snake_case (members). Types match the wire exactly (decision: a future backend
// cleanup could add snake_case json tags; the frontend would follow).

export type Role = "owner" | "operator" | "viewer";

export interface Me {
  admin_id: string;
  role: Role;
}

export interface Member {
  stake_credential_hash: string;
  tier: string;
  channel_type: string;
}

export interface Subscription {
  SessionID: string;
  PoolID: string;
  StakeCredentialHash: string;
  ChannelType: string;
  ChannelUserID: string;
  ChannelAccountID: string | null;
  Status: string;
  Tier: string;
  Topics: string[] | null;
  Entitlements: string[] | null;
  CreatedAt: string;
  LastVerifiedAt: string;
  ExpiresAt: string;
  CancelledAt: string | null;
}

export interface PushJob {
  JobID: string;
  PoolID: string;
  Title: string;
  Content: string;
  ChannelType: string;
  TargetTopic: string | null;
  RequiredEntitlement: string | null;
  TargetTier: string | null;
  Status: string;
  ScheduledAt: string | null;
  CreatedBy: string;
  CreatedAt: string;
}

export interface OAuthClient {
  ClientID: string;
  Name: string;
  ClientType: string;
  ClientSecretHash: string | null;
  RedirectURIs: string[] | null;
  AllowedAudiences: string[] | null;
  Status: string;
  CreatedAt: string;
}

export interface AuditEntry {
  AuditID: string;
  Actor: string;
  Action: string;
  Target: string;
  BeforeHash: string | null;
  AfterHash: string | null;
  IP: string | null;
  CreatedAt: string;
}

/** Step-up re-signature body for sensitive operations (§9.8). */
export interface StepUpBody {
  cose_key: string;
  step_up_nonce: string;
  step_up_signature: string;
}

// First-party tier mapping (S0004 §2.6): ordered, first match wins. Consumed only
// by the issuer's own channels; external RPs read raw token facts.
// TierCondition is the boolean DSL over aggregate facts (S0006 §2.4): exactly one
// of all/any/not (combinators) or a {fact,op,value} leaf. Empty = catch-all.
export interface TierCondition {
  all?: TierCondition[];
  any?: TierCondition[];
  not?: TierCondition;
  fact?: string;
  op?: string; // == != >= > <= <
  value?: string;
}

export interface TierRule {
  tier: string;
  when?: TierCondition;
}

export interface PoolInfo {
  pool_id: string;
  network?: string;
  ticker?: string;
  tier_rules: TierRule[];
}

// Attestor is one configured on-chain credential source (S0006): the
// generalization of "the served pool". params is kind-specific (pool_stake:
// pool_id/network/ticker/name).
export interface Attestor {
  attestor_id: string;
  kind: string;
  label: string;
  params: Record<string, unknown>;
  status: string; // active | disabled
}

export interface ClientRegister {
  // client_id is system-generated server-side; not part of the request.
  name: string;
  client_type: "public" | "confidential";
  redirect_uris: string[];
  allowed_audiences: string[];
}

export interface PushCreate {
  title: string;
  content: string;
  channel_type: string;
  channel_id?: string; // S0005: target a single channel instance (optional)
  target: { tier?: string; topic?: string; entitlement?: string };
}

// ChannelInstance is one configured delivery-channel instance (S0005). A pool may
// run N instances of one platform (e.g. two telegram bots). Secrets are never
// returned; bot_username is public and used for activation deep links.
export interface ChannelInstance {
  channel_id: string;
  channel_type: string;
  name: string;
  status: string; // active | disabled
  configured: boolean;
  bot_username?: string;
}
