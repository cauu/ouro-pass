# Ouro Pass Admin 前端 — SPO 运营后台 SPA

Spec-ID: S0002
Status: active
Created Time: 2026-06-24T01:40:00+08:00
Start Time: 2026-06-24T23:55:00+08:00
Completion Time:
Previous Spec-ID: (none)
Closure Reason:

> 修订（2026-06-24）：原 draft 误把**授权页**当成 web/ SPA。设计文档（§9.4）确认 `GET /connect` 是 **issuer 直接返回的 HTML**；授权页/绑定页 + walletauth 契约改造已移到 **S0003（后端）**。本 spec **收敛为纯 Admin 后台 SPA**，并依赖 S0003 的新钱包签名契约。

## 1. Requirement Details

### Background
实现 `web/` 下的 **Ouro Pass Admin 后台**（SPO 运营），消费 S0001 后端的 `/api/admin/*`。这是有状态、owner-key 钱包登录、RBAC、组件丰富的单页应用。`web/` 当前为空脚手架。

授权页/渠道绑定页**不在本 spec**——它们是 issuer 直接服务的 HTML，归 **S0003**。本 spec 与 S0003 共享同一套钱包签名契约（admin 登录/step-up 也走 CIP-30 `signData`）。

### Scope（本 spec 覆盖，均在 `web/`）
- **登录/会话**：owner-key 钱包登录（`auth/challenge`→`signData`→`auth/verify`，httpOnly cookie）、`auth/logout`、`/me`、RBAC 门禁（viewer<operator<owner）、敏感操作 **step-up 重签**。
- **Dashboard**：活跃会员/订阅/最近推送/密钥状态/当前 epoch 概览。
- **会员名册**：`GET /members[/:sch]`（按 stake_credential_hash）、撤销 `POST /members/:sch/revoke`(+step-up，级联)。
- **订阅管理**：`GET /subscriptions`、取消 `POST /subscriptions/:id/cancel`。
- **规则编辑器**：`GET/POST /rules`（tier、min_active_stake、min_active_epochs、grace、entitlements、priority、status；`rule_config` JSON 表单）。
- **渠道配置**：`POST /channels/:type/configure`、telegram 连通测试。
- **推送管理**：`GET/POST /push/jobs`（按 tier/topic/entitlement + 定时）、投递日志/状态。
- **OAuth 客户端**：`GET/POST /oauth-clients`（owner，+step-up，**一次性展示 client_secret**）。
- **签名密钥**：`POST /keys/issuer/{generate,rotate}`（owner+step-up）、JWKS/密钥状态视图。
- **审计日志**：`GET /audit`（owner）。
- **初始化向导**：首次——配 pool id / staking 数据源 / 生成首把签名密钥 / owner 自举登录。
- **共享层**：`WalletAdapter`（连钱包 + `signData` + **转发** COSE_Key/reward 地址给后端，浏览器零 CBOR）、API client（错误信封/429）、shadcn 组件。
- 构建：纯静态 SPA，可 embed 进 Go 二进制或任意静态托管。

### Constraints
- C1 **框架/构建已定**：React 18 + Vite + TS(strict)，**纯静态 SPA（无 SSR/Node 运行时）**。配 React Router、TanStack Query、React Hook Form + Zod。
- C2 **组件库已定**：shadcn/ui（Radix + Tailwind）+ TanStack Table。
- C3 **钱包集成走 S0003 契约**：admin 登录/step-up 用 CIP-30 `signData`；`WalletAdapter` **只连钱包 + 取 reward 地址 + signData + 转发两块 hex（`cose_key`+`signature`）给后端**，**不在浏览器解 CBOR/抽 vkey**（由后端做，S0003）。thin `window.cardano` 封装、不引入 Lucid/Mesh 重库；库藏 `WalletAdapter` 后可换。
- C4 **会话**：owner-key 登录后用后端 httpOnly cookie；RBAC 按角色 gate UI；敏感操作前 step-up 重签。
- C5 **网络一致**：network(mainnet/preprod/preview) 与 issuer 对齐，不一致阻断签名。
- C6 **依赖 S0003**：admin 登录/step-up 的 challenge/verify 契约以 S0003（reward 地址 + COSE_Key）为准 → **S0003 为前置（blocking）**。

### Non-goals
- 授权页 / 渠道绑定页（→ S0003，issuer 服务的 HTML）。
- walletauth 后端契约改造（→ S0003）。
- 凭证消费端（用 token 的 App / verifier SDK）、链上交易。
- S0001 后端、自建可观测性平台。

## 2. Outline Design

### 2.1 技术选型（落定）
| 关注点 | 选型 | 理由 |
|---|---|---|
| 框架 | **React 18 + Vite + TS(strict)** | Cardano 生态 + admin 组件库最厚；纯 SPA 自托管只托管静态文件、可 embed Go |
| 路由 | React Router | SPA 标准 |
| 服务端状态 | **TanStack Query** | 契合 admin REST（缓存/失效/重试） |
| 表单/校验 | React Hook Form + **Zod** | 规则编辑器等 + 运行时校验 |
| 组件库 | **shadcn/ui**(Radix+Tailwind) + **TanStack Table** | 无运行时锁定、bundle 小、可控、可访问 |
| 钱包 | **thin `window.cardano` 封装** + `WalletAdapter`（仅连接+sign+转发，零 CBOR） | 依赖面窄、CIP-30 稳定标准、库可换；CBOR/vkey 抽取归后端(S0003) |
| 构建 | Vite 静态产物 | 可 embed / 静态托管 |

> 钱包库评估见 §7：排除 Weld（采用低/pre-1.0）、Lucid/Mesh（含 tx/CSL-WASM，过度）；选 thin-wrapper（契合"零 CBOR 转发"），藏 `WalletAdapter` 后可换。

### 2.2 应用结构（建议）
```text
web/                       # 只含 Admin SPA（授权/绑定页在 server/，见 S0003）
  src/
    wallet/                # WalletAdapter：CIP-30 探测/enable/getRewardAddresses/signData/转发；network guard
    api/                   # admin API client：fetch + 错误信封 {error,error_description} + 429
    ui/                    # shadcn 组件 + 主题
    features/{auth,members,subscriptions,rules,channels,push,clients,keys,audit,dashboard,setup}/
    app/                   # 路由 + 布局 + RBAC 守卫
```

### 2.3 后端端点 → 页面映射（S0001 §9.1）
| 页面 | 端点 | 角色 |
|---|---|---|
| 登录 | `auth/challenge`(reward 地址)→`signData`→`auth/verify`(cose_key)、`logout`、`/me` | — |
| 名册/撤销 | `GET /members[/:sch]`、`POST /members/:sch/revoke`(+step-up) | viewer/operator |
| 订阅 | `GET /subscriptions`、`POST /subscriptions/:id/cancel` | viewer/operator |
| 规则 | `GET/POST /rules` | operator |
| 渠道 | `POST /channels/:type/configure` | operator |
| 推送 | `GET/POST /push/jobs` | operator |
| 客户端 | `GET/POST /oauth-clients`(+step-up，一次性 secret) | owner |
| 密钥 | `POST /keys/issuer/{generate,rotate}`(+step-up) | owner |
| 审计 | `GET /audit` | owner |

### 2.4 WalletAdapter（简化后）
```ts
interface WalletAdapter {
  list(): WalletInfo[];                       // window.cardano.*
  enable(id): Promise<EnabledWallet>;
  rewardAddress(w): Promise<string>;          // getRewardAddresses()[0]（原样，后端解析）
  // signData(rewardAddr, hex(utf8(nonce))) → {signature, key}
  signNonce(w, nonce): Promise<{ coseKeyHex: string; signatureHex: string }>; // 仅转发，无 CBOR
  network(w): Promise<'mainnet'|'testnet'>;   // guard 对齐 issuer
}
```

### 2.5 Risk and rollback
- R1 钱包互操作（多钱包 enable/signData/network）→ mock `window.cardano` 单测 + 真钱包手测矩阵（与 S0003 共用）。
- R2 依赖 S0003 契约未就绪 → S0003 为 blocking 前置；可先对 mock 后端开发。
- R3 库 pre-1.0/停更 → 藏 `WalletAdapter` 后、pin 版本、可换。
- Rollback：未发布按 working tree；已提交 `git revert`；forward-only。

## References
- docs/specs/completed/20260623T0041-S0001-poolops-issuer-backend.md — 后端契约/RBAC
- docs/specs/draft/S0003-walletauth-cose-and-authz-pages.md — **前置**：钱包签名契约 + 授权/绑定页
- docs/v1.0/ouropass-issuer-{overview,detailed-design}.md — 设计（三端、admin 流程）
- CIP-30 / CIP-19

## 3. Execution Plan
- [x] p0-1 **[blocking 前置]** 完成 S0003（walletauth 契约 + 授权/绑定页）——admin 登录/step-up 依赖其新契约。（S0003 已 delivered/closed）
- [x] p1-1 脚手架：Vite + React + TS(strict) + Tailwind + shadcn init；`src/{wallet,api,ui,features,app}`；lint/format/CI 占位（TC-7）。
- [x] p1-2 `WalletAdapter`（连钱包/enable/getRewardAddresses/signData/转发 + network guard）+ mock `window.cardano` 单测（TC-1）。
- [x] p1-3 `api` client：fetch + 错误信封 + 429 + 类型（TC-7 部分）。
- [x] p2-0 **后端 enabler**：新增 `POST /api/admin/auth/step-up/challenge`（requireSession 内，wire `admin.ChallengeStepUp`，旧仅 test 用）——前端 step-up 需要它取 nonce。
- [x] p2-1 登录/会话/RBAC/step-up + 布局/导航（TC-2）。
- [x] p3-1 业务页一批：Dashboard、名册+撤销(step-up)、订阅+取消、规则编辑器、渠道+telegram 测试、推送+日志、客户端注册(一次性 secret)、密钥轮换(step-up)+JWKS、审计、初始化向导（TC-3）。
- [x] p4-1 构建/部署：静态产物、可 embed/静态托管、env(issuer base URL/network)、CI 增 web job（TC-7/TC-8）。

## 4. Test and Acceptance Criteria
- TC-1 `WalletAdapter`：mock `window.cardano` 覆盖探测/enable/getRewardAddresses/signData；`signNonce` 返回 `{coseKeyHex, signatureHex}` 且**不在浏览器解 CBOR**；network guard 不匹配报错。
- TC-2 登录/RBAC/step-up：owner 登录得 cookie；viewer 看不到 operator/owner 操作；敏感操作缺 step-up 被前端拦/后端 401。
- TC-3 业务页：各页 CRUD 与后端契约一致（名册按 sch、规则 rule_config、推送过滤、客户端一次性 secret、密钥轮换 step-up、审计只读）。
- TC-7 构建：`pnpm build` 出静态资源；类型检查 + lint 绿；可 embed/静态托管。
- TC-8 网络/配置：issuer base URL + network 经 env 注入；network 与钱包不一致时阻断签名。
- Pass/fail：每 item 仅在其映射 TC 全 pass + 证据 append 后标 `[x]`。

## 5. Execution Log (append-only)
- 2026-06-24 S0002 草案创建（draft）：技术选型落定（React+Vite SPA / shadcn / thin CIP-30 + WalletAdapter）。
- 2026-06-24 S0002 修订（draft）：授权页/绑定页 + walletauth 契约移到 S0003；本 spec 收敛为纯 Admin SPA；`WalletAdapter` 简化为"连接+sign+转发、浏览器零 CBOR"；新增 p0-1 依赖 S0003。尚未执行。
- 2026-06-24 S0002 激活（active）：S0003 已 delivered，p0-1 标 [x]。
- 2026-06-25 p1-1 完成：`web/` 脚手架——React 18 + Vite 6 + TS(strict) + Tailwind v4(`@tailwindcss/vite`) + 自写 shadcn 风格 UI 基元（不跑交互式 CLI，copy-in 更可控）。pnpm（lockfile 入库），`src/{lib,wallet,api,auth,ui,app,features,test}` 目录，eslint flat + prettier，vitest(jsdom)，vite dev 代理 `/api`+`/.well-known`→issuer(:8080)。**决策**：① Tailwind v4（CSS 变量主题，免 tailwind.config）；② pnpm `overrides.vite ^6` 去重（vitest 拉 vite5 致类型冲突）+ `onlyBuiltDependencies:[esbuild]`；③ 生产同源（相对 API 路径），dev 用代理。

- 2026-06-25 p1-2 完成：`src/wallet/`——`listWallets()`（探测 window.cardano，宽松判定，跳过非钱包键）+ `connectWallet(key, expectedNetwork?)`→`WalletSession{rewardAddress, signNonce(nonce)}`。`signNonce` 调 `signData(addr, hex(utf8(nonce)))` 并**原样转发** `{coseKeyHex:key, signatureHex:signature}`——浏览器零 CBOR（S0003 C3）。network guard 不匹配抛错、无 reward 地址抛错。`src/lib/hex.ts` utf8ToHex。

- 2026-06-25 p1-3 完成：`src/api/client.ts`（同源 fetch、`credentials:include` 带 cookie、错误信封 `{error,error_description}`→`ApiError{status,code}` + `isUnauthorized/isForbidden/isRateLimited`）+ `src/api/admin.ts`（全部 admin 端点 typed 封装：auth/me/step-up、members/revoke、subscriptions/cancel、rules、channels、push、clients/register、keys rotate/generate、audit、JWKS）+ `src/lib/types.ts`（wire 类型，匹配后端实发 JSON）。**决策**：后端 domain 结构体无 json tag→list 端点发 Go PascalCase（如 `SessionID`），前端 wire 类型照实匹配（后端日后加 snake_case json tag 可再跟随）。

- 2026-06-25 p2-0 完成（后端 enabler，决策落盘）：S0001/S0003 里 `admin.ChallengeStepUp` 没有 HTTP 路由（只被测试直接调），导致前端拿不到 step-up nonce、无法完成撤销/注册 client/轮换密钥。新增 `POST /api/admin/auth/step-up/challenge {owner_stake_address}→{nonce}`（置于 `requireSession` 组内，与 `/me`/`logout` 同闸）。加 `TestAdminStepUpChallenge_Route`（登录后 200+nonce）。**决策**：此后端小改属 S0002 范围内的前置 enabler（S0003 已 close 不重开），跨 `server/`。

- 2026-06-25 p2-1 完成：UI 基元（button/input/textarea/label/card/badge/table/select/dialog(Radix)/toast/spinner，自写 shadcn 风格）+ 认证层。`AuthContext`（TanStack Query 拉 `/me`，401→null）+ `login(walletKey)`（connectWallet→challenge(reward 地址)→signData→verify，cookie 由后端下发）+ `logout`。`useStepUp` + `StepUpDialog`（敏感操作钱包重签→`{cose_key,step_up_nonce,step_up_signature}`）。`useWallets`（轮询 + load/cardano#initialized，沿用 S0003 经验）+ `WalletPicker` + `LoginPage`。`Layout`（侧栏导航按角色过滤 + 角色徽章 + 登出）、`RequireAuth`/`RequireRole`（rank: viewer<operator<owner）、`PageHeader`/`QueryState` 共享件。React Router(createBrowserRouter) + providers（QueryClient>Toast>Auth>Router）。其余业务路由暂用 `Placeholder`（p3-1 替换）。**决策**：钱包 network guard 取 `VITE_ISSUER_NETWORK`（可空→跳过，后端 owner-key 校验为真闸，TC-8）。

- 2026-06-25 p3-1 完成：10 个业务页（features/*）——Dashboard（会员/订阅/JWKS 统计卡）、Members（列表 + 撤销 step-up，operator）、Subscriptions（列表 + 取消，operator）、Rules（列表 + RHF/zod 编辑器，JSON rule_config 校验，operator）、Channels（telegram bot token 配置，operator）、Push（列表 + 新建定向推送，operator）、Clients（列表 + 注册 step-up + **一次性 secret** 弹窗，owner）、Keys（JWKS 状态 + rotate/generate step-up，owner）、Audit（列表，owner）、Setup（就绪 checklist，owner）。共享 `PageHeader/QueryState/Field`、TanStack Query 拉取/失效、`StepUpDialog` 接敏感操作、错误 toast。路由替换 Placeholder 为实页（RBAC gate 不变）。**决策**：Channels 仅做 configure（后端无 telegram 测试路由）；secret 一次性展示 + 复制按钮。

- 2026-06-25 p4-1 完成：构建/嵌入/部署。Vite `base:/admin/` + Router `basename:/admin`（SPA 在 /admin 下）。新增 `server/internal/httpapi/adminui` 包：`//go:embed all:dist` 嵌入构建产物，`Handler()` serve 静态 + history fallback（hashed 资源 immutable 长缓存），未构建时（仅 `dist/.gitkeep`）降级占位页。router 挂 `/admin`(301→/admin/) + `/admin/*`(StripPrefix)。`make web`（构建 ../web + 拷进 embed 目录）/`make web-clean`。`web/.env.example`（`VITE_ISSUER_NETWORK`，TC-8）。CI 增 `web` job（pnpm install/lint/typecheck/test/build）+ paths 加 `web/**`。**决策**：① SPA 挂 `/admin`（与 `/api/admin` 共存、不抢 `/connect` 等根路由）；② 构建产物**不入库**（`dist/*` gitignore，留 `.gitkeep`），发布时 `make web && make build` 嵌入——dev 用 `pnpm dev`(vite :5173/admin/ 代理后端) 热更或 `make web` 后看 /admin。

## 6. Validation Evidence (append-only)
- 2026-06-25 TC-7（部分）| stack: ui | command: `pnpm install` + `pnpm build`（`tsc -b && vite build`） | result: pass | note: 工具链就绪，类型检查 + 生产打包绿（27 模块、JS 144KB/gzip 46KB、CSS 5.3KB）。

- 2026-06-25 TC-1 | stack: ui | command: `pnpm test`（vitest, jsdom, mock window.cardano） | result: pass | note: 8 用例绿——探测/跳过非钱包键/空、signNonce 转发 key+signature 且 payload=hex(utf8(nonce))、错网络拒、无 reward 拒、未知钱包拒、networkName 映射。

- 2026-06-25 TC-7（部分）| stack: ui | command: `pnpm typecheck`（tsc -b） | result: pass | note: api client + 类型全量类型检查绿。

- 2026-06-25 p2-0 | stack: go | command: `go build ./...` + `go test ./internal/httpapi/` | result: pass | note: 新 step-up challenge 路由编译 + httpapi 全包测试绿（含新路由测试）。

- 2026-06-25 TC-2 | stack: ui | command: `pnpm test`（vitest）+ `pnpm build` | result: pass | note: `RequireRole` RBAC 单测 3 例（足/不足/相等）+ wallet 8 例共 11 绿；生产打包绿（1660 模块、JS 317KB/gzip 103KB）。登录得 cookie / step-up 401 属集成（后端联调/手测）。

- 2026-06-25 TC-3 | stack: ui | command: `pnpm build`（tsc+vite）+ `pnpm lint`（0 error）+ `pnpm typecheck` + `pnpm test` | result: pass | note: 10 页全量类型检查 + 打包绿（1745 模块、JS 463KB/gzip 145KB、CSS 17KB）；lint 0 error（2 个 react-refresh warning，hook 与 provider 同文件，无害）；11 单测绿。各页消费契约：members 按 sch、rules rule_config(JSON)、push target 过滤、client 一次性 secret、keys step-up、audit 只读。

- 2026-06-25 TC-7/TC-8 | stack: ui+go | command: `make web`+`go build`+二进制 smoke / `pnpm {lint,typecheck,test,build}` / `go build vet test ./...` / CI `yaml.safe_load` | result: pass | note: `make web` 出静态产物并 stage，issuer 二进制 `/admin/` 返回真 index.html（引 `/admin/assets`）、hashed JS 200+immutable、`/admin/dashboard` SPA fallback 200、`/admin`→301；未构建时占位 200。web 全绿(lint 0 error/typecheck/11 测/打包)；后端 build+vet+全测 0 FAIL；CI 3 job(test/integration/web) yaml 合法。network 经 `VITE_ISSUER_NETWORK` env 注入。

## 7. Change Requests (append-only)
- 2026-06-24 选型：框架 React+Vite 纯 SPA（用户确认）；组件库 shadcn/ui（用户确认）；钱包 thin `window.cardano` 封装（用户质疑 Weld 成熟度：~550 下载/月、pre-1.0；且 CBOR 解码改放后端后前端只需转发，thin-wrapper 最契合，库藏 `WalletAdapter` 后可换）。
- 2026-06-24 范围修订：授权页是 issuer 服务的 HTML（设计 §9.4），非 web/ SPA；连同 walletauth 契约改造拆到 S0003，S0002 收敛为纯 Admin。CBOR/vkey 抽取归后端 → 前端零 CBOR。
