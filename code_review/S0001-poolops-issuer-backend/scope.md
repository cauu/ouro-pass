# Review Scope — S0001 Ouro Pass Issuer 后端

- **Case**: 1 (review the spec's full changes) — 整体 review of the entire `server/` Go backend at `main` HEAD (`60b46f8`). All of `server/` is the output of spec S0001.
- **Base ref**: `19da9ae` (pre-Go baseline: only `.gitignore` + design docs). Diff `19da9ae..HEAD -- server/`.
- **Diff**: 119 files changed, ~10,120 insertions. Non-test Go: 75 files, ~6,062 LOC. Test Go: ~3,484 LOC.
- **Full diff file**: `tmp/review/S0001-poolops-issuer-backend/changes.diff`
- **Also review the actual source tree** under `server/` directly (agents run in the repo) — this is a holistic review, not just a line diff.
- **Spec standard**: `docs/specs/20260623T0041-S0001-poolops-issuer-backend.md` (active).

## Reviewers
- **cursor-agent** (auto model) — `cursor.md`
- **Claude subagent** (general-purpose) — `claude.md`
- **codex** — SKIPPED (user reports codex unavailable this session)
- Primary Claude synthesizes + independently re-confirms → `summary.md`

## System under review (what it is)
Self-hosted (per Cardano SPO) **staking-identity OAuth login + multi-channel subscription** backend. Go + chi + net/http. Planes: wallet-primitive / OAuth issuance / verifier / admin, plus Telegram bot + push + reconciliation workers. Persistence: hand-written `database/sql` repos, dual-stack PG (pgx) / SQLite (modernc, no CGO).

## Acceptance standard — Constraints (C1–C10)
- **C1** 语言/框架：Go + chi + net/http；不引入 Gin/Echo/Fiber。
- **C2** 签发统一走 OAuth：签发=`authorization_code`、刷新=`refresh_token`，均在 `POST /api/oauth/token`；无独立 license issue/refresh 端点。
- **C3** 私钥隔离：服务仅持 issuer 签名密钥(加密落盘)+bot token；cold/owner/KES/VRF/payment key 不进服务；owner key 仅用于 admin 登录签名、不存服务；节点 socket 仅只读 ledger 查询。
- **C4** 金额用大数：lovelace 用 `numeric(20)` / `math/big`，禁止 int64 / JS number。
- **C5** 敏感字段加密落盘：私钥、bot token、client_secret 等加密存储。
- **C6** Access token 短效+PoP：JWS/EdDSA(header 无 cert_hash/无证书链)，Access 1–7 天、Activation 5–60 分；public client 设备 PoP(`cnf.jkt`)，confidential client holder-of-key。
- **C7** 资格仅基于 active snapshot，按 epoch 由 Reconciliation 重算；委托进出 ≤2 epoch 滞后视为隐含宽限。
- **C8** 无 Member 表：身份键 = `stake_credential_hash`；对外 `sub = base32(HMAC(server_salt, stake_credential_hash))`。
- **C9** 无冷钥 license：不实现三层证书链/OwnerAuthCert/CertRevocation/`/api/certs/*`/`/crl`/JWKS 证书链；JWKS 仅发布签名公钥；admin owner 对照链上 pool owner 列表(本期 allowlist 近似, D9)。
- **C10** `core/rules` 纯函数：`Evaluate(snapshot, rules, epoch) → Decision` 无 IO/无副作用/不读时钟；多规则按 priority 排序后命中；确定性。`core/keys` 不强求纯。

## Acceptance standard — TC-1 … TC-12
- **TC-1** 启动：`go build ./...` 通过；健康检查 200；四平面路由可达(404/401 符合预期)；SIGTERM 优雅退出。
- **TC-2** 持久层双栈：同一 repo 接口在 SQLite 与 PG 均通过(CRUD + jsonb/TEXT 往返)。
- **TC-3** CIP-30 验签：真实钱包签名 golden vector 校验 `signData`(COSE_Sign1) 通过；篡改 nonce/签名被拒。
- **TC-4** JOSE：access token JWS 可被独立 verifier 用 JWKS 验签；`kid` 正确(无 cert_hash)；JWKS 符合 §9.6(仅签名公钥、无证书链)。
- **TC-5** 资格引擎：snapshot+rule_config → 资格/tier/entitlements 符合规则(min_active_stake、grace、priority)；纯函数(同输入确定、无时钟/IO)。
- **TC-6** 授权码流：`/api/connect/authorize` 验签合格→带 code；`/api/oauth/token` 换码返回 access+refresh；不合格→`not_eligible`。
- **TC-7** 刷新与盗用检测：refresh 轮换发新 access+新 refresh；旧 token 重放触发撤销链 `invalid_grant`；掉级降级/`403 not_eligible`。
- **TC-8** 密钥轮换/吊销：rotate 生成新 kid → JWKS overlap；revoke 后 introspect 反映吊销。
- **TC-9** 渠道激活+Telegram：activation/create 返回 token+deep link；bot `/start <code>` 建 SubscriptionSession 且标记 jti 已消费(零查链)；`/status`、`/unsubscribe` 按 from.id。
- **TC-10** 推送：PushJob → 按 tier/topic/entitlement 过滤 active session → sendMessage 限速+退避+DeliveryLog。
- **TC-11** Reconciliation：epoch 边界重算，掉级 session 自动降级/失效。
- **TC-12** Admin：owner-key 签名登录下发 session；RBAC 拒越权；敏感操作缺 step-up 被拒；操作写 AuditLog。

## 评审重点提示 (review focus)
钱包签名验证 (CIP-8/COSE 自实现, `utils/crypto/cose.go`)、OAuth 授权码/刷新轮换与盗用检测 (`core/oauth`)、伪匿名 `sub` 派生与 server_salt、SQL 拼接/注入面 (`store/repo_*.go`)、字段加密 (`utils/crypto/fieldcipher.go`)、PKCE/DPoP 校验、admin RBAC + step-up、错误信息泄露、PG/SQLite 方言分叉、worker 并发/限速/退避。
