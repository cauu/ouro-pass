# Ouro Pass Issuer Service 详细设计

> v3 · 2026-06-22 · **OAuth 登录版**（移除冷钥 license 证书链：删 OwnerAuthCert/CertRevocation，IssuerKey 瘦为签名密钥，LicenseRecord→IssuedToken）。数据实体（§1–8）+ 接口设计（§9）。流程见概要设计 `ouropass-issuer-overview.md`。

## 0. 约定

- **存储**：PostgreSQL（生产）/ SQLite（单机 MVP）。下文 `array`/`json` 字段在 PG 用 `jsonb`，在 SQLite 用 `TEXT`(JSON 编码)或拆子表。
- **时间**：`ts` = `timestamptz`（UTC）。所有实体含 `created_at`。
- **epoch**：Cardano epoch，整数。
- **金额**：lovelace 用 `numeric(20)` 或 `text`——最大供给约 4.5×10¹⁶ 超过 2⁵³，不可用 JS number / int4。
- **密钥/密文**：`bytea`(PG) 存原始字节；私钥、bot token、client_secret 等敏感字段一律**加密落盘**，下文标注 `🔒`。
- **命名**：表名 PascalCase（文档用），列名 snake_case。PK 标 `PK`，外键标 `FK→Entity`。

## 1. 实体总览

| 分组 | 实体 | MVP |
|---|---|---|
| 池与签名密钥 | PoolConfig · IssuerKey | ✅ |
| 规则与身份 | MembershipRule · StakeSnapshotCache(可选) · Blacklist(可选) ; 身份键用 stake_credential_hash，无 Member 表 | ✅/○ |
| Token 与凭证 | IssuedToken · RefreshGrant · AuthorizationCode · ActivationCode · AuthNonce | ✅ |
| 客户端 | OAuthClient | ✅ |
| 渠道与订阅 | ChannelConfig · SubscriptionSession | ✅(仅 Telegram) |
| 推送 | PushJob · DeliveryLog | ✅ |
| 管理与审计 | AdminUser · AdminSession · AuditLog | ✅ |

**关系主线**：

```text
MembershipRule（规则配置，独立）
IssuerKey 1─* IssuedToken            # 签名密钥 → 签出的 token（无证书链）
stake_credential_hash ─< IssuedToken / RefreshGrant / SubscriptionSession / Auth/ActivationCode
OAuthClient 1─* AuthorizationCode / RefreshGrant(confidential)
ChannelConfig 1─* SubscriptionSession
PushJob 1─* DeliveryLog *─1 SubscriptionSession
AdminUser 1─* AdminSession
```

> **OAuth 登录版**：access token 由 issuer 单签名密钥签发，无 cold/owner 证书链。无 Member 表：身份键 = `stake_credential_hash`；对外 `sub` 为派生伪匿名。资格状态不落库——按需现算，真值活在 token（签发时快照）与 session（tier/entitlements）。

---

## 2. 池与签名密钥

### 2.1 PoolConfig

issuer 所服务的 stake pool 配置（通常单行）。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| pool_id | text | PK | bech32 pool id（= `blake2b224(cold_vkey)`） |
| ticker | text | | 池 ticker |
| name | text | null | 显示名 |
| metadata_url | text | null | 池元数据 URL |
| network | enum | | `mainnet`/`preprod`/`preview` |
| created_at / updated_at | ts | | |

> staking 数据源（Koios/Blockfrost endpoint 与 API key）属基础设施配置且含密钥，走**环境变量 / secret**，不入库。单池 MVP 下 PoolConfig 近乎静态配置，`pool_id` 可作常量，各表 `pool_id` FK 为可选冗余。

### 2.2 IssuerKey（签名密钥）

签发 access / activation token 的 issuer 签名密钥；可轮换，发布到 JWKS。**无上层证书（无 cold/owner 链）**。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| kid | text | PK | 如 `op-issuer-2026-08` |
| public_key | bytea | | ed25519 公钥（JWKS 发布） |
| encrypted_private_key | bytea | 🔒 | 加密私钥 |
| status | enum | | `active`/`rotating`/`retired`/`revoked` |
| valid_from / valid_until | ts | null | 可选有效窗口 |
| created_at | ts | | |
| retired_at | ts | null | |

> 轮换=生成新 kid、JWKS overlap，旧 token 靠短有效期退役（§9.5）；泄露=该 kid 标 revoked 并移出 JWKS。无 OwnerAuthCert / CertRevocation 实体。

---

## 3. 规则与身份

### 3.1 MembershipRule

定义某 tier 的资格门槛与权益。匹配条件收敛进 `rule_config`（json），便于演进、免 schema 迁移。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| rule_id | text | PK | 如 `op_gold_v1` |
| name | text | | |
| rule_config | json | | 全部匹配条件：`required_status`、`min_active_stake_lovelace`、`min_active_epochs`、`grace_epochs`、campaign 等 |
| tier | text | | 命中后授予的 tier，如 `gold` |
| entitlements | array | | 授予的权益（构建 token / session 时读取） |
| priority | int | | 多规则匹配优先级 |
| status | enum | | `active`/`disabled` |
| created_at / updated_at | ts | | |

### 3.2 身份约定（无 Member 表）

**不设 Member 表。** 身份键统一用 `stake_credential_hash`，由各 durable 表直接持有（刷新/推送需用它回查链）。

- **对外伪匿名**：token 的 `sub` 用确定性派生 `base32(HMAC(server_salt, stake_credential_hash))`——稳定、隐私（verifier 无 salt 不可逆）、零状态，不暴露链上 hash。
- **查质押用户**：直接查链即可，无需本地名册，也无数据一致性问题。
- **手动拉黑**：真需要时再建稀疏 `Blacklist(stake_credential_hash, reason, created_at)`，不为它养整张表。admin 名册/撤销均按 `stake_credential_hash`(sch) 寻址——sch 为链上公开派生值，对受信 SPO admin 无隐私损失；伪匿名 `sub` 仅供外部 token 消费方。revoke 写 `Blacklist[sch]`（与资格判定查询同键）并级联撤销该 sch 的 token/grant/session。
- **链上缓存**：若 query 成本敏感，另设可选 `StakeSnapshotCache`（原始快照，post-MVP 性能优化），是缓存而非真值。
- **设备绑定**：public client 的 `device_pubkey` 落在 `RefreshGrant.bound_device_pubkey`；confidential client 由 OAuthClient + client_secret 充当 holder-of-key。

### 3.3 StakeSnapshotCache（可选，缓存）

链上质押数据的本地缓存——存**原始快照**（非评估结论），资格由 rule engine 读取时现算。允许按 epoch 粒度「陈旧」、随时可重建，不引入一致性风险。MVP 量小可不建（直查数据源）；会员量上来再启用以削减查询负担。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| stake_credential_hash | text | PK | 链上身份键 |
| snapshot_epoch | int | | 本行反映的 epoch（新鲜度标记） |
| delegated_pool_id | text | null | 当前/快照委托的 pool |
| active_stake_lovelace | numeric | null | 快照 active 委托额（仅 db-sync/外部源可填；纯节点 LSQ 模式可为空） |
| rewards_lovelace | numeric | null | reward 账户余额（可选） |
| source | enum | | `node_lsq`/`db_sync`/`koios`/`blockfrost` |
| fetched_at | ts | | 拉取时间 |

**数据源（Staking Index Adapter 可插拔，优先自托管、避免第三方）：**

- `node_lsq`——relay 节点本地 Local State Query（`cardano-cli query stake-address-info` / `stake-snapshot`）：当前委托 pool、reward 余额、pool 总 active stake。**零第三方**，但无 per-credential 精确 active stake。
- `db_sync`——本地自托管 `cardano-db-sync`（`epoch_stake` 表）：补齐 per-credential active stake。**仍非第三方**。
- `koios`/`blockfrost`——第三方便捷选项，能力同 db-sync。

> 查节点 socket 为只读 ledger 查询，不触及任何私钥，符合 §11 密钥隔离。刷新由 Reconciliation Job 每 epoch 执行。

---

## 4. Token 与凭证

### 4.1 IssuedToken

已签发 token 的台账（access / activation，用于 introspect / 吊销 / 审计；不存 token 本体）。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| jti | text | PK | token id |
| stake_credential_hash | text | idx | 链上身份键（刷新/推送回查链；非唯一） |
| kind | enum | | `access`/`activation` |
| audience | text | | aud |
| kid | text | FK→IssuerKey | 签名所用 issuer 签名密钥 |
| client_id | text | FK→OAuthClient, null | 签发客户端（confidential client） |
| status | enum | | `active`/`expired`/`revoked` |
| issued_at / expires_at | ts | | |
| redeemed_at / revoked_at | ts | null | |

### 4.2 RefreshGrant

长效、可轮换、可撤销的刷新凭证；轮换链用于盗用检测。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| refresh_grant_id | text | PK | 存哈希值，不存明文 |
| stake_credential_hash | text | idx | 链上身份键（刷新/推送回查链；非唯一） |
| audience | text | | |
| client_type | enum | | `public`/`confidential` |
| bound_device_pubkey | bytea | null | public：PoP 绑定的设备公钥（App 本地生成，私钥不上行） |
| client_id | text | FK→OAuthClient, null | confidential：client_secret 持有 |
| status | enum | | `active`/`rotated`/`revoked`/`expired` |
| rotated_from | text | FK→RefreshGrant, null | 上一代 grant（轮换链） |
| created_at / expires_at / last_used_at | ts | null | sliding window |

### 4.3 AuthorizationCode（OAuth 授权码）

`/connect` 签发、`/api/oauth/token` 兑换的一次性码。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| code | text | PK | 存哈希值 |
| client_id | text | FK→OAuthClient | |
| stake_credential_hash | text | idx | 链上身份键（刷新/推送回查链；非唯一） |
| aud | text | | |
| scope | array | null | 请求的 entitlement 子集 |
| redirect_uri | text | | 须与注册值精确匹配 |
| code_challenge | text | null | PKCE(S256) |
| expires_at | ts | | ≤60s |
| consumed_at | ts | null | |
| created_at | ts | | |

### 4.4 ActivationCode（渠道激活码）

绑定渠道（如 Telegram）用的一次性短效码。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| code | text | PK | 存哈希值（或 Activation Token 的 jti） |
| stake_credential_hash | text | idx | 链上身份键（刷新/推送回查链；非唯一） |
| channel_type | enum | | `telegram`/`discord`/`email` |
| status | enum | | `active`/`consumed`/`expired` |
| expires_at | ts | | 5–60 分钟 |
| consumed_at | ts | null | |
| created_at | ts | | |

> 若实现为签名 JWT（Activation Token），本表退化为「已消费 jti」记录，仅需 `jti`/`consumed_at`。

### 4.5 AuthNonce

钱包签名鉴权用的一次性 nonce（防重放）。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| nonce | text | PK | |
| purpose | enum | | `issue`/`activation`/`admin_login`/`step_up`（与 §9.3 接口取值一致） |
| bound_key_hash | text | null | 期望的签名者 key hash |
| expires_at | ts | | 短效 |
| consumed_at | ts | null | |
| created_at | ts | | |

---

## 5. 客户端

### 5.1 OAuthClient

注册的第一方/第三方集成应用。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| client_id | text | PK | |
| name | text | | |
| client_type | enum | | `confidential`/`public` |
| client_secret_hash | text | null, 🔒 | confidential 专有 |
| party | enum | | `first_party`/`third_party` |
| redirect_uris | array | | 回调 allowlist（精确匹配） |
| allowed_audiences | array | | 可签发的 aud |
| allowed_scopes | array | | 可请求的 entitlement |
| pkce_required | bool | | public client 应为 true |
| status | enum | | `active`/`disabled` |
| created_at | ts | | |

---

## 6. 渠道与订阅

### 6.1 ChannelConfig

SPO 配置的渠道实例（含密钥，加密存储）。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| channel_id | uuid | PK | |
| pool_id | text | FK→PoolConfig | |
| channel_type | enum | | `telegram`/`discord`/`email`/`webhook` |
| config | json | 🔒 | bot token / webhook url / smtp 凭据等（敏感字段加密） |
| status | enum | | `active`/`disabled` |
| created_at / updated_at | ts | | |

### 6.2 SubscriptionSession

某 member 在某渠道的订阅关系（服务端状态对象）。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| session_id | text | PK | |
| pool_id | text | FK→PoolConfig | |
| stake_credential_hash | text | idx | 链上身份键（刷新/推送回查链；非唯一） |
| channel_type | enum | | |
| channel_user_id | text | | 如 telegram user id |
| channel_account_id | text | null | bot id / 账号 |
| status | enum | | `active`/`downgraded`/`cancelled`/`expired` |
| tier | text | | |
| topics | array | | 订阅的 topic |
| entitlements | array | | 当前生效权益 |
| created_at / last_verified_at / expires_at | ts | | |
| cancelled_at | ts | null | |

> 唯一约束：`(pool_id, channel_type, channel_user_id)`。

---

## 7. 推送

### 7.1 PushJob

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| job_id | uuid | PK | |
| pool_id | text | FK→PoolConfig | |
| title | text | | |
| content | text | | |
| channel_type | enum | | |
| target_topic | text | null | 三选一/可组合的目标过滤 |
| required_entitlement | text | null | |
| target_tier | text | null | |
| status | enum | | `draft`/`scheduled`/`running`/`done`/`cancelled`/`failed` |
| scheduled_at | ts | null | |
| created_by | uuid | FK→AdminUser | |
| created_at | ts | | |

### 7.2 DeliveryLog

每个接收者一条投递记录。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| delivery_id | uuid | PK | |
| job_id | uuid | FK→PushJob | |
| session_id | text | FK→SubscriptionSession | |
| channel_type | enum | | |
| channel_user_id | text | | |
| status | enum | | `sent`/`failed`/`skipped` |
| retry_count | int | | |
| error_message | text | null | |
| sent_at | ts | null | |

---

## 8. 管理与审计

### 8.1 AdminUser

后台管理员；身份用 owner key（钱包签名登录）。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| admin_id | uuid | PK | |
| pool_id | text | FK→PoolConfig | |
| owner_key_hash | text | unique | 登录用 stake key hash |
| role | enum | | `owner`/`operator`/`viewer` |
| last_login_at | ts | null | |
| created_at | ts | | |

> `owner` 角色的 key 须在**链上 pool owner 列表**中（登录时对照 pool 注册的 owner stake key 校验，不依赖本地证书）；`operator`/`viewer` 由 owner 添加。

### 8.2 AdminSession

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| session_token | text | PK | 存哈希值；httpOnly cookie |
| admin_id | uuid | FK→AdminUser | |
| expires_at | ts | | 短效 |
| ip | text | null | |
| created_at | ts | | |

### 8.3 AuditLog

所有敏感操作的审计轨迹（密钥操作、撤销、规则变更、推送创建等）。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| audit_id | uuid | PK | |
| actor | text | | admin_id 或 `system` |
| action | text | | 如 `issuer_key.rotate` |
| target | text | | 受影响对象 |
| before_hash | text | null | 变更前状态摘要 |
| after_hash | text | null | 变更后状态摘要 |
| ip | text | null | |
| created_at | ts | | |

---

## 9. 接口设计

### 9.1 通用约定

- 传输：HTTPS + JSON。除 `/connect`、`/.well-known/*` 外，REST 端点位于 `/api`。
- 平面与鉴权：

| 平面 | 端点前缀 | 鉴权 |
|---|---|---|
| 钱包原语 | `/api/auth/challenge` | 无（发 nonce） |
| 签发(OAuth) | `/connect`、`/api/oauth/*` | 授权码：钱包签名→code；换取：client_secret(confidential) / PKCE+DPoP(public) |
| 渠道激活 | `/api/activation/*` | 钱包签名 |
| Verifier | `/.well-known/*`、`/api/oauth/introspect`、`/api/oauth/revoke` | 无 / 限速 / client |
| Telegram Bot | （长轮询 / webhook） | Telegram update + 一次性 code |
| Admin | `/api/admin/*` 及管理类 | owner-key 会话 + RBAC + step-up |

- 错误：HTTP 状态码 + `{ "error":"code", "error_description":"..." }`（OAuth 风格）。
- 幂等：create 类支持 `Idempotency-Key` 头。分页：cursor 风格 `?cursor=&limit=`。Public/Verifier 平面按 IP 限速。
- **签发统一走 OAuth**：无独立 `/api/license/issue`；签发=`authorization_code`、刷新=`refresh_token`，均在 `/api/oauth/token`。

**全部端点一览：**

| 方法 | 端点 | 平面 | 鉴权 | 用途 |
|---|---|---|---|---|
| POST | `/api/auth/challenge` | 钱包原语 | 无 | 取签名 nonce |
| GET | `/connect` | 签发 | 钱包（页面内） | Authorization Page |
| POST | `/api/connect/authorize` | 签发 | 钱包签名 | 授权页提交 → 授权码 |
| POST | `/api/oauth/token` | 签发 | client_secret / PKCE+DPoP | 换码 / 刷新 → access+refresh token |
| POST | `/api/oauth/introspect` | Verifier | 限速 | 查 token 状态（RFC 7662） |
| POST | `/api/oauth/revoke` | Verifier/Admin | client / admin | 吊销 token（RFC 7009） |
| GET | `/.well-known/ouropass/jwks.json` | Verifier | 无 | 签名公钥（无证书链） |
| POST | `/api/activation/create` | 渠道激活 | 钱包签名 | 生成 activation token + deep link |
| POST | `/api/admin/auth/challenge` | Admin | 无 | 取登录 nonce |
| POST | `/api/admin/auth/verify` | Admin | owner 签名 | 登录 → session |
| GET | `/api/admin/members` | Admin | viewer+ | 派生名册（按 stake_credential_hash） |
| GET | `/api/admin/members/:sch` | Admin | viewer+ | 会员详情 |
| POST | `/api/admin/members/:sch/revoke` | Admin | operator+ | 拉黑 |
| GET | `/api/subscriptions` | Admin | viewer+ | 订阅列表 |
| POST | `/api/subscriptions/:id/cancel` | Admin | operator+ | 取消订阅 |
| GET/POST/PATCH | `/api/rules` | Admin | operator+ | 规则增改查 |
| POST | `/api/channels/:type/configure` | Admin | operator+ | 配置渠道 |
| POST | `/api/channels/telegram/test` | Admin | operator+ | 测试渠道 |
| GET/POST | `/api/push/jobs` | Admin | operator+ | 推送任务 |
| POST | `/api/push/jobs/:id/cancel` | Admin | operator+ | 取消任务 |
| GET/POST | `/api/oauth-clients` | Admin | owner | 注册 client |
| POST | `/api/keys/issuer/generate` | Admin | owner+step-up | 生成签名密钥 |
| POST | `/api/keys/issuer/rotate` | Admin | owner+step-up | 轮换签名密钥（JWKS overlap） |
| GET | `/api/audit` | Admin | owner | 审计日志 |

Telegram Bot（非 REST，长轮询）：`/start <code>` · `/activate <code>` · `/status` · `/unsubscribe` · `/help`。

### 9.2 凭证格式（接口契约）

**Access Token（JWS, EdDSA）** — header `{ typ:"at+jwt", alg:"EdDSA", kid }`（**无 cert_hash、无证书链**），payload：

| claim | 说明 |
|---|---|
| iss | `ouropass:<pool_id>` |
| sub | 派生伪匿名 `base32(HMAC(server_salt, stake_credential_hash))` |
| aud | 目标消费方 |
| iat / nbf / exp | 短有效期（access 1–7 天） |
| jti | = IssuedToken.jti |
| tier / entitlements | 资格产出 |
| cnf | public client 设备绑定 `{ jkt:<device pubkey thumbprint> }`（RFC 7800/9449 PoP） |

**Activation Token** — 签名 JWT，claim 含 `sub`、`channel_type`、`tier`、`entitlements`、`jti`、短 `exp`(分钟)、`one_time:true`；兑换零查链（资格已签入）。

**验证** — 消费方从 `GET /.well-known/ouropass/jwks.json` 取 `kid` 对应公钥验签（一组签名公钥，无证书链），或调 `/api/oauth/introspect` 在线校验。

### 9.3 钱包鉴权原语

`POST /api/auth/challenge` — req `{ purpose:"issue|activation", stake_vkey }` → res `{ nonce, expires_at }`（写入 AuthNonce；客户端用 CIP-30 `signData`(COSE) 对 nonce 签名）。

### 9.4 签发与刷新（OAuth）

`GET /connect` — Authorization Page（浏览器跳转）。query：`client_id, redirect_uri, state, aud, response_type=code, scope?, code_challenge?(PKCE)`。页面内完成连钱包、签 nonce，提交至 ↓

`POST /api/connect/authorize` — 授权页提交（内部）。req `{ client_id, redirect_uri, state, aud, nonce, stake_vkey, signature, code_challenge?, device_pubkey? }`；处理：验 COSE 签名 → 映射 `stake_credential_hash` → 查快照评估资格 → 生成 AuthorizationCode（绑定待签发的 token 上下文）→ res `302 redirect_uri?code=..&state=..`（不合格 `?error=not_eligible`）。

`POST /api/oauth/token` — 换取/刷新。
- `grant_type=authorization_code`：`{ grant_type, code, client_id, client_secret?, code_verifier?, redirect_uri }`；public client 另带 `DPoP` 头。
- `grant_type=refresh_token`：`{ grant_type, refresh_token, client_id, client_secret? }`；public client 带 `DPoP` 头。
- res（两者一致）：

```json
{ "access_token":"<jwt>", "token_type":"Bearer",
  "refresh_token":"<opaque, 轮换>", "expires_at":"...",
  "membership":{ "status":"eligible_member", "tier":"gold" } }
```

- 刷新处理：验 refresh token(active/未过期/未轮换) → 按 client_type 认证(client_secret / DPoP) → 按 `stake_credential_hash` 重评 → 新 access token + 轮换 refresh token；不合格→降级或 `403 not_eligible`；已轮换 token 重放→撤销链 `400 invalid_grant`。

### 9.5 渠道激活

`POST /api/activation/create` — 第一方绑定页，钱包签名。req `{ channel_type:"telegram", nonce, stake_vkey, signature }` → res `{ activation_code:"<Activation Token>", deep_link:"https://t.me/<bot>?start=<code>", expires_at }`。兑换经 Telegram bot（§9.7）。

### 9.6 Verifier（公开只读）

`GET /.well-known/ouropass/jwks.json` — 标准 JWKS，仅签名公钥，**无证书链**：

```json
{ "keys":[ { "kid":"op-issuer-2026-08", "kty":"OKP", "crv":"Ed25519", "x":"<pub>", "status":"active" } ] }
```

`POST /api/oauth/introspect`（RFC 7662）— req `{ token }` 或 `{ jti }` → res `{ active, scope, tier, exp, sub, ... }`。

`POST /api/oauth/revoke`（RFC 7009）— req `{ token, token_type_hint:"access_token|refresh_token" }`；client 撤销自己的 token，admin 可撤任意。标记 IssuedToken/RefreshGrant 为 revoked，introspect 即时反映。

### 9.7 Telegram Bot

非 REST。接口由三部分构成：传输层（Telegram ↔ issuer）+ 命令文法 + 出站调用。底层协议（`getUpdates` / `setWebhook` / `sendMessage`）由 Telegram Bot API 规定，不自定义。

**传输层（收消息）** — issuer 用 bot token 向 Telegram 认证：

- Long Polling（MVP）：bot worker 循环调 `getUpdates`，无需公网入口。
- Webhook（生产）：`setWebhook` 注册公网端点 `POST /telegram/webhook/<secret_token>`，Telegram 推送 update，用 secret token 校验来源。

**入站载荷** — 读取 Telegram `Update` 的关键字段：`message.from.id`（= `telegram_user_id`，Telegram 已鉴权、防冒充）、`message.text`（命令与参数）、`message.chat.id`（回复目标）。

**命令文法（用户侧接口）**：

| 命令 | 输入 | 处理 | 回复 |
|---|---|---|---|
| `/start <code>` / `/activate <code>` | 激活码 | 验 Activation Token（签名 + 未消费 + 未过期 + channel 匹配）→ 建 SubscriptionSession（绑 `from.id`）→ 标记 jti 已消费 | 订阅成功 + 已订阅 topics（**零查链**） |
| `/status` | — | 按 `from.id` 查 session | tier / topics / 到期 |
| `/unsubscribe` | — | 按 `from.id` 关闭 session | 已退订 |
| `/help` | — | — | 命令说明 |

**出站（回复 / 推送）** — 统一调 `sendMessage(chat_id, text)`：命令回复发 `from.id`；推送由 Push Scheduler 遍历匹配 session，对每个 `channel_user_id` 调 `sendMessage`，带限速（全局 ~30 msg/s）+ 退避重试，记 DeliveryLog。

**鉴权** — 激活靠 code 本身（签名、一次性）；`/status`、`/unsubscribe` 靠 `from.id` 定位 session（来自 Telegram 已鉴权 update，天然防冒充，无需额外鉴权）；Webhook 来源用 secret token 校验。

### 9.8 Admin（owner-key 会话 + RBAC）

`POST /api/admin/auth/challenge` → `{ nonce }`
`POST /api/admin/auth/verify` — req `{ nonce, owner_vkey, signature }` → 校验 `owner_key_hash ∈ 链上 pool owner 列表`（查 pool 注册 owners）→ 下发 httpOnly session cookie（AdminSession）。

以下端点需会话 + 角色；敏感操作（keys）需 **step-up**（重签 nonce）：

| 端点 | 方法 | 角色 | 说明 |
|---|---|---|---|
| `/api/admin/members` | GET | viewer+ | 派生名册（聚合 active session/token，按 **stake_credential_hash**） |
| `/api/admin/members/:sch` | GET | viewer+ | 单会员详情（现算资格 + 其 session/token） |
| `/api/admin/members/:sch/revoke` | POST | operator+ | 拉黑（写 Blacklist[sch] + 级联撤销其 token/grant/session） |
| `/api/subscriptions` | GET | viewer+ | |
| `/api/subscriptions/:id/cancel` | POST | operator+ | |
| `/api/rules` | GET/POST/PATCH | operator+ | 增改 MembershipRule（`rule_config`） |
| `/api/channels/:type/configure` | POST | operator+ | 配置渠道（bot token 等，加密存） |
| `/api/channels/telegram/test` | POST | operator+ | |
| `/api/push/jobs` | GET/POST | operator+ | 建/查推送任务 |
| `/api/push/jobs/:id/cancel` | POST | operator+ | |
| `/api/oauth-clients` | GET/POST | owner | 注册第一方/第三方 client |
| `/api/oauth/revoke` | POST | operator+ | 吊销指定用户/token |
| `/api/keys/issuer/generate` | POST | owner+step-up | 生成签名密钥 |
| `/api/keys/issuer/rotate` | POST | owner+step-up | 轮换签名密钥（JWKS overlap） |
| `/api/audit` | GET | owner | 审计日志 |

示例 `POST /api/keys/issuer/rotate` — req `{ }`（生成新 kid 并 overlap） → res `{ new_kid, status:"active", jwks_updated:true }`。旧 kid 待旧 token 过期后转 retired。

示例 `POST /api/push/jobs` — req `{ title, content, channel_type:"telegram", target:{ tier:"gold" }, scheduled_at:null }` → res `{ job_id, status:"scheduled" }`。
