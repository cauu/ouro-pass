# Cursor Review — S0001-test-completeness

Scope reviewed: test-completeness audit (unit + integration + e2e)
Coverage: 73.6% total (with e2e); per-package + by-func in the artifacts
Spec standard: docs/specs/20260623T0041-S0001-poolops-issuer-backend.md

## Assessment

Overall: **NEEDS_MORE_TESTS**

核心 OAuth / COSE / refresh 盗用链 / PG 并发 CAS 覆盖扎实，但 p12-4 push `Worker.Run` 轮询、`keys.Revoke`、admin 读/变更端点、TC-14 rotate→mint 回滚、PG 全量 repo 双栈、e2e reconciliation 与若干路由级安全否定用例仍缺，发布前存在静默回归窗口。

## Findings

### P0 — Critical test gaps

- **[server/internal/worker/push/worker.go:31 `Run`]** Push worker 轮询路径零覆盖
  - Untested: `NewWorker` + `Run` 通过 `ListScheduled` 拾取 `status=scheduled` 的 job 并调用 `Scheduler.Run`；e2e 在 `e2e_test.go:398-401` 直接调 `sched.Run`，绕过 worker。
  - Risk: p12-4 修复（admin 建 job → worker 投递）若 `ListScheduled` 查询/SQL 方言/轮询间隔接线回归，单元+e2e 均不会失败。
  - Suggested test: SQLite store 插入 `scheduled` job + `scheduled_at<=now`；`recordingSender` + `Worker` 以 `poll=10ms` 启动；`ctx` 超时前断言 `DeliveryLog` 有 `sent` 且 job→`done`（不直接调 `Scheduler.Run`）。

- **[server/internal/core/oauth/token.go:137-156 `tokenRefresh` WithTx]** Rotate→mint 同事务失败回滚未测
  - Untested: `RotateIfActive` 成功后 `mint` 失败时事务回滚，旧 grant 保持 `active`（TC-14 文本要求）。
  - Risk: mint 写库/签名失败时 grant 被置 `rotated` 却无 successor，用户 refresh 永久 `invalid_grant`。
  - Suggested test: harness 中 stub `mint` 或注入 `IssuedTokens.Create` 失败；refresh 一次后断言旧 grant 仍为 `GrantActive`、`RotateIfActive` 二次返回 false。

### P1 — High

- **[server/internal/core/keys/keys.go:141 `Revoke`]** 签名密钥紧急吊销零覆盖
  - Untested: `Revoke(ctx, kid)` 将 key 标为 `revoked` 且影响 JWKS/验签行为。
  - Risk: 妥协响应路径接入 HTTP 或 cron 后，状态机错误不会触发任何测试失败。
  - Suggested test: `testService` 中 `Rotate` 得 `kid1`，`Revoke(ctx, kid1)`；`PublicJWKSKeys` 不含 revoked kid；用 revoked kid 签发的 token `Introspect`→inactive（若仍发布则失败）。

- **[server/internal/httpapi/handlers_admin_resources.go:130-147,151-157,223-230,268-277,366-373]** Admin 读/变更端点零执行
  - Untested: `adminSubscriptions`、`adminCancelSub`、`adminListRules`、`adminListPushJobs`、`adminListClients`、`adminAudit`（coverpkg 均为 0%）。
  - Risk: 路由挂载/SQL 扫描/RBAC 中间件对这些路径的回归（404、500、越权）无测试网。
  - Suggested test: 沿用 `adminResourceEnv`：operator `GET /api/admin/rules`→200 且 body 含已 upsert 规则；`POST /subscriptions/{id}/cancel`→200 且 DB `SubCancelled`；owner `GET /api/admin/audit`→200；viewer `GET /subscriptions`→200。

- **[server/internal/e2e/e2e_test.go — missing Flow E]** E2E 缺 reconciliation 全链路
  - Untested: spec TC-24 声称 6 条主链路，实际仅 5 个 `TestE2E_*`（无 reconciliation）；`reconciliation.Run` 仅在 `reconciliation_test.go:150-167` 单包测。
  - Risk: `NewRouter` + mock epoch 推进 + subscription 降级/过期的接线 bug 在 CI 默认 `make test` 不可见。
  - Suggested test: e2e 种子 active gold session + mock chain epoch 递增 + 资格变不合格；驱动 `reconciliation.New(...).Run` 或挂到 env；断言 session→`expired`/`tier` 变更。

- **[server/internal/httpapi/handlers_verifier_introspect.go:14-28 `introspect`]** HTTP 层裸 `jti` 枚举防护未测
  - Untested: handler 固定 `Introspect(..., b.token, "")`，忽略 body 中 `jti`（p12-9）；无 HTTP 测试 POST `{"jti":"known-jti"}` 无 token。
  - Risk: 重构时恢复 `jti` 直通会重新开放未鉴权 token 状态 oracle。
  - Suggested test: 种子 active `IssuedToken`；`POST /api/oauth/introspect` 仅 `{"jti":"<jti>"}`→200 `active:false`（与 `introspect_test.go:53` 核心行为一致但在路由层证明）。

- **[server/internal/httpapi/router.go:49-62 + handlers_admin.go:146-158]** 路由级限流与 TrustedProxy IP 解析未 E2E 验证
  - Untested: `publicLimit` 挂在 `/api/oauth/token` 等路由上返回 429（TC-21）；`TrustedProxy=true` 时 admin `clientIP` 取 `X-Forwarded-For` 最右跳（TC-18）。
  - Risk: 中间件单测通过但路由未挂/挂错限流器；审计 IP 仍取 `RemoteAddr` 导致 spoof 或限流绕过。
  - Suggested test: httptest 对同一 IP 连发 41 次 `POST /api/oauth/token`→第 41 次 429；`Deps{TrustedProxy:true}` + `X-Forwarded-For: 1.2.3.4, 10.0.0.1` 登录 admin 后查 `Audit().Recent` IP 字段。

- **[server/internal/inttest/pg_concurrency_test.go — TC-2 gap]** PG 仅 OAuthClient 方言往返
  - Untested: `store/repo_*_test.go` 全套 CRUD（jsonb/TEXT/时间/lovelace）未在 PG 重跑；`TestPG_DialectRoundTrip` 只覆盖 `OAuthClients`（`pg_concurrency_test.go:181-211`）。
  - Risk: PG 专属 `Rebind`/扫描/迁移 DDL 在某 repo 上静默失败，SQLite 单测仍绿。
  - Suggested test: 抽取共享 `TestAllReposRoundTrip(t, openStore)`，`-tags integration` 用 `pgStore` 跑一遍（或 table-driven 覆盖 PushJob `ListScheduled`、Subscription `ListActive` 等 0% PG 函数）。

- **[server/internal/core/oauth/refresh_test.go — TC-7 gap]** Refresh 资格降级（仍合格但 tier 下降）未测
  - Untested: `tokenRefresh` 重算 eligibility 后 `mint` 使用新 `decision.Tier`；仅有完全不合格 `TestRefresh_ReEvaluatesEligibility`（`refresh_test.go:81-89`）。
  - Risk: tier 降级逻辑（gold→silver）在 refresh 路径回归不会失败。
  - Suggested test: 发 token 时 gold；更新 chain snapshot 使 stake 仅满足 silver；refresh→assert access token `tier` claim 为 `silver` 且 subscription 未误拒。

- **[server/cmd/issuer/main.go:87-149 — TC-16 gap]** Main worker 生命周期仅 smoke，无可重复单测
  - Untested: `startWorker`/`workers.WaitGroup` join push+reconciliation+telegram；`main_test.go` 仅测 `buildServices` 与 `runNonceGC`（`main_test.go:29-86`）。
  - Risk: worker 未启动或 shutdown 不 join 的回归只能靠手工二进制 smoke。
  - Suggested test: 抽 `startWorkers(deps, cfg, st, ctx)` 可注入 fake worker；cancel ctx 后断言 `WaitGroup` 在超时内归零。

### P2 — Medium

- **[server/internal/utils/crypto/cose.go:40-65 `ParseCOSESign1`]** 畸形 COSE 输入分支未全覆盖（68.8%）
  - Untested: 非 4 元组、protected/payload/signature CBOR 解析失败、空输入等返回 `ErrCOSEMalformed`。
  - Risk: 恶意输入 panic 或错误类型映射变化不被捕获。
  - Suggested test: table-driven 截断 CBOR、3 元素数组、非法 protected map→assert `ErrCOSEMalformed`。

- **[server/internal/core/oauth/activation_test.go — blacklist]** 激活路径 blacklist 否定用例缺失
  - Untested: `CreateActivation` 经 `evaluate` 拒绝 blacklist（`oauth.go:155-159`）；仅有 ineligible/bad-signature（`activation_test.go:39-59`）。
  - Risk: blacklist 仅覆盖 `TestAuthorize_BlacklistedRejected`（`oauth_test.go:136`），激活面回归独立。
  - Suggested test: `Blacklist.Add(sch)` 后 `CreateActivation`→`ErrNotEligible` 或等价错误。

- **[server/internal/store/repo_authorizationcode.go:29 `Consume`]** 过期授权码分支（72.7%）
  - Untested: 过期 `AuthorizationCode.Consume`→`ErrExpired`/`ErrNotFound`（nonce 有过期测试 `repo_tokens_test.go:131-134`，auth code 对称路径未显式测）。
  - Risk: 过期码仍可兑换的 CAS/时间守卫回归。
  - Suggested test: 创建 `ExpiresAt` 过去的 code，`Consume`→`ErrExpired`。

- **[server/internal/httpapi/middleware/middleware_test.go:44-80 — TC-19 gap]** 幂等中间件负向用例不全
  - Untested: 500 响应不缓存（`middleware.go:170-174`）；同 key 不同 `Method+Path` 不串响应；TTL 清扫。
  - Risk: 失败响应被缓存导致重试永远拿到旧 500（p12-7 回归）。
  - Suggested test: handler 首次 500、同 key 重试仍执行 handler；`POST /a` 与 `POST /b` 同 key 返回不同 body。

- **[server/internal/httpapi/handlers_admin_resources_test.go — step-up HTTP]** Step-up 错 key 仅服务层覆盖
  - Untested: HTTP `adminRotateKey`/`adminRevokeMember` 在 step-up 签名正确但 `owner_vkey` 与会话不匹配→403；`admin_test.go:123-127` 仅测 `VerifyStepUp`。
  - Risk: handler 传错 hash 给 `requireStepUp` 不被 HTTP 测试捕获。
  - Suggested test: operator 登录后用另一 wallet 的 step-up 签名 POST rotate→403。

- **[server/internal/store/repo_pushjob.go:87 `ListScheduled`]** 调度查询未单测（0% coverpkg）
  - Untested: `scheduled_at IS NULL OR scheduled_at <= now` 过滤与排序；push worker 依赖此查询。
  - Risk: SQL 条件错误导致 job 永不投递或重复投递。
  - Suggested test: 插入 future/past/null `scheduled_at` 三条 job，`ListScheduled(now, 10)` 仅返回 due 条目。

### P3 — Low

- **[server/internal/worker/telegram/transport_botapi.go]** Bot API HTTP 传输层 0% — 集成适配器，可接受由 fake transport 覆盖。
- **[server/cmd/issuer/main.go:46,56 `main`/`run`]** 阻塞服务循环仅 smoke — 符合 scope「cmd serve loop 可接受 0%」。
- **[server/internal/utils/crypto/random.go]** 原包 0% 但 coverpkg 100% — 经全链路间接执行，无需独立 primitive 测试。

## Spec Compliance (TC coverage)

- TC-1: **partially-met** — `router_test.go:44-64` 健康检查+优雅关停；`main_test.go:29-73` `buildServices`；`main`/`run` 服务循环无单测（仅 smoke）
- TC-2: **partially-met** — SQLite：`store/repo_*_test.go` + `store_test.go:60-83`；PG：`inttest/pg_concurrency_test.go:181-211` 仅 OAuthClient
- TC-3: **partially-met** — `crypto_test.go:101-161` + `walletauth_test.go:48-129` 合成 COSE；**无真实钱包 golden vector**（spec D5 仍 deferred）
- TC-4: **met** — `jose_test.go:13-76`
- TC-5: **met** — `engine_test.go:23-100`
- TC-6: **met** — `oauth_test.go:109-147` + `e2e_test.go:205-267`
- TC-7: **partially-met** — `refresh_test.go:55-79` 盗用链；`refresh_test.go:81-89` 完全不合格；**tier 降级 refresh 无测试**
- TC-8: **partially-met** — `keys_test.go:32-111` + `e2e_test.go:417-438` 轮换/overlap；`introspect_test.go:40-51` revoke；**`keys.Revoke` 未测**
- TC-9: **met** — `activation_test.go:14-59` + `telegram_test.go:50-112` + `e2e_test.go:354-405`
- TC-10: **partially-met** — `push_test.go:67-166` Scheduler 过滤/退避/日志；**`push/worker.go` Worker 未测**
- TC-11: **met**（单元）— `reconciliation_test.go:54-167`；**e2e 未覆盖**
- TC-12: **partially-met** — `admin_test.go:55-128` + `handlers_admin_test.go:42` + `handlers_admin_resources_test.go:72-244`；**list/audit/cancel 端点未测**；step-up 错 key 仅服务层
- TC-13: **met** — `repo_tokens_test.go:175-229` + `inttest/pg_concurrency_test.go:95-151`
- TC-14: **partially-met** — `repo_tokens_test.go:71-99` + `inttest/pg_concurrency_test.go:154-174`；**rotate+mint 回滚无测试**
- TC-15: **met** — `reconciliation_test.go:107-138`
- TC-16: **partially-met** — `push_test.go` Scheduler；**`worker.go:Run` 与 main WaitGroup join 无自动化单测**
- TC-17: **met** — `token_test.go:134-188` + `e2e_test.go:271-303`
- TC-18: **partially-met** — `middleware_test.go:11-41` 默认 RemoteAddr；**`handlers_admin.go:146-158` TrustedProxy 路径无测试**
- TC-19: **partially-met** — `middleware_test.go:44-80` 基本 replay；**500 不缓存 / method+path 隔离 / TTL 清扫无测试**
- TC-20: **met** — `stakeaddr_test.go:9-17` + `engine_test.go:87-97`
- TC-21: **partially-met** — `introspect_test.go:52-55` 核心裸 jti；**HTTP handler 忽略 jti 无测试**；**路由 429 无测试**
- TC-22: **met** — `config_test.go:36-60` + `crypto_test.go:143-161` + 全量 `go test ./...` 绿
- TC-23: **partially-met** — `config_test.go` + `main_test.go`；httpapi 负向有增补但 admin 读端点仍空
- TC-24: **partially-met** — `e2e_test.go` **5 条非 6 条**；缺 reconciliation；push 走 Scheduler 非 Worker
- TC-25: **met**（并发 CAS）— `inttest/pg_concurrency_test.go:95-174`；**全量 repo PG 往返未 met**
- TC-26: **met** — Makefile/CI 编排（spec §6 已记录 pass）

## Test quality notes

- **E2E 绕过 worker 接线**：`e2e_test.go:376-401` 直接调 `telegram.NewProcessor` 与 `push.NewScheduler.Run`，无法捕获 main 未启动 push worker 类 bug（与 scope 自述一致）。
- **Reconciliation `Run` 测试时序敏感**：`reconciliation_test.go:150-167` 用 200ms `context.WithTimeout` 等 epoch 触发，在极慢 CI 上可能 flaky（当前断言 session expired，逻辑合理但边界紧）。
- **PG 并发测试设计良好**：`inttest/pg_concurrency_test.go:72-90` `start.Wait()` 对齐起跑线，「恰好 1 次成功」对 CAS 语义正确（失败者返回非 nil error，不计入 `won`）。
- **Admin 测试偏状态码**：`handlers_admin_resources_test.go` RBAC 多用 `postCode`/`getCode`，对 `adminCancelSub` 等未测路径无法发现 body/DB 不一致。
- **幂等/限流仅中间件单测**：未证明 `router.go` 实际挂载到 issuance 平面（TC-18/19/21 证据弱于 spec 文案）。

## Strengths (what's well covered)

- OAuth 授权码全生命周期（confidential + public PKCE/PoP）在单元与 e2e 双重覆盖，含 refresh 盗用链撤销（`refresh_test.go:55-79`，`e2e_test.go:244-257`）。
- COSE/CIP-8 对抗性测试扎实：篡改 payload/签名/alg/缺 alg header（`crypto_test.go:101-161`），walletauth 重放与 purpose 绑定（`walletauth_test.go:71-128`）。
- P0 CAS 双花在真实 PG 并发下得证（`inttest/pg_concurrency_test.go:95-174`，N=24）。
- Push Scheduler 业务过滤、退避重试、DeliveryLog 计数完整（`push_test.go:67-166`）。
- Admin 核心安全面：RBAC 矩阵、step-up 门禁、cascade revoke、错误信封不泄漏（`handlers_admin_resources_test.go:72-177`）。
- Reconciliation 降级/过期/故障隔离（`reconciliation_test.go:54-138`）。
