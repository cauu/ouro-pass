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

export interface Rule {
  RuleID: string;
  Name: string;
  RuleConfig: unknown;
  Tier: string;
  Entitlements: string[] | null;
  Priority: number;
  Status: string;
  CreatedAt: string;
  UpdatedAt: string;
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

export interface RuleUpsert {
  rule_id: string;
  name: string;
  rule_config: unknown;
  tier: string;
  entitlements: string[];
  priority: number;
  status: string;
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
  target: { tier?: string; topic?: string; entitlement?: string };
}
