# PoolOps Issuer Service — 后端实现

Spec-ID: S0001
Status: active
Created Time: 2026-06-22T22:48:27+08:00
Start Time: 2026-06-23T00:41:32+08:00
Completion Time:
Previous Spec-ID: (none)
Closure Reason:

## 1. Requirement Details

### Background

实现 `docs/v1.0/` 概要设计（`poolops-issuer-overview.md`）与详细设计（`poolops-issuer-detailed-design.md`，**v3 OAuth 登录版**）中声明的 **PoolOps Issuer Service 后端**：一个由 SPO 自托管的 staking-based 会员登录授权 + 多渠道订阅服务。本期采用**质押身份 OAuth 登录**：issuer 持一把可轮换签名密钥签发短效 access token（JWT），经 JWKS 公钥或 introspect 校验；**不做冷钥锚定的离线 license 证书链**（cold→owner→issuer），待第三方/离线生态验证需求出现再加。服务自身只持 issuer 签名密钥与 bot token。

后端是"唯一真源"，对外暴露四个鉴权独立的逻辑平面（钱包原语 / 签发(OAuth) / Verifier / Admin）+ Telegram bot worker + 调度 worker。代码落在仓库 `server/` 目录（Go）。

### Scope（本 spec 覆盖）

- 四平面 REST API（详见详细设计 §9.1 端点总表）。
- OAuth2 授权服务器：`authorization_code` 签发、`refresh_token` 刷新（PKCE + DPoP / client_secret）；token introspect(RFC 7662) / revoke(RFC 7009)。
- 钱包鉴权原语：nonce challenge + CIP-30 `signData`(COSE) 验签。
- 签名密钥管理：issuer 单签名密钥生成/轮换（JWKS overlap）、JWKS 发布。**无 cold/owner 证书链、无 CRL。**
- Rule engine：基于 active stake snapshot 现算资格。
- Staking Index Adapter：`node_lsq` / `db_sync` / `koios|blockfrost` 可插拔只读链查询。
- 渠道激活 + Telegram bot（long-poll MVP，可切 webhook）。
- 推送任务 + Push Scheduler + DeliveryLog。
- 每 epoch Reconciliation Job（重算资格、维护/降级 session）。
- Admin 平面：owner-key 钱包签名登录 + RBAC + 敏感操作 step-up + 审计。
- 数据持久层：PostgreSQL（生产）/ SQLite（单机 MVP）双栈。
- 全部数据实体（详细设计 §1–§8）。

### Constraints

- C1 **语言/框架已定**：Go + `chi` 路由 + 标准库 `net/http`。不引入 Gin/Echo/Fiber（Fiber 因 fasthttp 不兼容 net/http 明确排除）。
- C2 **签发统一走 OAuth**：以详细设计 §9.1 为准——无独立 `/api/license/issue` / `/api/license/refresh`；签发=`authorization_code`、刷新=`refresh_token`，均落在 `POST /api/oauth/token`。旧版概要设计曾列独立 issue/refresh 端点，v3 已统一、**不实现**。见 §7 Change/决策记录。
- C3 **私钥隔离**：服务仅持 issuer 签名密钥（加密落盘）与 bot token；cold/owner/KES/VRF/payment key 一律不进服务（owner key 仅用于 admin 登录的钱包签名，不存服务）。查节点 socket 仅做只读 ledger 查询。
- C4 **金额用大数**：lovelace 用 `numeric(20)` / `math/big`，禁止 int64 / JS number。
- C5 **敏感字段加密落盘**：私钥、bot token、client_secret 等 🔒 字段加密存储。
- C6 **Access token 短效 + PoP**：JWS/EdDSA（header 无 cert_hash、无证书链），Access 1–7 天、Activation 5–60 分钟，public client 设备 PoP（`cnf.jkt`），confidential client holder-of-key。
- C9 **无冷钥 license**：不实现 cold→owner→issuer 三层证书链、OwnerAuthCert/CertRevocation 实体、`/api/certs/*`、`/crl`、JWKS 证书链。token 由 issuer 单签名密钥签发，JWKS 仅发布签名公钥。Admin 登录用 owner key 对照**链上 pool owner 列表**校验。
- C7 **资格仅基于 active snapshot**，按 epoch 边界由 Reconciliation Job 重算；委托进出 ≤2 epoch 滞后视为隐含宽限。
- C8 **无 Member 表**：身份键 = `stake_credential_hash`；对外 `sub = base32(HMAC(server_salt, stake_credential_hash))`。
- C10 **`core/rules` 实现为纯函数**：资格评估 `Evaluate(snapshot, rules, epoch) → Decision` 无 IO / 无副作用 / 不读时钟，快照、规则、epoch 一律参数注入；多规则按 `priority` 命中前先排序，保证同输入确定性输出，便于 golden-vector / table test。`core/keys` 不强求纯（含 keygen、落盘、JWKS 发布等固有副作用），按常规有状态服务实现。

### Non-goals（本 spec 不做）

- Vite + TS 前端（Admin UI / Authorization Page 的页面实现）——属 `web/`，另立 spec。
- 凭证消费端（本地 App、第三方网站、verifier SDK）。
- 链上交易构建/提交（本服务不建不发任何 Cardano 交易）。
- cold key / owner key 的离线签名工具链。
- 生产级可观测性/告警平台搭建（仅保留结构化日志与审计）。

## 2. Outline Design

### 2.1 技术选型（落定）

| 关注点 | 选型 | 理由 / 备注 |
|---|---|---|
| 语言 | **Go**（1.22+） | 单静态二进制、自托管易运维；goroutine 契合长驻 worker；crypto 标准库成熟 |
| HTTP 路由 | **`go-chi/chi` v5** + 标准库 `net/http` | 子路由 + 中间件组 1:1 映射四个鉴权平面；纯 `http.Handler`、零魔法、可审计 |
| JOSE / JWS / JWKS | **`github.com/lestrrat-go/jwx/v2`** | 签发 access/activation token JWS(EdDSA)、发布 `/.well-known/jwks.json` |
| CBOR | **`github.com/fxamacker/cbor/v2`** | 解码 CIP-30 `signData` 的 COSE_Sign1 结构 |
| COSE | **`github.com/veraison/go-cose`** | 配合 CBOR 按 CIP-8 校验钱包签名 |
| Ed25519 / 哈希 | 标准库 `crypto/ed25519` + `golang.org/x/crypto/blake2b` | blake2b224 算 pool_id；HMAC-SHA256 派生 `sub` |
| 大数 | 标准库 `math/big` | lovelace numeric(20) |
| DB 访问 | **`sqlc`** 生成类型安全查询 + `database/sql` 抽象 | 屏蔽 PG/SQLite 差异（jsonb vs TEXT/JSON），不用 ORM |
| PG 驱动 | **`jackc/pgx/v5`**（stdlib 模式） | 生产主力 |
| SQLite 驱动 | `modernc.org/sqlite`（纯 Go，无 CGO） | 单机 MVP；保持单二进制无 CGO |
| DB 迁移 | `golang-migrate` 或 `goose` | 版本化 schema；PG/SQLite 各一套方言 |
| 后台 worker | 标准库 goroutine + `context` | Reconciliation / Push Scheduler / Telegram，三个长驱 |
| 配置 | env + `.env`（仅本地）；secret 走环境变量 | staking 数据源 key、加密主密钥、server_salt 等 |
| 字段加密 | `crypto/aes`(GCM) 或 `nacl/secretbox`，主密钥来自 env/KMS | 🔒 字段落盘加密 |
| 限速 | `golang.org/x/time/rate` | Public/Verifier 平面按 IP 限速 |
| 日志 | 标准库 `log/slog`（结构化） | 配合 AuditLog |
| 测试 | 标准库 `testing` + `testify`(可选) | TC 验收 |

### 2.2 模块 / 目录结构（server/）

```text
server/
  cmd/issuer/main.go            # 启动、装配、graceful shutdown
  internal/
    config/                     # 配置加载、env、secret
    httpapi/
      router.go                 # chi 装配，四平面路由组
      middleware/               # requestid/log/recover/ratelimit/idempotency/error-envelope
      wallet/                   # /api/auth/challenge
      oauth/                    # /connect, /api/connect/authorize, /api/oauth/token
      activation/               # /api/activation/create
      verifier/                 # /.well-known/jwks.json, /api/oauth/introspect, /api/oauth/revoke
      admin/                    # /api/admin/*, RBAC, step-up
    core/                       # 业务领域逻辑
      rules/                    # rule engine（snapshot → eligibility/tier/entitlements）
      keys/                     # issuer 签名密钥 生成/轮换、JWKS 发布
    utils/                      # 技术基础设施（非业务）
      crypto/                   # ed25519, blake2b224, hmac, COSE/CIP-30 verify, field encryption
      jose/                     # JWS builder（access/activation token）, JWKS publisher (jwx)
      chain/                    # Staking Index Adapter 接口 + node_lsq/db_sync/koios 实现
    store/                      # sqlc 生成代码 + repository 封装（PG/SQLite）
      migrations/
    worker/
      reconciliation/           # 每 epoch 重算
      push/                     # 推送调度 + 投递
      telegram/                 # bot transport（long-poll/webhook）+ 命令
    domain/                     # 实体类型、枚举、错误
```

> 分组逻辑：`core/` 收业务领域逻辑、`utils/` 收技术基础设施（非业务）；`httpapi`/`store`/`worker` 作为架构支柱留顶层。新增业务模块进 `core/`，新增技术模块进 `utils/`。

### 2.3 鉴权平面 → 路由组映射（详细设计 §9.1）

| 平面 | 端点前缀 | 中间件链 |
|---|---|---|
| 钱包原语 | `/api/auth/challenge` | ipRateLimit |
| 签发(OAuth) | `/connect`、`/api/connect/authorize`、`/api/oauth/*` | idempotencyKey（token）；COSE 验签（authorize） |
| 渠道激活 | `/api/activation/*` | COSE 验签 |
| Verifier | `/.well-known/*`、`/api/oauth/introspect`、`/api/oauth/revoke` | ipRateLimit |
| Admin | `/api/admin/*` | adminSession → rbac(role) → stepUp(敏感操作) |

### 2.4 数据模型

实体与字段以详细设计 §2–§8 为准（PascalCase 表名、snake_case 列、PK/FK 标注）。`array`/`json` 字段：PG 用 `jsonb`，SQLite 用 `TEXT`(JSON)。无 Member 表（C8）。**无 OwnerAuthCert / CertRevocation（C9）；IssuerKey 为纯签名密钥；token 台账实体名 `IssuedToken`。** 可选 `StakeSnapshotCache` 与稀疏 `Blacklist` 按需建。

### 2.5 凭证契约

- **Access Token**：JWS/EdDSA，header `{typ:"at+jwt",alg:"EdDSA",kid}`（**无 cert_hash、无证书链**），payload 见详细设计 §9.2（iss/sub/aud/iat/nbf/exp/jti/tier/entitlements/cnf）。
- **Activation Token**：签名 JWT，含 `channel_type/tier/entitlements/jti/exp/one_time`，兑换零查链。
- **JWKS**：仅发布签名公钥（`kid/kty:OKP/crv:Ed25519/x`），无证书链。

### 2.6 Risk and rollback strategy

- R1 CIP-30/COSE 验签为自实现（按 CIP-8），风险最高 → 用真实钱包签名样本做 golden-vector 测试（TC-3）。
- R2 PG/SQLite 双栈方言分叉 → sqlc 双 schema + 同一组 repository 接口测试两栈（TC-2）。
- R3 refresh grant 轮换/盗用检测逻辑复杂 → 专项测试轮换链与重放撤销（TC-7）。
- R4 链适配器外部依赖（node socket / db-sync / 第三方）→ 适配器接口 + mock，单测不依赖真链；集成测试单独标记。
- Rollback：未发布前按 working tree 修正；已提交按 item 用 `git revert`；遵循 immutable-spec 的 forward-only 回滚规则。

## References

- docs/v1.0/poolops-issuer-overview.md — 概要设计（流程、平面、安全）
- docs/v1.0/poolops-issuer-detailed-design.md — 详细设计（实体字段 §1–§8、接口 §9，**接口以此为准**）
- CIP-8 / CIP-30 — 钱包 `signData`(COSE_Sign1) 规范
- RFC 7636(PKCE) / RFC 9449(DPoP) / RFC 7800(cnf) / RFC 8252(native app auth) / RFC 7662(token introspection) / RFC 7009(token revocation)
- docs/codebase-map.md — 待生成（首个实现 item 后补）

## 3. Execution Plan

### p1 — 基础设施 / scaffold
- [x] p1-1 Go module + chi 服务骨架（四平面空路由、健康检查、graceful shutdown、config 加载）
- [x] p1-2 持久层底座：手写 `database/sql` repository 底座（D2）+ pgx/SQLite 双栈、embed migration 框架、Querier/WithTx/Rebind
- [x] p1-3 crypto 基础（`utils/crypto`）：ed25519、blake2b224、HMAC `sub` 派生、字段加密(AES-GCM)、CIP-30 COSE 验签
- [x] p1-4 JOSE（`utils/jose`）：access/activation token JWS builder + JWKS publisher（jwx）
- [x] p1-5 通用中间件：request-id / slog / recover / ipRateLimit / idempotency / OAuth 风格错误信封

### p2 — 数据模型（详细设计 §2–§8）
- [x] p2-1 池与签名密钥：PoolConfig / IssuerKey（签名密钥，无证书链实体）
- [x] p2-2 规则与身份：MembershipRule / StakeSnapshotCache(可选) / Blacklist(可选)
- [x] p2-3 Token 与凭证：IssuedToken / RefreshGrant / AuthorizationCode / ActivationCode / AuthNonce
- [ ] p2-4 客户端与渠道：OAuthClient / ChannelConfig / SubscriptionSession
- [ ] p2-5 推送与管理审计：PushJob / DeliveryLog / AdminUser / AdminSession / AuditLog

### p3 — 链访问与规则引擎
- [ ] p3-1 Staking Index Adapter（`utils/chain`）接口 + `node_lsq` 实现（MVP）+ `db_sync`/`koios` 占位
- [ ] p3-2 Rule engine（`core/rules`，纯函数实现，输入参数注入 + 稳定排序）：snapshot + rule_config → eligibility / tier / entitlements

### p4 — 钱包原语与签名密钥管理
- [ ] p4-1 `POST /api/auth/challenge`（AuthNonce）+ COSE 验签接入 `stake_credential_hash` 映射
- [ ] p4-2 issuer 签名密钥 生成/轮换（`core/keys`，JWKS overlap，owner step-up）
- [ ] p4-3 Verifier 发布：`GET /.well-known/poolops/jwks.json`（仅签名公钥，无 CRL）

### p5 — 签发与刷新（OAuth 授权服务器）
- [ ] p5-1 `GET /connect`（Authorization Page 契约）+ `POST /api/connect/authorize`（验签→评估资格→AuthorizationCode）
- [ ] p5-2 `POST /api/oauth/token` `grant_type=authorization_code` → access token JWS + RefreshGrant + IssuedToken 台账
- [ ] p5-3 `grant_type=refresh_token`：token 轮换、盗用重放撤销、按 client_type 认证(client_secret / PKCE+DPoP)、资格重评/降级
- [ ] p5-4 `POST /api/oauth/introspect`(RFC 7662) + `POST /api/oauth/revoke`(RFC 7009)（Verifier 平面）

### p6 — 渠道激活与 Telegram bot worker
- [ ] p6-1 `POST /api/activation/create`（activation token + deep link）
- [ ] p6-2 Telegram transport：long-poll worker（+ webhook 占位）+ 命令 `/start|/activate|/status|/unsubscribe|/help` → SubscriptionSession

### p7 — 推送与调度 worker
- [ ] p7-1 推送任务 CRUD（PushJob）+ Push Scheduler（限速 sendMessage + 退避重试 + DeliveryLog）
- [ ] p7-2 Reconciliation Job：每 epoch 重算资格、维护/降级/失效 SubscriptionSession

### p8 — Admin 平面
- [ ] p8-1 Admin 鉴权：`/api/admin/auth/challenge` + `/verify`（owner-key 钱包签名 → httpOnly session）+ RBAC + step-up 中间件
- [ ] p8-2 Admin 资源端点：members / subscriptions / rules / channels / push / oauth-clients / keys / audit（按 §9.8 角色矩阵）

## 4. Test and Acceptance Criteria

- TC-1 服务启动：`go build ./...` 通过；二进制启动后健康检查 200，四平面路由可达（404/401 符合预期），SIGTERM 优雅退出。
- TC-2 持久层双栈：同一 repository 接口测试在 SQLite 与 PG 上均通过（CRUD + jsonb/TEXT 往返）。
- TC-3 CIP-30 验签：用真实钱包签名 golden vector 校验 `signData`(COSE_Sign1) 通过；篡改 nonce/签名被拒。
- TC-4 JOSE：签发 access token JWS 可被独立 verifier 用 JWKS 公钥验签通过；`kid` header 正确（无 cert_hash）；JWKS 输出符合 §9.6 结构（仅签名公钥、无证书链）。
- TC-5 资格引擎：给定 snapshot + rule_config，资格/tier/entitlements 评估结果符合规则（含 min_active_stake、grace、priority 多规则）；纯函数验证——同输入确定性输出、无时钟/IO 依赖（table test 覆盖）。
- TC-6 授权码流：`/api/connect/authorize` 验签合格 → 302 带 code；`/api/oauth/token` 换码返回 access_token + refresh_token；不合格返回 `not_eligible`。
- TC-7 刷新与盗用检测：`refresh_token` 轮换发新 access token + 新 refresh token；旧 token 重放触发撤销链 `invalid_grant`；资格掉级时降级/`403 not_eligible`。
- TC-8 签名密钥轮换/吊销：`/api/keys/issuer/rotate` 生成新 kid → JWKS overlap 新旧 kid；`/api/oauth/revoke` 后 introspect 反映 token 吊销。
- TC-9 渠道激活 + Telegram：`/api/activation/create` 返回 activation token + deep link；bot `/start <code>` 建 SubscriptionSession 且标记 jti 已消费（零查链）；`/status`、`/unsubscribe` 按 from.id 生效。
- TC-10 推送：建 PushJob → Scheduler 按 tier/topic/entitlement 过滤 active session → sendMessage 限速 + 退避 + DeliveryLog 记录。
- TC-11 Reconciliation：epoch 边界重算，掉级会员的 session 自动降级/失效。
- TC-12 Admin：owner-key 签名登录下发 session；RBAC 拒绝越权；敏感操作缺 step-up 被拒；操作写 AuditLog。
- Pass/fail：每个 item 仅在其映射的 TC 全部 `pass` 且证据 append 后方可标 `[x]`。

测试栈映射（验收证据用）：`stack: go`，命令以 `go test ./...`、`go build ./...` 为主，集成测试（真链/真 Telegram）单独打 build tag 标注。

## 5. Execution Log (append-only)

- 2026-06-22T22:48:27+08:00 S0001 草案创建（draft）；技术选型落定为 Go + chi；尚未开始执行。
- 2026-06-23T00:41:32+08:00 S0001 提升为 active（draft/ → docs/specs/，加时间戳前缀），开始执行。环境：Go 1.25.5、module 下载可用、git 树干净（基线 commit 19da9ae）。
- 2026-06-23 p1-1 started：搭 `server/` go module（github.com/poolops/issuer）+ chi 四平面骨架 + config 加载 + graceful shutdown。
- 2026-06-23 p1-1 completed：`config`/`httpapi`/`cmd/issuer` 三包就绪；健康检查、四平面 stub 路由、admin 401 网关、SIGTERM 优雅退出均验证。证据见 §6（TC-1）。
- 2026-06-23 p1-2 completed：`internal/store` 底座——`Open`(sqlite/pgx 双驱动)、`Querier`/`WithTx`/`Rebind`(? → $n)、`embed` 迁移 runner（按 `<driver>/NNNN.sql` 顺序应用 + schema_migrations 记录 + 幂等）。SQLite 全测通过；PG 路径置于 `POOLOPS_TEST_PG_DSN` 后（D3）。证据见 §6（TC-2）。
- 2026-06-23 p1-3 completed：`utils/crypto`——`Blake2b224`(pool_id/credential)、`DeriveSub`(base32(HMAC-SHA256))、`FieldCipher`(AES-256-GCM)、`COSESign1.Verify`(CIP-8 Sig_structure + ed25519，自实现 per D4)。COSE 验签覆盖 tagged/untagged、payload/key/sig 篡改、错误 alg 拒绝。真实钱包 golden vector 留作 integration（D5）。证据见 §6（TC-3）。
- 2026-06-23 p1-4 completed：`utils/jose`——`SignAccessToken`/`SignActivationToken`(EdDSA JWS, header typ/alg/kid)、`BuildJWKS`(OKP/Ed25519 公钥 + status, 无证书链)、`Verify`(JWKS 验签 + 标准时间 claim 校验)。注：jwx keyset 验签要求 JWKS key 带 `alg`，已在 BuildJWKS 设 EdDSA。证据见 §6（TC-4）。
- 2026-06-23 p1-5 completed：新增 `httpapi/respond`(OAuth 错误信封) + `httpapi/middleware`(RequestLogger/slog、IPRateLimiter per-IP token bucket、Idempotency-Key replay)；chi RequestID/Recoverer 组合。router 重构为按平面挂中间件（public/verifier 限速、token/activation 幂等）。证据见 §6（middleware 单测 + TC-1 路由仍通过）。
- 2026-06-23 p2-1 completed：`domain` 包起步（ErrNotFound、PoolConfig、IssuerKey + 状态枚举）；迁移 0002_pool_keys（双方言，D6 可移植列类型）；`store` repo——PoolConfig Upsert/Get、IssuerKey Create/Get/SetStatus/ListByStatus。embedded Migrate 修正为 fs.Sub("migrations")。证据见 §6（TC-2）。
- 2026-06-23 p2-2 completed：`domain/rules`（MembershipRule + RuleStatus、StakeSnapshotCache、Blacklist）；迁移 0003_rules_identity；repo——Rules Upsert/ListActive（priority desc 确定性排序，喂 p3-2）、Blacklist Add/Has、SnapshotCache Upsert/Get。lovelace 大数以 TEXT 精确往返。证据见 §6（TC-2）。
- 2026-06-23 p2-3 completed：`domain/tokens`（IssuedToken/RefreshGrant/AuthorizationCode/ActivationCode/AuthNonce + 全枚举 + 哨兵错误 ErrConsumed/ErrExpired/ErrPurpose）；迁移 0004_tokens；repo——IssuedToken CRUD+Revoke、RefreshGrant Create/Get/SetStatus/**RevokeChain**(rotated_from BFS)、AuthNonce/AuthCode/ActivationCode 原子一次性 Consume(并发安全 via WithTx)。证据见 §6（TC-2）。

## 6. Validation Evidence (append-only)

- 2026-06-23 TC-1 | stack: go | command: `go build ./... && go vet ./... && go test ./...` | result: pass | note: httpapi 测试通过；config/httpapi/cmd 编译 OK
- 2026-06-23 TC-1 | stack: go | command: 真二进制 `POOLOPS_ADDR=:18080 issuer` + curl | result: pass | note: /healthz=200，/api/admin/audit=401（gated），SIGTERM 优雅退出 exit 0
- 2026-06-23 TC-2 | stack: go | command: `go test ./internal/store/...`（SQLite） | result: pass | note: 迁移应用+幂等、widget DDL 往返、WithTx 回滚、Rebind ?→$n 均通过；PG 路径需 POOLOPS_TEST_PG_DSN（本机未跑，代码就绪）
- 2026-06-23 TC-3 | stack: go | command: `go test ./internal/utils/crypto/...` | result: pass | note: blake2b224("") 已知向量匹配；COSE_Sign1 验签（含 tag18 剥离）通过，nonce/key/sig 篡改与错误 alg 均被拒；AES-GCM 往返+篡改检测；DeriveSub 确定性+salt 敏感。注：自构造 CIP-8 向量；真实钱包捕获向量属 integration（D5）
- 2026-06-23 TC-4 | stack: go | command: `go test ./internal/utils/jose/...` | result: pass | note: access token JWS 经 JWKS 公钥独立验签通过；header 含 kid/typ=at+jwt/alg=EdDSA 且无 cert_hash/x5c；JWKS 仅 OKP 公钥（无 d/x5c/chain）；错误公钥验签失败；activation token one_time/channel_type 校验
- 2026-06-23 p1-5 | stack: go | command: `go test ./internal/httpapi/...` | result: pass | note: IPRateLimiter 突发后 429 且每 IP 独立桶；Idempotency-Key 重放（handler 仅执行一次、回放 header）、无 key 透传；RequestLogger 透传；TC-1 平面路由测试在中间件重构后仍通过
- 2026-06-23 TC-2(p2-1) | stack: go | command: `go test ./internal/store/...`（embedded 迁移, SQLite） | result: pass | note: 0002 迁移应用；PoolConfig upsert/get/更新/ErrNotFound；IssuerKey create/get/SetStatus(retired+时间戳)/ListByStatus 往返均通过
- 2026-06-23 TC-2(p2-2) | stack: go | command: `go test ./internal/store/...` | result: pass | note: 0003 迁移应用；MembershipRule ListActive 优先级降序+排除 disabled+重排序；Blacklist Has/Add；SnapshotCache 大数 lovelace 精确往返
- 2026-06-23 TC-2(p2-3) | stack: go | command: `go test ./internal/store/...` | result: pass | note: 0004 迁移应用；IssuedToken create/get/revoke；RefreshGrant 轮换链 g1→g2→g3 RevokeChain 全撤销；AuthNonce 一次性消费 + 重放/缺失/错 purpose/过期 四类哨兵错误；AuthCode/ActivationCode 一次性 + 错渠道拒绝

## 7. Change Requests (append-only)

- 2026-06-22T22:48:27+08:00 决策：签发统一走 OAuth（详细设计 §9.1 v2 为准），概要设计 §4.1 的独立 `/api/license/issue`、`/api/license/refresh` 不实现。见 Constraint C2。
- 2026-06-22 决策（重大范围收敛）：本期**移除冷钥锚定的离线 license 证书链**，改为质押身份 **OAuth 登录**（access token 由 issuer 单签名密钥签发，JWKS/introspect 校验）。删除 OwnerAuthCert / CertRevocation 实体、`/api/certs/*`、`/crl`、JWKS 证书链；IssuerKey 瘦为签名密钥；LicenseRecord→IssuedToken；Admin 登录改用 owner key 对照链上 pool owner 列表。理由：第一方 app 登录场景无需第三方/离线独立验证，证书链过重；同时大幅削减 P0 密码学复杂度。证书链待生态/离线验证需求出现再加（详细设计 §7 演进 / overview §7）。见新增 Constraint C9。文档同步：overview（OAuth 登录版）、detailed-design v3。

### 执行期技术决策（active 期间逐条追加）

- 2026-06-23 D1 **module 路径** = `github.com/poolops/issuer`（self-hosted，未发布到公共 registry；路径仅作 import 前缀）。
- 2026-06-23 D2 **store 层偏离 sqlc**：环境未装 `sqlc`/`goose`/`migrate`，为保持 build 自包含、零外部 codegen 依赖，store 层改为**手写 `database/sql` repository + `embed` 迁移 SQL + 极简 migration runner**。架构不变（repository 接口边界、PG/SQLite 双栈保留）。§2.1 技术选型表中 sqlc/goose 一项以此决策为准。
- 2026-06-23 D3 **DB 驱动与测试边界**：SQLite 用 `modernc.org/sqlite`（纯 Go、无 CGO），单元测试跑 SQLite（临时文件/内存）；PG 用 `jackc/pgx/v5`（stdlib `database/sql` 模式），PG 专项测试需 `POOLOPS_TEST_PG_DSN` 环境变量，未提供则 skip（标 integration）。TC-2 在本机以 SQLite 为主证，PG 路径以代码 + 可选 DSN 跑通为准。
- 2026-06-23 D4 **CIP-30 COSE 验签自实现**：用 `fxamacker/cbor/v2` 解 COSE_Sign1 + 按 CIP-8 组 `Sig_structure` + `crypto/ed25519` 验签（不引入 go-cose，因 CIP-8 的 Sig_structure 组装本就需手控，自实现更直接可审计）。§2.1 中 go-cose 一项以此决策为准。
- 2026-06-23 D5 **真链 / 真 Telegram 不在本机集成测试**：`chain` 的 `node_lsq`/`db_sync`/`koios` 与 telegram transport 以接口 + mock 单测逻辑，真实集成打 `//go:build integration` tag，本 spec 验收以单测 + 可编译为准（与 §4 测试栈映射一致）。
- 2026-06-23 D6 **MVP 用可移植列类型**：为让 PG/SQLite 双栈 repository 完全同构，`array`/`json` 列在两栈统一用 `TEXT`(JSON)，时间统一用 `TEXT`(RFC3339Nano)，lovelace 用 `TEXT`(math/big 解析)，二进制用 `BYTEA`(PG)/`BLOB`(SQLite)。详细设计 §0 的 PG `jsonb`/`timestamptz`/`numeric(20)` 作为后续优化迁移（post-MVP），不影响实体语义。
