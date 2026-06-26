# Cursor Review — S0002-keys-oauth-hardening

Scope reviewed: case 1 (spec changes, session-scoped)  
Base/diff: `b9f87b8..HEAD` | 27 files, +510 / −211 per scope.md  
Spec standard: `docs/specs/20260624T2355-S0002-ouropass-web-frontend.md`

## Assessment

Overall: **COMMENT** — PKCE binding、密钥退役守卫与 admin 硬化逻辑正确且与 spec 对齐；无阻塞级缺陷，但安全关键路径缺少若干负向/HTTP 回归测试，建议在合并前或紧随跟进补齐。

## Findings

### P0 — Critical

（无）

### P1 — High

（无）

### P2 — Medium

- **[server/internal/core/oauth/token_test.go]** 缺少 confidential 客户端「有 secret、无 `code_verifier`」负向测试 — **Issue**: `p6-3` 的核心修补是 `authenticateClient` 从「confidential 只验 secret」改为「先验 PKCE、再验 secret」；现有 `TestToken_AuthCodeConfidential` 仅在带 verifier 时测错 secret，未覆盖旧漏洞路径。**Risk**: 未来重构可能无声回归，confidential 再次跳过 PKCE。**Suggested fix**: 在 `TestToken_AuthCodeConfidential` 或新用例中，用 `eligibleCode` 拿到 code 后仅以 `client_secret` 兑换（省略 `CodeVerifier`），断言 `ErrInvalidGrant`。

- **[server/internal/core/oauth/oauth_test.go:170]** 缺少 authorize 无 `code_challenge` 负向测试 — **Issue**: `Authorize` 现无条件拒绝空 challenge（`oauth.go:120`），但 `TestAuthorize_ClientValidation` 的 `base` 请求仍不带 challenge，且未断言 PKCE 必填。**Risk**: authorize 侧 PKCE 强制无单测守护。**Suggested fix**: 增加 `TestAuthorize_RequiresPKCE`：`base` 补全其余字段但不设 `CodeChallenge`，期望 `ErrInvalidRequest`。

- **[server/internal/httpapi/handlers_admin_resources_test.go]** `POST /keys/issuer/{kid}/retire` 无 HTTP 层测试 — **Issue**: `adminRetireKey`（`handlers_admin_resources.go:405–433`）映射 404/`keys.ErrNotRotating`→409 与 audit `issuer_key.retire` 仅有 `keys.TestRetire` 单测，无 handler 集成覆盖。**Risk**: 路由/鉴权/错误映射漂移未被发现。**Suggested fix**: 在 `TestAdminOwner_RegisterClientAndRotateKey` 或独立测试中：rotate 后对 rotating kid step-up retire→200；对 active kid→409；未知 kid→404。

- **[server/internal/httpapi/handlers_admin_resources_test.go]** public 客户端 secret 再生 409 未测 — **Issue**: `adminRegenerateClientSecret` 对 `ClientPublic` 返回 409（`handlers_admin_resources.go:360–362`），测试仅覆盖 confidential 成功路径与 404。**Risk**: 409 分支易在后续改动中失效。**Suggested fix**: seed 一个 public client，step-up 调用 `/secret`，断言 409。

- **[server/internal/httpapi/handlers_admin_resources.go:311]** `client_id` 生成无碰撞检测 — **Issue**: `clientID := "op-client-" + crypto.RandomToken(9)` 后直接 `Upsert`（`ON CONFLICT DO UPDATE`）。**Risk**: 极低概率碰撞会静默覆盖已有客户端记录（名称、secret、redirect URIs）。**Suggested fix**: 插入前 `Get` 循环重试，或 `INSERT` 遇 unique violation 重试（≤3 次）。

### P3 — Low

- **[server/internal/core/oauth/oauth.go:120]** 未校验 `code_challenge_method` — **Issue**: authorize 只要求非空 `code_challenge`；token 侧硬编码 S256（`token.go:207–221`）。**Risk**: 与 RFC 7636 字面不完全一致；实际兑换仍只接受 S256，无已知利用链。**Suggested fix**: authorize 拒绝非 `S256`/空 method（OAuth 2.1 默认 S256）。

- **[web/src/api/client.ts:36]** `cache: "no-store"` 作用于全部 admin `fetch` — **Issue**: spec 仅要求 JWKS GET 绕过浏览器缓存；实现为全局。**Risk**: 轻微多余网络流量；功能上满足 p3-1-fix2。**Suggested fix**: 可改为仅对 `fetchJwks` 传 `cache: "no-store"`，或保留现状并注释为 intentional。

- **[server/internal/httpapi/handlers_oauth.go:20]** 注释仍写 `code_challenge?` 可选 — **Issue**: 与强制 PKCE 不一致。**Suggested fix**: 更新 connect 路由注释为必填。

- **[server/internal/core/keys/keys_test.go:127]** 测试注释写 `p5-1` — **Issue**: spec 项为 `p5-2`。**Suggested fix**: 改注释编号。

## Spec Compliance

- **p3-1-fix2**: **met** — `web/src/api/client.ts:31–36` 全局 `cache: "no-store"`；`KeysPage.tsx:13,refresh` + `invalidateQueries` 在 generate/rotate 后刷新 JWKS；verifier 端点 `handlers_verifier.go:29` 仍 `max-age=60` 未改。
- **p3-1-fix3**: **met** — `admin.ts:104–108` `Jwk.status`；`KeysPage.tsx:77–82` active 显示 `active (signing)` badge；后端 `keys.go:112`/`jose.BuildJWKS` 已带 status。
- **p3-1-fix4**: **met** — `KeysPage.tsx:15–16,27–38` 单按钮按 `hasActiveKey` 切换 Generate/Rotate。
- **p5-2**: **met** — `keys.go:118–135` `Retire` 仅 `KeyRotating`；`handlers_admin_resources.go:405–433` owner 路由 + step-up + 404/409 + audit `issuer_key.retire`；`keys_test.go:130–161` 单测覆盖 active/unknown/re-retire。
- **p6-1**: **met** — `handlers_admin_resources.go:311–334` 系统生成 `op-client-`+`RandomToken(9)`，请求体无 `client_id`，`name` 必填；`handlers_admin_resources_test.go:645–648` 断言供给 id 被忽略。
- **p6-2**: **met** — 迁移 `0007`（sqlite+postgres `DROP COLUMN party, allowed_scopes`）；`domain/oauthclient.go`、`repo_oauthclient.go`、handler、前端字段/列/UI 已删除。
- **p6-3**: **met** — `oauth.go:118–142` authorize 恒要 challenge 且 code 恒绑；`token.go:200–216` 先 PKCE 后 secret；迁移 `0008`；前端去 `pkce_required`；e2e/单测已补 confidential verifier。
- **p6-4**: **met** — `handlers_admin_resources.go:337–363` `POST .../secret` owner+step-up、404/409、audit `oauth_client.secret_regenerate`、一次性 plaintext；`ClientsPage.tsx` Copy ID + `RegenerateSecretAction`。
- **TC-3**: **met** — Keys/Clients 页消费新 admin 契约（status、合并按钮、retire、系统 id、secret 再生）；类型与 API 层同步（`types.ts`、`admin.ts`）。

**Scope drift**: 本 diff 未更新 `server/Makefile:94` `dev-seed-client`（仍 INSERT 已删列 `party`/`allowed_scopes`/`pkce_required`），本地 dev 种子在迁移 0007/0008 后会失败；属会话外残留，建议跟进修复。

## Removal / Iteration candidates

- `server/Makefile` `dev-seed-client`：对齐迁移后 schema（删三列，authorize URL 需带 `code_challenge`）。
- `repo_oauthclient.go` 已删 `boolToInt` — 干净，无残留引用。
- `connect` 文档字符串（`handlers_oauth.go:20`）与可选 query 注释可一并更新。

## Notes / residual risk

- **PKCE 闭环**: authorization_code 路径已闭合——authorize 恒存 challenge（`oauth.go:142`），`tokenAuthCode` 仅经 `authenticateClient`（`token.go:176`），refresh 路径独立、不涉 auth code（`token.go:86–99`），无跳过 PKCE 的兑换路径。遗留 DB 中无 challenge 的未消费 code 在升级后将无法兑换（`ErrInvalidGrant`），属预期破坏性变更。
- **迁移 0007/0008**: `DROP COLUMN` 对 sqlite（modernc 3.45+）与 postgres 均合法；被删列为 `NOT NULL` 死配置，前向迁移安全，旧值丢失可接受。
- **Key retire**: 无法退役 active key（`keys.go:130–133`）；退役 rotating key 是运维判断（UI 已警告 token 过期风险），非代码缺陷。
- **Secret 再生**: 哈希存储、列表脱敏（`adminListClients` nil `ClientSecretHash`）、响应仅一次 plaintext；disabled 客户端未拦再生，视为可接受运维能力。
- **前端**: `RegenerateSecretAction` 与 `StepUpDialog` 分工清晰（step-up 成功后开一次性 secret 对话框）；`(c.AllowedAudiences ?? [])` 防空；table `key={c.ClientID}` / `key={k.kid}` 正确。
