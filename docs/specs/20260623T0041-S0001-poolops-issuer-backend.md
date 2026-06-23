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
- [x] p2-4 客户端与渠道：OAuthClient / ChannelConfig / SubscriptionSession
- [x] p2-5 推送与管理审计：PushJob / DeliveryLog / AdminUser / AdminSession / AuditLog

### p3 — 链访问与规则引擎
- [x] p3-1 Staking Index Adapter（`utils/chain`）接口 + `node_lsq` 实现（MVP）+ `db_sync`/`koios` 占位
- [x] p3-2 Rule engine（`core/rules`，纯函数实现，输入参数注入 + 稳定排序）：snapshot + rule_config → eligibility / tier / entitlements

### p4 — 钱包原语与签名密钥管理
- [x] p4-1 `POST /api/auth/challenge`（AuthNonce）+ COSE 验签接入 `stake_credential_hash` 映射
- [x] p4-2 issuer 签名密钥 生成/轮换（`core/keys`，JWKS overlap，owner step-up）
- [x] p4-3 Verifier 发布：`GET /.well-known/poolops/jwks.json`（仅签名公钥，无 CRL）

### p5 — 签发与刷新（OAuth 授权服务器）
- [x] p5-1 `GET /connect`（Authorization Page 契约）+ `POST /api/connect/authorize`（验签→评估资格→AuthorizationCode）
- [x] p5-2 `POST /api/oauth/token` `grant_type=authorization_code` → access token JWS + RefreshGrant + IssuedToken 台账
- [x] p5-3 `grant_type=refresh_token`：token 轮换、盗用重放撤销、按 client_type 认证(client_secret / PKCE+DPoP)、资格重评/降级
- [x] p5-4 `POST /api/oauth/introspect`(RFC 7662) + `POST /api/oauth/revoke`(RFC 7009)（Verifier 平面）

### p6 — 渠道激活与 Telegram bot worker
- [x] p6-1 `POST /api/activation/create`（activation code + deep link，D8 短码方案）
- [x] p6-2 Telegram transport：long-poll worker（+ webhook 占位）+ 命令 `/start|/activate|/status|/unsubscribe|/help` → SubscriptionSession

### p7 — 推送与调度 worker
- [x] p7-1 推送任务 CRUD（PushJob）+ Push Scheduler（限速 sendMessage + 退避重试 + DeliveryLog）
- [x] p7-2 Reconciliation Job：每 epoch 重算资格、维护/降级/失效 SubscriptionSession

### p8 — Admin 平面
- [x] p8-1 Admin 鉴权：`/api/admin/auth/challenge` + `/verify`（owner-key 钱包签名 → httpOnly session）+ RBAC + step-up 中间件
- [x] p8-2 Admin 资源端点：members / subscriptions / rules / channels / push / oauth-clients / keys / audit（按 §9.8 角色矩阵）

### p9 — 重构（可读性：一实体一文件）
- [x] p9-1 `domain/` 按实体拆分：一实体一文件（共享哨兵错误归 `errors.go`，跨实体枚举归其主属实体文件），文件树即实体清单。纯移动、零逻辑改动。
- [x] p9-2 `store/` 按实体拆分：每个 `XxxRepo`（含其专属 cols 常量/scan 函数）一文件；共享 `rowScanner` 移入 `scan.go`。纯移动、零逻辑改动。

### p11 — 命名对齐
- [x] p11-1 module path 改名：`github.com/poolops/issuer` → `ouro-pass/server`（与真实后端名一致）；`go mod edit` + 全量 import 前缀替换，纯机械、零逻辑改动。

### p10 — 正确性修复（走查发现）
- [x] p10-1 修复 admin revoke-member：改为按 **`stake_credential_hash`(sch)** 寻址（非 sub），Blacklist 写 sch（与 `evaluate` 查询一致，修 D1）+ 级联撤销其 IssuedToken/RefreshGrant/SubscriptionSession（满足 §9.8，修 D2）；admin 名册改显 sch；文档 §9.8/overview 端点与名册口径对齐 sch（修 D3）；顺带补 D4（AuthNonce.purpose 文档对齐 §9.3）、D5（Blacklist 进 §1 实体总览）。
- [x] p10-2 安全加固：F1 服务端错误脱敏（500 不再回显原始 `err.Error()`，改 `slog` 记录 + 通用消息 + req_id）；F2 请求枚举校验（rule status / client_type / party / channel_type 落库前对照白名单，非法→400）。
- [x] p10-3 AuthNonce GC：补 `AuthNonceRepo.DeleteExpired`（删 `expires_at < now`）+ `walletauth.Service.PurgeExpiredNonces`；main 起独立 ticker（独立于 epoch，按短间隔清理，因 nonce TTL 仅分钟级）定期清理，防过期 nonce 无限堆积。nonce 后端仍为 SQL（接口化留作后续）。

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
  - 2026-06-23 TC-10 | stack: go | command: `go test ./internal/worker/push/...` | result: pass | note: tier=gold 仅 2 金会员收到、银会员跳过；vip 权益+alerts 主题组合过滤仅 1 命中；失败 2 次后第 3 次成功(退避重试)；持续失败耗尽重试→DeliveryLog failed + job→failed；成功路径 job→done、DeliveryLog sent 计数正确
- TC-11 Reconciliation：epoch 边界重算，掉级会员的 session 自动降级/失效。
  - 2026-06-23 TC-11 | stack: go | command: `go test ./internal/worker/reconciliation/...` | result: pass | note: 三 session 一保持/一 gold→silver 降级/一不合格→expired，计数 checked=3/downgraded=1/expired=1/unchanged=1，last_verified 刷新；空集 noop；Run 在 mock epoch 490>0 时触发一次并使会员 expired
- TC-12 Admin：owner-key 签名登录下发 session；RBAC 拒绝越权；敏感操作缺 step-up 被拒；操作写 AuditLog。
  - 2026-06-23 TC-12(p8-1) | stack: go | command: `go test ./internal/core/admin/... ./internal/httpapi/...` | result: pass | note: owner allowlist 命中自举 owner+发 session、Authenticate/Logout(登出后 ErrUnauthorized)；非 owner key 未种→ErrForbidden、种 operator 后登录得 operator；AtLeast RBAC；step-up 正确 key 通过/错 key→ErrForbidden；handler 全流程 httpOnly cookie：未登录 /me→401、登录后→200(role owner)、登出后→401
  - 2026-06-23 TC-12(p8-2) | stack: go | command: `go test ./internal/httpapi/... ./internal/store/...` | result: pass | note: RBAC 矩阵——viewer 读 members 200/写 rules 403/读 clients 403；operator upsert rule + create push 落库且写 audit；owner 注册 confidential client 发一次性 secret；key rotate 无 step-up→401/403、带正确 step-up 签名→200(new_kid) 且 issuer_key.rotate 入审计
- 2026-06-23 全量 | stack: go | command: `go vet ./... && go test ./...`（14 包）+ 真二进制全配置 smoke | result: pass | note: 全部包测试绿；真二进制开库迁移+JWKS(OAuth on 空集)+reconciliation worker 启动+admin /me 401+challenge 200，SIGTERM 优雅退出 exit 0
- 2026-06-23 p9-1 | stack: go | command: `go build ./... && go test ./...` | result: pass | note: domain 拆为一实体一文件（18 实体 + doc.go + errors.go）后全部包编译+测试绿；零逻辑改动
- 2026-06-23 p9-2 | stack: go | command: `go build ./... && go vet ./... && go test ./...`（14 包） | result: pass | note: store 拆为 18 个 repo_<entity>.go + scan.go 后全部编译/vet/测试绿；AdminUserRepo 抽 scanAdmin 去重行为等价；测试文件保持按组
- 2026-06-23 p10-1 | stack: go | command: `go vet ./... && go test ./internal/store/... ./internal/httpapi/...` | result: pass | note: 新增 IssuedToken/RefreshGrant.RevokeByStakeCredential、Subscription.CancelByStakeCredential（仅影响目标 sch 的 active 行、不波及他人、幂等 0 行）；handler revoke-by-sch 写 Blacklist[sch] + 级联撤销 token/grant/session 并验证状态；现有 TestAuthorize_BlacklistedRejected 已覆盖"blacklist[sch]→evaluate 拒绝"闭环。文档 §1/§3.2/§4.5/§9.8 + overview 对齐 sch（docs/v1.0 本地未入库）
- 2026-06-23 p10-2 completed：F1——新增 `httpapi.serverError(w,r,err)`(slog 记录原始 err+method/path/req_id，对客户端只回 `{"error":"server_error","error_description":"internal error"}`)，把 20 处 500 站点(admin 资源 16 + verifier/introspect 4)全部替换;不再回显任何 DB 错误。F2——`domain` 加 `RuleStatus.Valid`/`ClientType.Valid`/`ClientParty.Valid`/`ValidChannelType`，handler 在 rule upsert / client register / channel configure / push create 落库前校验，非法值→400。
- 2026-06-23 p10-2 | stack: go | command: `go vet ./... && go test ./...`（14 包） | result: pass | note: `TestServerError_GenericNoLeak`——构造含 "sql/SELECT/AdminUser/database" 的原始 err，响应只剩 "internal error"、原文零泄露;`TestAdminF2_RejectsInvalidEnums`——坏 rule status/push channel_type/channel type/client_type/party 各→400，合法值仍 200。全套 14 包绿。
- 2026-06-23 p10-3 completed：`AuthNonceRepo.DeleteExpired(before)`(删 expires_at<before，删过期/已消费均可，replay 保护不受影响) + `walletauth.Service.PurgeExpiredNonces`(注入 clock)；main 起 `runNonceGC` ticker(10min，独立 epoch、随 sigCtx 优雅退出)。AuthNonce 表不再无限堆积。
- 2026-06-23 p10-3 | stack: go | command: `go vet ./... && go test ./internal/store/... ./internal/core/walletauth/...` | result: pass | note: DeleteExpired 删 2 过期/留 1 有效，过期 nonce 后续 Consume→ErrNotFound、有效仍可消费、二次删 0(幂等)；PurgeExpiredNonces 经 clock 前移 2min 删 1。全套 14 包绿。
- 2026-06-23 p11-1 completed：module `github.com/poolops/issuer` → `ouro-pass/server`（D11）。`go mod edit -module` + `find … -exec perl -i 's{github.com/poolops/issuer}{ouro-pass/server}g'` 替换全部 import；`go mod tidy`。
- 2026-06-23 p11-1 | stack: go | command: `go build ./... && go vet ./... && go test ./...`（14 包）+ 真二进制 smoke | result: pass | note: 旧 module 路径残留 0；go.mod=`module ouro-pass/server`；全套编译/vet/测试绿；二进制开库+健康检查 200+SIGTERM exit 0。注：`POOLOPS_*` env、`/.well-known/poolops/*`、`poolops:<pool_id>` iss 属**产品/协议命名**（PoolOps Issuer），非 module 名，未改动。
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
- 2026-06-23 p2-4 completed：`domain/clients`（OAuthClient/ChannelConfig/SubscriptionSession + 枚举）；`utils/crypto/random`（RandomID/RandomToken/HashToken）；迁移 0005_clients_channels（含 SubscriptionSession 唯一约束 + bool→INTEGER 可移植）；repo——OAuthClient Upsert/Get、Channels Upsert/GetByType、Subscriptions Upsert(唯一键 upsert)/GetByChannelUser/SetStatus。证据见 §6（TC-2）。
- 2026-06-23 p2-5 completed（phase p2 收尾）：`domain/admin`（PushJob/DeliveryLog/AdminUser/AdminSession/AuditLog + 枚举）；迁移 0006_push_admin；repo——PushJob CRUD+SetStatus、DeliveryLog Append/CountByStatus、AdminUser Upsert/GetByOwnerKeyHash/TouchLogin、AdminSession Create/GetValid(过期判定)/Delete、Audit Append/Recent。全部 16 实体落库。证据见 §6（TC-2）。
- 2026-06-23 p3-1 completed：`utils/chain`——`Source` 接口 + `Snapshot` 类型（lovelace 字符串保大数）；实现 MockSource、KoiosSource(HTTP /account_info+/tip)、NodeLSQSource(cardano-cli，runner 可注入)、DBSyncSource(占位 ErrNotImplemented)；`NewSource` 工厂按 kind 选型。真实 node/db-sync/HTTP 走 integration（D5）。证据见 §6（p3-1）。
- 2026-06-23 p3-2 completed：`core/rules`——`Evaluate(Input, ruleset, epoch) → Decision` **纯函数**（C10）：内部稳定排序(priority desc, rule_id asc)、big.Int 精确比较 min stake、min_active_epochs 净 grace、首个命中胜出；`InputFromSnapshot` 纯映射。无 IO/时钟/随机。证据见 §6（TC-5）。
- 2026-06-23 p4-1 completed：`core/walletauth` Service（Challenge 发 nonce + Verify 验 COSE 签名并映射 stake_credential_hash=blake2b224(vkey)，clock 可注入）；接入 router `POST /api/auth/challenge`（public 限速）；main 改为开库+迁移+装配 Service。Verify 复用 p1-3 COSE 验签 + p2-3 AuthNonce 一次性消费。证据见 §6（p4-1）。
- 2026-06-23 p4-2 completed：`core/keys` Service（有状态，非纯，符合 C10）——`Rotate`(bootstrap 兼轮换：新 active + 旧 active→rotating overlap)、`ActiveSigner`(解密私钥签名)、`PublicJWKSKeys`(active+rotating 集合)、`RetireRotating`、`Revoke`。私钥 AES-GCM 加密落盘(C5)。证据见 §6（TC-8 部分）。
- 2026-06-23 p4-3 completed（phase p4 收尾）：`GET /.well-known/poolops/jwks.json` handler（Keys.PublicJWKSKeys → jose.BuildJWKS，Cache-Control 60s）；Deps 加 Keys，main 在 POOLOPS_FIELD_KEY 存在时装配 keys.Service，否则降级 501。证据见 §6（p4-3/TC-4 端点侧）。
- 2026-06-23 p5-1 completed：`core/oauth` Server——`ValidateClient`(client 状态/redirect allowlist/aud 校验+PKCE 要求) + `Authorize`(walletauth 验签→`evaluate` 黑名单+快照+规则→不合格 ErrNotEligible，合格发一次性 AuthorizationCode 存哈希)；`evaluate` 复用同一资格路径供 token/refresh。handler `GET /connect`(参数校验+占位 HTML) + `POST /api/connect/authorize`(302 带 code&state，不合格 302 error=not_eligible)。main 装配 chain.Source(默认 mock)+oauth.Server(需 Keys+salt)。证据见 §6（TC-6 部分）。
- 2026-06-23 p5-2 completed：`Token` 分派 + `tokenAuthCode`——消费 authcode(一次性/校 client+redirect)→`authenticateClient`(confidential client_secret SHA-256 比对 / public PKCE S256，D7)→token 时重评资格→`mint`(签 access token JWS 含 sub=DeriveSub/tier/entitlements/cnf、写 IssuedToken 台账、发 RefreshGrant 存哈希)。handler `POST /api/oauth/token`(JSON/form 双解析、OAuth 错误码映射)。证据见 §6（TC-6）。
- 2026-06-23 p5-3 completed：`tokenRefresh`——active grant 验客户端(confidential client_secret)→重评资格(不合格 ErrNotEligible，低 tier 自然降级)→旧 grant 置 rotated + `mint`(rotated_from 链接)；**rotated grant 重放→RevokeChain 撤销整链**（盗用响应）；过期 grant→expired/invalid_grant。证据见 §6（TC-7）。
- 2026-06-23 p5-4 completed（phase p5 收尾）：`Introspect`(token→JWKS 验签+ledger 交叉校验 / 裸 jti→ledger，失活/撤销/未知→inactive) + `Revoke`(JWS→jti 撤 IssuedToken / opaque→撤 RefreshGrant，RFC 7009 幂等)；`jose.JTIUnverified`(ParseInsecure 取 jti 供撤销)。handler `POST /api/oauth/{introspect,revoke}`(JSON/form 单次解析)。证据见 §6（TC-8 introspect 侧）。
- 2026-06-23 p6-1 completed：`Server.CreateActivation`(验 activation nonce→评估资格→发短码存 ActivationCode 哈希行+Telegram deep link，D8)；`Server.Eligibility` 导出供 bot 重评；config 加 TelegramBot/Token，Deps 加 TelegramBot；handler `POST /api/activation/create`。证据见 §6（TC-9 部分）。
- 2026-06-23 p6-2 completed（phase p6 收尾）：`worker/telegram`——`Processor`(命令文法 /start|/activate|/status|/unsubscribe|/help：消费 ActivationCode→重评资格→建 SubscriptionSession 绑 from.id)、`Worker.Run`(long-poll 循环 + 优雅退出)、`BotAPITransport`(Telegram getUpdates/sendMessage HTTP，真集成走 D5)。main 在 TelegramToken 存在时起 worker goroutine。Transport 接口化、命令逻辑用 mock 单测。证据见 §6（TC-9）。
- 2026-06-23 p7-1 completed：`store` 加 Subscriptions.ListActiveByChannel；`worker/push.Scheduler`——按 tier/topic/entitlement 三选一可组合过滤匹配 session，经 `Sender` 接口 rate.Limiter 限速(~30/s)+指数退避重试发送，每接收者写 DeliveryLog，job 状态 running→done/failed。Sender 接口化、用 mock 单测。证据见 §6（TC-10）。
- 2026-06-23 p7-2 completed（phase p7 收尾）：`store` 加 Subscriptions.ListActive；`worker/reconciliation.Reconciler`——`Reconcile` 遍历 active session 重评资格：不合格→Expired、tier 变→降/升级并刷新 entitlements、否则刷新 last_verified；`Run` 轮询 chain.Epoch、epoch 推进时触发。main 在 OAuth 启用时起 reconciliation goroutine。证据见 §6（TC-11）。
- 2026-06-23 p8-1 completed：`core/admin` Service——Challenge/Verify(owner-key COSE 签名→owner allowlist D9 命中自举 owner / 否则查已种 operator/viewer→建 AdminSession)、Authenticate、Logout、VerifyStepUp(step_up nonce + 绑定 owner key)、RBAC `AtLeast`(owner>operator>viewer)；`store` 加 AdminUsers.GetByID；handler challenge/verify(httpOnly+Secure cookie)/logout/me + `requireSession`/`requireRole` 中间件；config 加 OwnerKeyHashes(POOLOPS_OWNER_KEYS)。证据见 §6（TC-12 部分）。
- 2026-06-23 p8-2 completed（phase p8 收尾 / 全 25 项完成）：`mountAdminResources` 按 §9.8 角色矩阵挂全部资源端点——members(viewer+，sub 派生)/members revoke(operator+，写 Blacklist)/subscriptions(viewer+)/cancel(operator+)/rules GET+upsert(operator+)/channels configure(operator+)/push jobs list+create(operator+)/oauth-clients list+register(owner，confidential 发一次性 secret)/keys generate+rotate(owner+**step-up**)/audit(owner)；敏感操作写 AuditLog。`store` 加 PushJobs.ListByPool、OAuthClients.List；Deps 加 Store/ServerSalt/PoolID。证据见 §6（TC-12）。
- 2026-06-23 **全部 25 个执行项（p1-1…p8-2）实现完成、逐项测试通过、逐项提交**。`go vet ./...` + `go test ./...`（14 包）全绿，真二进制全配置 smoke 通过。Spec 保持 **active 不关闭**，等待用户验收。
- 2026-06-23 p9（重构）追加：应用户反馈「一眼看不出有哪些实体」，把 `domain/`、`store/` 从「按 § 分组」改为「一实体一文件」。
- 2026-06-23 p9-1 completed：`domain/` 5 个分组文件 → 18 个实体文件 + `doc.go`(包注释) + `errors.go`(共享哨兵)。跨实体枚举 `ClientType` 归 `oauthclient.go`(RefreshGrant 同包引用)。纯移动，`go build`/`go test`(14 包)全绿。证据见 §6（p9-1）。
- 2026-06-23 p9-2 completed（phase p9 收尾）：`store/` 5 个分组 repo 文件 → 18 个 `repo_<entity>.go`(各含专属 cols/scan)；共享 `rowScanner` 移入 `scan.go`。`issuerKeyCols`/`subscriptionCols`/`boolToInt` 跟随各自实体；`AdminUserRepo` 的 GetByOwnerKeyHash/GetByID 顺手抽 `scanAdmin` 去重(行为等价)。文件树即实体清单。`go build`/`go vet`/`go test`(14 包)全绿。**测试文件 `repo_*_test.go` 仍按组保留**(它们是测试分组、含跨用例 helper，非实体索引)。证据见 §6（p9-2）。

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
- 2026-06-23 TC-2(p2-4) | stack: go | command: `go test ./internal/store/... ./internal/utils/crypto/...` | result: pass | note: 0005 迁移应用；OAuthClient confidential/public(PKCE) 往返；ChannelConfig GetByType；SubscriptionSession 唯一键 upsert(tier 改写不重复)/SetStatus；RandomID/Token/Hash 可用
- 2026-06-23 TC-2(p2-5) | stack: go | command: `go build ./... && go vet ./... && go test ./...` | result: pass | note: 0006 迁移应用；PushJob CRUD+SetStatus；DeliveryLog CountByStatus(2 sent/1 failed)；AdminUser/Session(valid→expired→deleted)/Audit Recent 全通过；整库 6 迁移、16 实体编译+测试绿
- 2026-06-23 p3-1 | stack: go | command: `go test ./internal/utils/chain/...` | result: pass | note: Mock(known/unknown/epoch)；Koios 经 httptest 解析 /tip+/account_info（大数 lovelace 精确，未注册账户清空 pool）；node_lsq parseStakeAddressInfo/parseTip + 注入 runner 全流程；NewSource 工厂 4 类 + 未知报错 + db_sync ErrNotImplemented
- 2026-06-23 TC-5 | stack: go | command: `go test ./internal/core/...` | result: pass | note: 资格评估覆盖 whale→gold/小额→silver/全不达标/委托他池/未委托/未知 stake；**纯函数验证**——50 次同输入 DeepEqual 一致、规则乱序仍 gold(prio 10) 胜出；min_active_epochs 净 grace（2 失败/3 通过/未知跳过）；disabled 规则忽略
- 2026-06-23 p4-1 | stack: go | command: `go test ./internal/core/walletauth/... ./internal/httpapi/...` + 真二进制 curl | result: pass | note: Challenge→COSE 签名→Verify 返回正确 credential hash；重放 ErrConsumed、错绑定 key/篡改签名/错 purpose 均拒；handler 200+nonce、bogus purpose/坏 vkey/坏 JSON→400；真二进制开库迁移后 /api/auth/challenge 返回 nonce
- 2026-06-23 p4-2(TC-8) | stack: go | command: `go test ./internal/core/keys/...` | result: pass | note: bootstrap→1 active；二次 Rotate→旧 rotating+新 active 两 kid overlap 发布；ActiveSigner 解密私钥签名经公钥验签通过(加解密往返)；RetireRotating 退役旧 kid 后 JWKS 剩 1
- 2026-06-23 p4-3 | stack: go | command: `go test ./internal/httpapi/...` | result: pass | note: JWKS 端点空集 200；Rotate 后发布 1 个 OKP/Ed25519/status=active key，无 d/x5c/chain 泄漏；无 Keys 服务时 501
- 2026-06-23 TC-6(p5-1) | stack: go | command: `go test ./internal/core/oauth/... ./internal/httpapi/...` | result: pass | note: Authorize 合格发 code(存哈希、可一次性消费)；委托他池/黑名单→ErrNotEligible；未知 client→ErrInvalidClient、坏 redirect→ErrInvalidRequest；handler /connect 校验 response_type/client；/api/connect/authorize 合格 302 带 code&state、不合格 302 error=not_eligible
- 2026-06-23 TC-6(p5-2) | stack: go | command: `go test ./internal/core/oauth/...` | result: pass | note: confidential authcode 换 token，access token 经 JWKS 验签通过且 sub=DeriveSub/tier=gold，refresh grant 落库；错 client_secret→invalid_client；public PKCE 正确 verifier 成功(带 cnf.jkt)、错 verifier→invalid_grant；code 复用→invalid_grant；password grant→unsupported_grant_type
- 2026-06-23 TC-7 | stack: go | command: `go test ./internal/core/oauth/...` | result: pass | note: refresh 轮换发新 access+新 refresh、旧 grant→rotated/新 grant rotated_from；**重放旧 rotated grant→invalid_grant 且 RevokeChain 把后代 refresh2 也撤销、refresh2 随后不可用**；委托迁出→重评 not_eligible；错 client_secret→invalid_client、未知 grant→invalid_grant
- 2026-06-23 TC-8(introspect/revoke,p5-4) | stack: go | command: `go test ./internal/core/oauth/...` | result: pass | note: Introspect active token→active+tier+sub+membership_status；revoke access 后→inactive；未知 jti/垃圾 token→inactive 无错；Revoke refresh→grant revoked 且不可再 mint；未知 token revoke 仍成功(RFC 7009)
- 2026-06-23 TC-9(p6-1) | stack: go | command: `go test ./internal/core/oauth/...` | result: pass | note: CreateActivation 合格发短码(≤64 字符可作 deep link start)+deep_link https://t.me/PaoBot?start=…，存 ActivationCode 哈希行可一次性消费；不合格→ErrNotEligible；错 purpose nonce→ErrAccessDenied
- 2026-06-23 TC-9(p6-2) | stack: go | command: `go test ./internal/worker/telegram/...` | result: pass | note: /start <code> 消费激活码+重评→建 active 订阅(tier=gold)、回放码→already used；invalid/缺参/不合格各自回复；/status 显 tier、/unsubscribe→cancelled；parseCommand 去 @botname 后缀；Worker.Run 经 mock transport 派发并回复 Subscribed

## 7. Change Requests (append-only)

- 2026-06-22T22:48:27+08:00 决策：签发统一走 OAuth（详细设计 §9.1 v2 为准），概要设计 §4.1 的独立 `/api/license/issue`、`/api/license/refresh` 不实现。见 Constraint C2。
- 2026-06-22 决策（重大范围收敛）：本期**移除冷钥锚定的离线 license 证书链**，改为质押身份 **OAuth 登录**（access token 由 issuer 单签名密钥签发，JWKS/introspect 校验）。删除 OwnerAuthCert / CertRevocation 实体、`/api/certs/*`、`/crl`、JWKS 证书链；IssuerKey 瘦为签名密钥；LicenseRecord→IssuedToken；Admin 登录改用 owner key 对照链上 pool owner 列表。理由：第一方 app 登录场景无需第三方/离线独立验证，证书链过重；同时大幅削减 P0 密码学复杂度。证书链待生态/离线验证需求出现再加（详细设计 §7 演进 / overview §7）。见新增 Constraint C9。文档同步：overview（OAuth 登录版）、detailed-design v3。

### 执行期技术决策（active 期间逐条追加）

- 2026-06-23 D1 **module 路径** = `github.com/poolops/issuer`（self-hosted，未发布到公共 registry；路径仅作 import 前缀）。【已被 D11 取代】
- 2026-06-23 D11 **module 路径改名**（取代 D1）：→ `ouro-pass/server`，与真实后端名一致。不带 host 前缀——self-hosted、永不 `go get`、私有模块完全合法（与 D1"不发布到公共 registry"一致）。见 p11-1。
- 2026-06-23 D2 **store 层偏离 sqlc**：环境未装 `sqlc`/`goose`/`migrate`，为保持 build 自包含、零外部 codegen 依赖，store 层改为**手写 `database/sql` repository + `embed` 迁移 SQL + 极简 migration runner**。架构不变（repository 接口边界、PG/SQLite 双栈保留）。§2.1 技术选型表中 sqlc/goose 一项以此决策为准。
- 2026-06-23 D3 **DB 驱动与测试边界**：SQLite 用 `modernc.org/sqlite`（纯 Go、无 CGO），单元测试跑 SQLite（临时文件/内存）；PG 用 `jackc/pgx/v5`（stdlib `database/sql` 模式），PG 专项测试需 `POOLOPS_TEST_PG_DSN` 环境变量，未提供则 skip（标 integration）。TC-2 在本机以 SQLite 为主证，PG 路径以代码 + 可选 DSN 跑通为准。
- 2026-06-23 D4 **CIP-30 COSE 验签自实现**：用 `fxamacker/cbor/v2` 解 COSE_Sign1 + 按 CIP-8 组 `Sig_structure` + `crypto/ed25519` 验签（不引入 go-cose，因 CIP-8 的 Sig_structure 组装本就需手控，自实现更直接可审计）。§2.1 中 go-cose 一项以此决策为准。
- 2026-06-23 D5 **真链 / 真 Telegram 不在本机集成测试**：`chain` 的 `node_lsq`/`db_sync`/`koios` 与 telegram transport 以接口 + mock 单测逻辑，真实集成打 `//go:build integration` tag，本 spec 验收以单测 + 可编译为准（与 §4 测试栈映射一致）。
- 2026-06-23 D6 **MVP 用可移植列类型**：为让 PG/SQLite 双栈 repository 完全同构，`array`/`json` 列在两栈统一用 `TEXT`(JSON)，时间统一用 `TEXT`(RFC3339Nano)，lovelace 用 `TEXT`(math/big 解析)，二进制用 `BYTEA`(PG)/`BLOB`(SQLite)。详细设计 §0 的 PG `jsonb`/`timestamptz`/`numeric(20)` 作为后续优化迁移（post-MVP），不影响实体语义。
- 2026-06-23 D7 **public client PoP：PKCE 强制、DPoP 简化**：token 端点对 public client 强制 PKCE(S256) 校验；sender-constrained PoP 的 `cnf.jkt` 用请求携带的 `device_pubkey` 的 SHA-256 thumbprint 近似设置；完整 RFC 9449 DPoP proof 头校验（jws over htu/htm/nonce）留作后续/integration。confidential client 用 client_secret(SHA-256 比对) 充当 holder-of-key，完整实现。
- 2026-06-23 D8 **渠道激活用一次性短码（非签名 JWT 变体）**：Telegram deep-link `start` 参数限 64 字符，签名 JWT 放不下；故 MVP 用短随机码作 `start` 参数 + `ActivationCode` 行（存码哈希、sch、channel、一次性/短效），与详细设计 §4.4「退化为已消费记录」一致。`ActivationCode` 不带 tier/entitlements 列，故兑换时由 bot 重评一次资格（§9.7）——把「兑换零查链」放宽为「绑定时一次查链」（低频操作可接受）；带已解析权益的自包含 JWT 变体留作 post-MVP。`jose.SignActivationToken` 已就绪备用。
- 2026-06-23 D10 **Admin 成员平面按 sch 寻址（非 sub）**：走查发现 revoke 把 `sub` 写进 Blacklist 而 `evaluate` 按 `sch` 查，二者经 HMAC 不可逆永不相等→拉黑失效（D1）。决策：admin 名册/撤销统一用 `stake_credential_hash`（sch），不用伪匿名 `sub`。理由：sch = `blake2b224(stake_vkey)` 本就是**链上公开派生值**，SPO admin 可自行查链，对受信 admin 暴露 sch 无隐私损失；伪匿名 `sub` 仅服务**外部 token 消费方**（token `sub` claim 不变）。同时所有 durable 行都按 sch 键，故 revoke 可级联撤销 token/grant/session（D2）。文档 §9.8/overview 的 `/members/:sub` 与"按 sub 名册"改为 sch。
- 2026-06-23 D9 **链上 pool owner 列表用配置 allowlist 近似**：详细设计 §8.1/C9 要求 admin `owner` 角色对照链上 pool 注册 owner 列表校验；`chain.Source` 接口尚无 PoolOwners 查询，故 MVP 用配置项 `POOLOPS_OWNER_KEYS`(逗号分隔 owner stake key hash) 代表链上注册 owners——命中即 owner 角色(自动 upsert AdminUser)；`operator`/`viewer` 由 owner 在后台添加(AdminUser 行)。实时 pool-registration 查询(给 Source 加 PoolOwners 方法)留作 integration/post-MVP。
