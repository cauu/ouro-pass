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
- [x] p3-1-fix1 **fix**（验收发现）：Setup/Dashboard 的 `data?.X.length` 只对 `data` 可选链、未对数组 `X`，接口返回缺 `keys`/`members` 等字段时 `undefined.length` 崩页；改为 `data?.X?.length`（全仓扫净同类）。Setup 页 = owner 首次配置就绪清单（其"规则"步将随 S0004 删 rules 改写）。
- [x] p3-1-fix2 **fix**（验收发现）：Keys 页生成/轮换密钥后 JWKS 列表 ~60s 不更新（仍显示空/旧值）。根因——KeysPage 以公开 `GET /.well-known/ouropass/jwks.json` 为数据源，后端给它发 `Cache-Control: public, max-age=60`（面向 verifier 的有意缓存），而 admin client 的 `fetch` 未设 `cache`，react-query 失效后重取命中浏览器 HTTP 缓存返回过期空列表。修复——admin client GET 加 `cache: "no-store"` 绕过浏览器缓存（verifier 侧缓存保持不变）。后端 Rotate 逻辑无误；"两个 key"= active+rotating 正常重叠。
- [x] p3-1-fix3 **fix**（验收发现）：Keys 页看不出哪个 key 在签名。后端 JWKS 每个 key 本已带 `status` 字段（`jose.BuildJWKS`，active|rotating|retired），但前端 `Jwk` 类型漏声明 `status`、表格也无 Status 列，把唯一可区分字段丢了。修复——`Jwk` 加 `status?`，表格加 Status 列徽章（`active`→`active (signing)` success 高亮，rotating/retired→muted）。澄清：当前签名 key = 唯一 `active`（`keys.ActiveSigner` 取最新 active），`rotating` 仅验证旧 token。
- [x] p3-1-fix4 **fix**（UX，用户确认）：Keys 页 Generate / Rotate 两个按钮功能完全相同（同 handler），易误导。合并为单按钮：无 active key 时显示 "Generate"（bootstrap 首个 key），有 active key 时显示 "Rotate"（新 active + 旧降 rotating）；标题/说明同步切换，按意图调对应端点（功能等价）。后端两路由暂保留不动（前端仅暴露一个入口）。
- [ ] p5-1 **（后端，延后/不采用）** rotating key 退役**自动 worker**：`keys.RetireRotating(olderThan)` 无生产调用方，rotating key 随每次 rotate 累积。原拟后台 worker 周期退役。**决策（用户确认）**：不做自动 worker——① 现状判据 `RetireRotating` 以 `ValidFrom`（激活时刻）为 cutoff，而非降级时刻+TTL，可能在旧 token 仍有效时过早退役（潜在 bug，且 schema 无 `demoted_at` 列需迁移）；② rotate 稀有、JWKS 增长极慢、收益近零。改为手动退役（p5-2）。本项保留为"仅当 JWKS 真膨胀到困扰时再考虑，且需先补 `demoted_at` 并修判据"。
- [x] p5-2 **（手动退役，用户确认采用）** owner 手动退役单个 rotating key：后端 `keys.Service.Retire(kid)`（仅 `rotating` 可退，`ErrNotRotating`/`ErrNotFound` 守卫）+ `POST /api/admin/keys/issuer/{kid}/retire`（owner + step-up，404/409 映射）+ 审计 `issuer_key.retire`；前端 `retireKey()` + Keys 表 rotating 行加 "Retire"（step-up 弹窗，含"仅在 token 过期后退役"警示）。绕开自动方案的时间启发式，由 owner 判时点。
- [x] p6-1 **（OAuth client 注册简化，用户要求）** `client_id` 改为系统生成（同 `client_secret` 已是）：后端去掉请求体 `client_id`、改为 `"op-client-"+RandomToken(9)` 生成（不可被调用方指定/猜测/碰撞），必填字段从 `client_id` 改 `name`，响应回 `client_id`（+ confidential 的一次性 secret）。前端 `ClientRegister`/表单去掉 Client ID 输入，注册成功面板**始终展示生成的 client_id**（public 也展示，不再仅 toast）+ confidential 再附 secret，可一键复制。
- [x] p6-2 **（死字段精简，用户定 A：两者皆删）** 注册表单死字段精简：`party`（0 处读取）、`allowed_scopes`（authorize 不强制、空操作）。已选**方案 A：两者皆删**（用户答"party 和 scopes 都可以删"；并确认保留 audiences/type，已解释二者用途）。端到端删除：DB 列（迁移 0007 DROP COLUMN，sqlite+postgres）、domain（`ClientParty` 类型/常量/`Valid` + `Party`/`AllowedScopes` 字段）、repo（INSERT/SELECT/scan）、handler（请求体 + party 校验）、前端（`OAuthClient`/`ClientRegister`/表单输入/Party 列）。详见执行日志。

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

- 2026-06-25 验收支持工具：`make dev` 增 `DEV_OWNER_KEYS` 透传 `OUROPASS_OWNER_KEYS`（设置后可用该 owner 钱包登录 /admin）；新增 `cmd/stakehash` + `make stake-hash ADDR=stake1...` 从钱包 stake 地址算出 owner key hash（复用 `chain.StakeHashFromRewardAddress`）。验收登录链路：钱包复制 stake 地址 → `make stake-hash` → `make web` → `make dev DEV_OWNER_KEYS=<hash>` → 开 `http://localhost:8080/admin/` 用同一钱包登录。

- 2026-06-25 p3-1-fix1 完成（fix，验收发现）：Setup 页报错。根因——`(jwks.data?.keys.length ?? 0)` 只可选链 `data`、未链数组 `keys`，接口返回不含 `keys` 的形状时 `undefined.length` 崩页；Setup 三处(keys/rules/clients)+ Dashboard 两处(members/keys)同患。改为 `data?.X?.length`，全仓扫净同类不安全访问；`pnpm build` 绿。

- 2026-06-25 p3-1-fix2 完成（fix，验收发现）：Keys 页 generate/rotate 返回成功（`status:active`,`jwks_updated:true`）但 JWKS 列表 ~60s 不刷新、过后才显示（此时 active+rotating=2 key）。排查链：KeysPage(`fetchJwks`)→公开 `GET /.well-known/ouropass/jwks.json`，后端发 `Cache-Control: public, max-age=60`（`handlers_verifier.go:29`，verifier 缓存有意为之）；`client.ts` 的 `fetch` 未设 `cache`，故 `invalidateQueries(["jwks"])` 重取命中浏览器磁盘缓存返回 60s 内"新鲜"的空 `[]`。**决策**：不动后端缓存（保留对 verifier 的减压），仅在 admin client 的统一 `request()` 加 `cache:"no-store"`——admin SPA 恒取实时数据，覆盖此类及未来被缓存端点。后端 `keys.Rotate`/`PublicJWKSKeys` 逻辑核对无误。

- 2026-06-25 p3-1-fix3 完成（fix，验收发现）：用户分不清哪个 key 在签名。核对：① Generate 与 Rotate 同一 handler（`handlers_admin_resources.go:33-34`→`adminRotateKey`→`Rotate()`），Generate 即"建新 active + 旧 active 降 rotating"——两按钮功能相同（待后续 UX 收敛）；② 签名 key=`keys.ActiveSigner` 取唯一/最新 `active`，rotating 仅验证旧 token；③ JWKS 本带 `status`（`jose.go:93`），但前端 `Jwk` 漏声明、表格无该列。修复——`Jwk` 加 `status?` + 表格 Status 列徽章（active 高亮"active (signing)"）。`pnpm build` 绿。

- 2026-06-25 p3-1-fix4 完成（UX 收敛，用户确认 fix3 遗留）：KeysPage 两按钮合一。`hasActiveKey = keys.some(status==="active")`：无→"Generate"（建首个 active）、有→"Rotate"；`StepUpDialog` 的 title/description/onConfirm 端点随之切换（generate vs rotate，后端等价）。后端两路由保留（前端只暴露一个入口），不动后端 scope。同时把"rotating 退役驱动器"记为延后项 p5-1（用户确认本 spec 内后补）。`pnpm build` 绿、lint 0 error。

- 2026-06-25 p5-2 完成（手动退役，用户拍板"直接做手动 retire"）：后端 `keys.go` 加 `Retire(ctx,kid)`（`repo.Get` 查状态，非 `rotating`→`ErrNotRotating`，缺失→`ErrNotFound`，否则 `SetStatus(retired)`）；`handlers_admin_resources.go` 加 `adminRetireKey`（owner+step-up，`{kid}` path 参，错误映射 404/409，审计 `issuer_key.retire`）+ 路由 `POST /keys/issuer/{kid}/retire`。前端 `admin.ts` 加 `retireKey(kid,su)`；`KeysPage` 表加 Actions 列，仅 rotating 行显示 "Retire"（StepUpDialog，文案警示"仅在其 token 过期后退役，否则那些 token 验签将失败"）。新增 `TestRetire`（拒退 active、拒未知 kid、退 rotating 后 JWKS 掉该 key 且 active 仍可签、拒重复退）。决策：p5-1 自动 worker 不采用（判据/schema 隐患 + 收益近零），手动方案由 owner 判时点、最简且安全。

- 2026-06-25 p6-1 完成（client_id 系统生成，用户"id 和 secret 都应由系统生成"）：核对——secret 本已系统生成（confidential `RandomToken(32)`、回显一次、存 hash），仅 `client_id` 手填。改：`adminRegisterClient` 删请求体 `ClientID`、必填校验改 `name`、生成 `clientID="op-client-"+RandomToken(9)`（72-bit，沿用 `op-issuer-` 命名风格），audit/response 用生成值。前端 `ClientRegister` 与 `ClientForm` 去 `client_id`、表单删该输入、`signAndRegister` 校验改 `name`；注册结果由"仅 confidential 弹 secret"改为 `result{clientId,secret?}` 面板——**始终显示 client_id**（public 也显示，原仅 toast），confidential 再附 secret，复制按钮 id（+secret）。测试：`TestAdminOwner_RegisterClientAndRotateKey` 加断言"供给的 `client_id:c1` 被忽略、返回 `op-client-…`"；新增"缺 name→400"用例；两条坏 type/party 用例补 `name` 以真正命中各自校验。决策：本项只做 id 生成；`party`/`allowed_scopes` 死字段精简单列为 p6-2 待用户拍板。

- 2026-06-25 p6-2 完成（死字段精简，用户答"party 和 scopes 都可以删"；并问 audiences/type 何用 → 已解释：type=public/confidential 凭证模型、audiences=token 受众白名单，二者真生效予以保留）。方案 A 端到端删 `party`+`allowed_scopes`：① 迁移 `0007_drop_client_party_scopes.sql`（sqlite+postgres，`ALTER TABLE OAuthClient DROP COLUMN`；驱动 modernc sqlite v1.53/pgx 均支持——注意原两列 `NOT NULL` 无默认，故不可仅停写、必须删列）；② `domain.OAuthClient` 删 `Party`/`AllowedScopes` 字段 + 删 `ClientParty` 类型/`FirstParty`/`ThirdParty`/`Valid`；③ `repo_oauthclient.go` 三处（Upsert/Get/List）SQL 列与 scan 同步；④ handler 删请求体 `Party`/`Scopes` + 删 party `Valid()` 校验块 + 构造体去字段；⑤ 前端 `OAuthClient`/`ClientRegister`/`ClientForm`/defaults/body 去两字段、表单删 Party 下拉 + Scopes 输入、客户端表删 Party 列。测试同步：删 6 个测试文件中 `OAuthClient{}` 字面量的 `Party`/`AllowedScopes`、`repo_clients`/`pg_concurrency` 断言改查 audiences、`TestAdminF2_RejectsInvalidEnums` 删失效的"bad party→400"用例（party 校验已不存在）。

## 6. Validation Evidence (append-only)
- 2026-06-25 TC-7（部分）| stack: ui | command: `pnpm install` + `pnpm build`（`tsc -b && vite build`） | result: pass | note: 工具链就绪，类型检查 + 生产打包绿（27 模块、JS 144KB/gzip 46KB、CSS 5.3KB）。

- 2026-06-25 TC-1 | stack: ui | command: `pnpm test`（vitest, jsdom, mock window.cardano） | result: pass | note: 8 用例绿——探测/跳过非钱包键/空、signNonce 转发 key+signature 且 payload=hex(utf8(nonce))、错网络拒、无 reward 拒、未知钱包拒、networkName 映射。

- 2026-06-25 TC-7（部分）| stack: ui | command: `pnpm typecheck`（tsc -b） | result: pass | note: api client + 类型全量类型检查绿。

- 2026-06-25 p2-0 | stack: go | command: `go build ./...` + `go test ./internal/httpapi/` | result: pass | note: 新 step-up challenge 路由编译 + httpapi 全包测试绿（含新路由测试）。

- 2026-06-25 TC-2 | stack: ui | command: `pnpm test`（vitest）+ `pnpm build` | result: pass | note: `RequireRole` RBAC 单测 3 例（足/不足/相等）+ wallet 8 例共 11 绿；生产打包绿（1660 模块、JS 317KB/gzip 103KB）。登录得 cookie / step-up 401 属集成（后端联调/手测）。

- 2026-06-25 TC-3 | stack: ui | command: `pnpm build`（tsc+vite）+ `pnpm lint`（0 error）+ `pnpm typecheck` + `pnpm test` | result: pass | note: 10 页全量类型检查 + 打包绿（1745 模块、JS 463KB/gzip 145KB、CSS 17KB）；lint 0 error（2 个 react-refresh warning，hook 与 provider 同文件，无害）；11 单测绿。各页消费契约：members 按 sch、rules rule_config(JSON)、push target 过滤、client 一次性 secret、keys step-up、audit 只读。

- 2026-06-25 TC-7/TC-8 | stack: ui+go | command: `make web`+`go build`+二进制 smoke / `pnpm {lint,typecheck,test,build}` / `go build vet test ./...` / CI `yaml.safe_load` | result: pass | note: `make web` 出静态产物并 stage，issuer 二进制 `/admin/` 返回真 index.html（引 `/admin/assets`）、hashed JS 200+immutable、`/admin/dashboard` SPA fallback 200、`/admin`→301；未构建时占位 200。web 全绿(lint 0 error/typecheck/11 测/打包)；后端 build+vet+全测 0 FAIL；CI 3 job(test/integration/web) yaml 合法。network 经 `VITE_ISSUER_NETWORK` env 注入。

- 2026-06-25 p3-1-fix1 | stack: ui | command: `pnpm build`(tsc+vite) + grep 扫描数组可选链 | result: pass | note: 修复后打包绿;无残留 `data?.X.length` 类不安全访问。

- 2026-06-25 p3-1-fix2 | stack: ui | command: `pnpm build`(tsc -b && vite build) | result: pass | note: admin client GET 加 `cache:"no-store"` 后打包绿（1745 模块、JS 463KB/gzip 145KB）；生成/轮换后 jwks 重取不再命中浏览器缓存。后端 jwks `Cache-Control` 保持不变（verifier 侧）。

- 2026-06-25 p3-1-fix3 | stack: ui | command: `pnpm build`(tsc -b && vite build) | result: pass | note: `Jwk.status` + Keys 表 Status 列徽章打包绿（1745 模块、JS 463KB/gzip 145KB）；active key 显示"active (signing)"，rotating/retired muted。

- 2026-06-25 p3-1-fix4 | stack: ui | command: `pnpm build`(tsc -b && vite build) + `pnpm lint` | result: pass | note: 单按钮按 `hasActiveKey` 切 Generate/Rotate；打包绿（1745 模块、JS 463KB/gzip 145KB），lint 0 error（仅 toast.tsx 既有 react-refresh warning ×2，与本改无关）。

- 2026-06-25 p5-2 | stack: go | command: `go test ./internal/core/keys/ ./internal/httpapi/` + `go vet ./...` + `go build ./...` | result: pass | note: `TestRetire` 绿（拒 active/未知 kid、退 rotating 后 JWKS 掉 key 且 active 仍签、拒重复退）；keys+httpapi 全包绿；vet/build 0 error。
- 2026-06-25 p5-2 | stack: ui | command: `pnpm build`(tsc -b && vite build) + `pnpm lint` | result: pass | note: `retireKey` + Keys 表 rotating 行 Retire 按钮（step-up）打包绿（JS 463KB/gzip 145KB），lint 0 error（仅 toast.tsx 既有 warning ×2）。

- 2026-06-25 p6-1 | stack: go | command: `go test ./internal/httpapi/` + `go build ./...` | result: pass | note: 注册返回系统生成 `op-client-…`、忽略供给 id、缺 name→400、坏 type/party→400 全绿；httpapi 全包绿。
- 2026-06-25 p6-1 | stack: ui | command: `pnpm build`(tsc -b && vite build) + `pnpm lint` | result: pass | note: 表单去 Client ID 输入、成功面板始终显示生成的 client_id（+confidential secret）打包绿（JS 463KB/gzip 145KB），lint 0 error。

- 2026-06-25 p6-2 | stack: go | command: `go build ./...` + `go vet ./...` + `go test ./...` + `go vet -tags=integration ./internal/inttest/` | result: pass | note: 删 party/allowed_scopes（含迁移 0007）后全包 build/vet/test 绿（store/e2e 跑 Migrate 含新迁移）；integration-tagged pg 测试编译通过。
- 2026-06-25 p6-2 | stack: ui | command: `pnpm build`(tsc -b && vite build) + `pnpm lint` | result: pass | note: 去 Party 下拉/Scopes 输入/Party 列后打包绿（JS 463KB/gzip 145KB），lint 0 error。

## 7. Change Requests (append-only)
- 2026-06-24 选型：框架 React+Vite 纯 SPA（用户确认）；组件库 shadcn/ui（用户确认）；钱包 thin `window.cardano` 封装（用户质疑 Weld 成熟度：~550 下载/月、pre-1.0；且 CBOR 解码改放后端后前端只需转发，thin-wrapper 最契合，库藏 `WalletAdapter` 后可换）。
- 2026-06-24 范围修订：授权页是 issuer 服务的 HTML（设计 §9.4），非 web/ SPA；连同 walletauth 契约改造拆到 S0003，S0002 收敛为纯 Admin。CBOR/vkey 抽取归后端 → 前端零 CBOR。
- 2026-06-25 验收期 Keys 页一组改动（用户确认）：① 缓存导致更新不可见 → admin GET no-store（p3-1-fix2）；② 暴露签名 key 状态（p3-1-fix3）；③ Generate/Rotate 合一，无 key 叫 Generate、有 key 叫 Rotate（p3-1-fix4）。决策：均在本 active spec 追加，不开新 spec。后端"rotating 退役驱动器"确认为已知缺口、当前不影响功能 → 记为本 spec 延后项 p5-1，暂不实现（后端 scope，后补）。
