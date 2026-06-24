# Ouro Pass Web 前端 — 授权页 + Admin 后台

Spec-ID: S0002
Status: draft
Created Time: 2026-06-24T01:40:00+08:00
Start Time:
Completion Time:
Previous Spec-ID: (none)
Closure Reason:

## 1. Requirement Details

### Background
实现 `web/` 目录下的 Ouro Pass Issuer 前端，消费 S0001 后端（`server/`，已交付）暴露的四平面 API。后端 Non-goals 已将「Authorization Page / Admin UI 的页面实现」划归 web/、另立 spec —— 即本 spec。`web/` 当前为空脚手架（仅 `.gitignore`）。

前端有两个诉求相反的面向：
- **Authorization Page（授权页）**：公开、无业务状态、**安全关键**、要极轻；OAuth 授权码流的前端（连钱包→签 nonce→拿 code→回跳）。
- **Admin 后台**：有状态、owner-key 钱包登录、RBAC、组件丰富（名册/规则/推送/密钥/审计）。
- 附带 **渠道绑定页**（Telegram 激活，公开、轻）。

### Scope（本 spec 覆盖）
- **Authorization Page**：解析 OAuth 参数 → CIP-30 连钱包 → `/api/auth/challenge` → `signData` → `/api/connect/authorize` → 302 带 code 回跳；资格失败 UI。
- **渠道绑定页**：连钱包(activation 目的) → `/api/activation/create` → deep link / 二维码。
- **Admin 后台**：owner-key 登录(challenge/verify, httpOnly cookie) + logout + `/me` + RBAC 门禁 + step-up 重签；Dashboard；会员名册 + 撤销(级联)；订阅管理 + 取消；规则编辑器；渠道配置 + telegram 连通测试；推送任务 + 投递日志；OAuth 客户端注册(一次性 secret)；签名密钥生成/轮换(step-up) + JWKS 状态；审计日志；初始化向导。
- **共享层**：`WalletAdapter`（CIP-30 薄封装）、API client（错误信封/429 处理）、shadcn/ui 组件、路由/状态。
- 构建：纯静态 SPA，可 embed 进 Go 二进制或任意静态托管。

### Constraints
- C1 **框架/构建已定**：React 18 + Vite + TypeScript(strict)，**纯静态 SPA（无 SSR/Node 运行时）**；产物为静态文件。配套 React Router、TanStack Query（服务端状态）、React Hook Form + Zod（表单/校验）。
- C2 **组件库已定**：shadcn/ui（Radix + Tailwind）+ TanStack Table（数据网格）。授权页保持精简、Admin 用全量。
- C3 **CIP-30 钱包集成**：用 **thin `window.cardano` 封装**（探测/enable/`getRewardAddresses`/`signData`），藏在自有 **`WalletAdapter` 接口**后（库可替换、锁定面最小）。**不引入 Lucid/MeshJS 等含 tx 构建/CSL-WASM 的重库**（本产品不构建任何链上交易，只签 nonce）。需访问 `signData` 的**原始 `DataSignature`**（`signature` + `key` 两字段）。
- C4 **stake_vkey 来源与约束**（CIP-30 固有，见 §2.5 / D-决策）：后端 `stake_vkey` = 裸 32 字节 Ed25519 公钥 hex（`walletauth.decodeVkey`）。CIP-30 **唯一**获取裸 stake 公钥的途径是 `signData` 返回的 `key`(COSE_Key，公钥在字段 `-2`)；`getRewardAddresses()` 只给地址(哈希)、无公钥。必须**对 reward/stake 地址签名**使恢复出的 key = stake 凭证；network(mainnet/preprod/preview) 必须与 issuer 一致。
- C5 **授权页隔离**：与 Admin **分应用/分构建**；授权页**绝不持有 token**，只产出 code 并回跳；依赖最小、便审计。
- C6 **Admin 会话**：owner-key 登录后用后端下发的 **httpOnly cookie**；RBAC 按角色(viewer<operator<owner) gate UI；敏感操作(撤销会员/注册 client/轮换密钥)前走 **step-up 重签**。
- C7 **签名统一走 CIP-30 `signData`**(COSE_Sign1)，四种 purpose：issue / activation / admin_login / step_up。

### Non-goals（本 spec 不做）
- 凭证**消费端**（第一方/第三方 App、verifier SDK）—— 用 token 的应用，不是 issuer UI。
- 链上交易构建/提交。
- S0001 后端本身（已交付）。
- 自建可观测性平台、设计系统 token 体系（用 shadcn 默认）。

## 2. Outline Design

### 2.1 技术选型（落定）
| 关注点 | 选型 | 理由 |
|---|---|---|
| 框架 | **React 18 + Vite + TS(strict)** | Cardano 钱包库/admin 组件库生态最厚；纯 SPA 自托管只托管静态文件、可 embed Go 二进制 |
| 路由 | React Router | SPA 标准 |
| 服务端状态 | **TanStack Query** | 天然契合 admin REST API（缓存/失效/重试） |
| 表单/校验 | React Hook Form + **Zod** | 规则编辑器等复杂表单 + 运行时校验 |
| 组件库 | **shadcn/ui**(Radix+Tailwind) + **TanStack Table** | copy-in、无运行时锁定、bundle 小、可访问、可控 |
| 钱包 | **thin `window.cardano` 封装** + `WalletAdapter` 接口 | 依赖面极窄、CIP-30 是稳定标准、库可换；避开重 WASM 拖肥授权页 |
| 构建/部署 | Vite 静态产物 | 可 embed 进 Go 二进制 / 任意静态托管 |

> 评估并排除：Weld（@ada-anvil/weld，~550 下载/月、pre-1.0、采用低）；Cardano Foundation connect-with-wallet（更可信但高层库可能不暴露 `DataSignature.key`）；Lucid/Mesh（含 tx/CSL-WASM，对「只签 nonce」过度且拖肥授权页）。结论：thin-wrapper 最契合 C3/C4，库选型藏在 `WalletAdapter` 后随时可换。

### 2.2 应用结构（建议）
```text
web/
  packages/
    wallet/        # WalletAdapter：CIP-30 薄封装 + COSE_Key→stake_vkey 抽取 + network guard
    api/           # 后端 API client：fetch 封装 + 错误信封 {error,error_description} + 429
    ui/            # shadcn 组件 + 共享样式/主题
  apps/
    authorize/     # 授权页(+渠道绑定页)：极轻、安全关键、无 token
    admin/         # Admin SPA：登录/RBAC/step-up + 各业务页
```
（monorepo 用 pnpm workspaces / Vite 多入口；authorize 与 admin **独立构建**，各自最小依赖图。）

### 2.3 后端端点 → 前端页面映射（详见 S0001 §9.1）
| 页面 | 端点 | 角色 |
|---|---|---|
| 授权页 | `GET /connect`(参数校验)、`POST /api/auth/challenge`、`POST /api/connect/authorize`→302 | 公开 |
| 绑定页 | `POST /api/auth/challenge`(activation)、`POST /api/activation/create` | 公开 |
| Admin 登录 | `POST /api/admin/auth/challenge`/`auth/verify`/`auth/logout`、`GET /me` | — |
| 名册/撤销 | `GET /api/admin/members[/:sch]`、`POST /members/:sch/revoke`(+step-up) | viewer/operator |
| 订阅 | `GET /api/admin/subscriptions`、`POST /subscriptions/:id/cancel` | viewer/operator |
| 规则 | `GET/POST /api/admin/rules` | operator |
| 渠道 | `POST /api/admin/channels/:type/configure` | operator |
| 推送 | `GET/POST /api/admin/push/jobs` | operator |
| 客户端 | `GET/POST /api/admin/oauth-clients`(+step-up，一次性 secret) | owner |
| 密钥 | `POST /api/admin/keys/issuer/generate`/`rotate`(+step-up) | owner |
| 审计 | `GET /api/admin/audit` | owner |

### 2.4 WalletAdapter 接口（核心抽象）
```ts
interface WalletAdapter {
  list(): WalletInfo[];                          // 探测 window.cardano.*
  enable(id): Promise<EnabledWallet>;            // CIP-30 enable
  rewardAddressHex(w): Promise<string>;          // getRewardAddresses()[0]
  // signData(rewardAddr, payloadHex) → {signature(COSE_Sign1), key(COSE_Key)}
  // 抽出 stake_vkey = COSE_Key 字段 -2 (raw 32B ed25519 pubkey, hex)
  signNonce(w, nonceHex): Promise<{ stakeVkeyHex: string; signatureHex: string }>;
  network(w): Promise<'mainnet'|'testnet'>;      // network guard 对齐 issuer
}
```
实现里需 CBOR 解 `DataSignature.key` 取字段 -2；对 payload 编码（CIP-30 payload 为 hex bytes，后端按 `[]byte(nonce)` 比对，需确认 nonce 编码一致）。

### 2.5 关键约束：CIP-30 与 `/challenge` 的流程冲突（**前置项**）
- 后端现流程：`challenge(stake_vkey)`(绑 nonce 到 `blake2b224(vkey)`) → 签 nonce → `authorize(stake_vkey,sig)`。
- 但 CIP-30 的裸 `stake_vkey` **只能在 `signData` 之后**拿到，`signData` 又需 `challenge` 给的 nonce → **先有鸡还是先有蛋**，标准钱包跑不通。
- **解决（推荐方案 A，后端小改，作为本 spec 前置依赖）**：`/api/auth/challenge` 改为接受 **stake 地址 / key hash**（签名前即可由 `getRewardAddresses()` 得到），把 nonce 绑到该 hash；`/authorize` 不变（仍收 `stake_vkey`+sig，校验 `blake2b224(vkey)==hash` + COSE 签名）。改动 ~10 行（只动 `Challenge`，`Verify` 几乎不变）。**全部 CIP-30 钱包、一次签名**。
- 备选 B（CIP-95 签名前取裸 stake 公钥，收窄钱包兼容）、C（双签，UX 差）—— 不采用。
- 该后端改动可在本 spec 内做（跨 server/）或回 server 开小 fix spec；**p3 标为 blocking**。

### 2.6 Risk and rollback strategy
- R1 钱包互操作（多钱包 signData/COSE 差异、payload 编码）→ `WalletAdapter` 后做 mock `window.cardano` 单测 + 真钱包手测矩阵(Nami/Eternl/Lace…)。
- R2 §2.5 流程冲突 → 前置后端改动 + thin-wrapper 自取 `DataSignature.key` 兜底。
- R3 授权页安全（不得泄 token/钓鱼）→ 与 Admin 分应用、最小依赖、CSP、只产 code。
- R4 库 pre-1.0/停更 → 钱包库藏 `WalletAdapter` 后、pin 版本、可换 thin-wrapper。
- Rollback：未发布前按 working tree 修；已提交按 item `git revert`；遵循 immutable-spec forward-only。

## References
- docs/specs/completed/20260623T0041-S0001-poolops-issuer-backend.md — 后端 spec（端点/契约/RBAC 以此为准）
- docs/v1.0/ouropass-issuer-{overview,detailed-design}.md — 设计文档（三端、授权流程）
- CIP-30（dApp-Wallet Web Bridge）/ CIP-8（message signing）/ CIP-19（地址）/ CIP-95（可选）
- server/internal/core/walletauth/walletauth.go、internal/httpapi/handlers_{oauth,wallet,activation,admin*}.go — 前端要对接的契约

## 3. Execution Plan
- [ ] p1-1 脚手架：pnpm workspaces + Vite + React + TS(strict) + Tailwind + shadcn init；`packages/{wallet,api,ui}` + `apps/{authorize,admin}` 双入口独立构建；lint/format/CI 占位。
- [ ] p2-1 `WalletAdapter`（`packages/wallet`）：CIP-30 探测/enable/getRewardAddresses/signData；COSE_Key→`stake_vkey`(字段 -2) 抽取；network guard；mock `window.cardano` 单测 + 已知 `DataSignature` 向量验 vkey 抽取（TC-1）。
- [ ] p2-2 `api` client：fetch 封装 + 错误信封 `{error,error_description}` + 429/限流 + 类型（TC-7 部分）。
- [ ] p3-1 **[blocking 前置]** 后端 `/api/auth/challenge` 改绑 stake 地址/hash（§2.5 方案 A）+ 配套测试（跨 server/，或回 server 开 fix spec）。
- [ ] p4-1 授权页：OAuth 参数解析/校验 → 连钱包 → challenge → signNonce → authorize → 302 回跳；not_eligible/access_denied UI（TC-2/TC-3）。
- [ ] p5-1 渠道绑定页：连钱包(activation) → activation/create → deep link/二维码 + 资格 UI（TC-4）。
- [ ] p6-1 Admin 外壳：owner-key 登录(challenge/verify cookie)/logout/`/me` + RBAC 门禁 + step-up 重签流程 + 布局/导航（TC-5）。
- [ ] p7-1 Admin 业务页（一批）：Dashboard、名册+撤销(step-up,级联)、订阅+取消、规则编辑器(rule_config 表单)、渠道配置+telegram 测试、推送+投递日志、客户端注册(一次性 secret)、密钥生成/轮换(step-up)+JWKS 状态、审计、初始化向导（TC-6）。
- [ ] p8-1 构建/部署：静态产物、可 embed 进 Go 二进制(embed FS)、env 配置(issuer base URL/network)、CI 增 web job（TC-7/TC-8）。

## 4. Test and Acceptance Criteria
- TC-1 `WalletAdapter`：给定一条真实/向量 `DataSignature`，抽出的 `stake_vkey` = COSE_Key 字段 -2 的 32 字节 hex；network guard 在不匹配时报错；mock `window.cardano` 覆盖探测/enable/sign。
- TC-2 授权页 happy path（mock 后端）：参数 → 连钱包 → 拿 code → 302 回跳带 `code`+`state`；PKCE/device_pubkey 透传。
- TC-3 授权页否定：not_eligible/access_denied 正确回跳 `error=`；client 无效/redirect 不匹配直接报错。
- TC-4 绑定页：activation/create 成功 → 展示 `https://t.me/<bot>?start=<code>` deep link/二维码；不合格态提示。
- TC-5 Admin 登录/RBAC/step-up：owner 登录得 cookie；viewer 看不到 operator/owner 操作；敏感操作缺 step-up 被前端拦/后端 401。
- TC-6 Admin 业务：各页 CRUD 与后端契约一致（名册按 sch、规则 rule_config、推送过滤、客户端一次性 secret、密钥轮换 step-up、审计只读）。
- TC-7 构建：`pnpm build` 产出 authorize/admin 两套静态资源；可被 Go embed/静态托管；类型检查 + lint 绿。
- TC-8 网络/配置：issuer base URL + network 经 env 注入；network 与钱包不一致时阻断签名。
- Pass/fail：每个 item 仅在其映射 TC 全部 pass 且证据 append 后方可标 `[x]`。

## 5. Execution Log (append-only)
- 2026-06-24 S0002 草案创建（draft）：技术选型落定（React+Vite SPA / shadcn / thin CIP-30 + WalletAdapter）；记录 CIP-30 与 `/challenge` 流程冲突及后端前置改动（§2.5）。尚未开始执行。

## 6. Validation Evidence (append-only)

## 7. Change Requests (append-only)
- 2026-06-24 决策汇总：框架 React+Vite 纯 SPA（用户确认）；组件库 shadcn/ui（用户确认）；钱包从 Weld 改为 thin `window.cardano` 封装（用户质疑 Weld 成熟度 + §2.5 需原始 `DataSignature.key`，故 thin-wrapper 最契合，库藏 `WalletAdapter` 后可换）。
