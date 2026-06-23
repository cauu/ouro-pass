# Ouro Pass Issuer Service 概要设计

> 精简版 · 2026-06-22 · **OAuth 登录版**（本期移除冷钥 license 证书链）。完整细节见 `poolops-issuer-detailed-design.md`。

## 1. 是什么

SPO 自托管的 **质押身份 OAuth 登录 + 多渠道订阅后端**。做两件事：

1. **会员登录与授权**：用户用钱包证明对指定 stake pool 的委托（staking 身份）→ OAuth 登录 → 拿到带会员等级/权益的 **access token**，用于门控 app 功能。
2. **多渠道订阅**：统一管理用户在 Telegram / Discord / Email 等渠道的订阅关系与内容推送。

> 本期**不做**可被第三方离线、独立验证的冷钥锚定 license；token 由 issuer 自己的签名密钥签发、自己（或第一方 app 经 JWKS）校验。等出现第三方/离线生态验证需求时再加证书链，见 §7 演进。

## 2. 架构

**签发模型（单签名密钥，无证书链）**：

```text
Issuer Signing Key (服务内, 可轮换 Ed25519)
   └─签发→ access token (短效 JWT) / activation token
```

- app / 第一方消费方通过 issuer **JWKS**（一组签名公钥，无证书链）验签，或走 `introspect` 在线校验。
- 没有 cold/owner/issuer 三层链，没有 air-gapped 仪式。

**三端**：

| 端 | 职责 | 状态 |
|---|---|---|
| Admin 前端 | SPO 运营：规则、会员、推送、密钥轮换 | 有状态，owner key 登录 |
| Authorization Page | 纯授权页：连钱包 → 签名 → 拿 code | 无业务状态 |
| 后端 API | 四平面 API + bot worker + 调度 worker | 唯一真源 |

## 3. 核心流程

### 3.1 初始化（一次性）

```text
部署容器 → 配 pool id 与 staking 数据源 → 生成 issuer 签名密钥（发布到 JWKS）
→ 配会员规则与渠道 → 启用
```

> Admin 身份无需种子：`owner` 角色登录时对照**链上 pool owner 列表**校验（取代旧版导入冷钥证书的步骤），`operator`/`viewer` 由 owner 在后台添加。

### 3.2 用户登录（OAuth 授权码流）

钱包交互（CIP-30）只能在用户真实浏览器顶层窗口；因此 native App 走「系统浏览器 + loopback」（RFC 8252），Web App 走后端回调（BFF）。登录成功后拿到 **access token + refresh token**。

**Native App（public client，系统浏览器 + PKCE）：**

```text
1. App 本地生成设备 keypair（私钥进 OS keystore）+ PKCE code_verifier/challenge
2. App 起回环监听 127.0.0.1:PORT（或注册 ouro:// deep link）
3. 调起系统浏览器 → /connect?...&code_challenge=...&device_pubkey=<设备公钥>
4. 用户在浏览器连钱包、签 nonce 证明控制 stake credential
5. issuer 查 active snapshot → rule engine 算资格 → 生成一次性 code
6. 浏览器回跳 127.0.0.1:PORT/cb?code=...
7. App 用 code + code_verifier → POST /api/oauth/token（带 DPoP）
8. issuer 返回 { access_token, refresh_token }；存入 OS keystore
```

**Web App（confidential client / BFF）：**

```text
1. 前端「登录」→ 顶层跳转 issuer /connect（redirect_uri = Web App 后端回调）
2. 用户连钱包、签 nonce
3. issuer 回跳 Web App 后端 /cb?code=...
4. 后端用 code + client_secret → POST /api/oauth/token
5. 后端拿 { access_token, refresh_token }：refresh_token 只存服务端，绝不下发前端
6. 后端与前端建 httpOnly session cookie；后端代持/代刷 token
```

**客户端分型（决定凭证保护方式）：**

| 客户端 | client_secret | 凭证保护 | refresh |
|---|---|---|---|
| Public（native App、纯 SPA） | 无 | PKCE + 设备 PoP（sender-constrained，token `cnf.jkt`） | 设备私钥签 DPoP |
| Confidential（带后端 Web App / BFF） | 有 | client_secret 当 holder-of-key，refresh token 留服务端 | 后端用 client_secret |

第一方 Web App 与第三方网站是同一套 confidential client 流程，区别仅在 client 注册与钱包连接托管位置。

### 3.3 Token 刷新

```text
App/后端 发起 refresh（public 带 DPoP / confidential 带 client_secret）
→ 验 refresh token（active/未过期/未轮换）→ 重查 active snapshot → 重评资格
→ 符合则签发新 access token + 轮换 refresh token，否则降级/拒绝
```

> 资格仅基于 **active stake snapshot**；委托进入/退出有 ≤2 epoch 滞后，视为隐含宽限。正常刷新对用户无感；仅 refresh token 失效时才需重新连钱包登录。

### 3.4 Telegram 激活与推送

bot 是 issuer 的一个 worker（同库同 DB），无跨服务交接。

**激活（绑定 stake 身份 ↔ telegram user）：**

```text
1. 用户在第一方绑定页连钱包签 nonce
   → issuer 验签 + 查会员资格 → 资格 OK 才生成一次性 activation token
   → 返回 deep link https://t.me/<bot>?start=<code>
2. 用户点链接打开 bot → 自动发送 /start <code>
3. issuer 收到消息（含 telegram_user_id 与 code）：
   校验 token → 建 SubscriptionSession（绑 telegram_user_id）→ 标记 jti 已消费
4. bot 回复订阅成功
```

activation token 一次性、短效、绑 channel，且自带资格（兑换零查链）；telegram_user_id 来自 Telegram 已鉴权 update，冒充不了。其他命令：`/status`、`/unsubscribe`；session 由 Reconciliation Job 每 epoch 维护（掉级自动降级/失效）。

**推送：**

```text
1. SPO 建推送任务（目标 topic / entitlement / tier + 定时）
2. issuer 查 active session → 发送前校验会员状态 / entitlement
3. 调 Telegram API（限速 + 退避重试）→ 记 DeliveryLog
```

接入方式：MVP 用 Long Polling；生产可切 Webhook。

### 3.5 签名密钥轮换 / token 吊销

无证书链后，轮换与吊销都很轻：

- **签名密钥轮换**：生成新 kid → 发布到 JWKS（新旧 overlap）→ 新 token 用新 kid 签；旧 token 靠自身短有效期（access 1–7 天）自然过期 → 旧 kid 退役、私钥销毁。零停机、对用户无感。
- **token 吊销**：标记 IssuedToken / RefreshGrant 为 revoked，`introspect` 即时反映；access token 因短效，离线消费方最坏在一个有效期内收敛。
- **密钥泄露**：撤下泄露 kid（从 JWKS 移除 + 标 revoked），其签出的 token 作废，受影响用户下次 refresh 在新 kid 下自动重签。

涉及接口：`POST /api/keys/issuer/generate`、`POST /api/keys/issuer/rotate`、`POST /api/oauth/revoke`、`GET /.well-known/ouropass/jwks.json`；敏感操作需 admin step-up。

## 4. 接口定义

四个逻辑平面，鉴权各自独立。**签发与刷新统一走 OAuth token 端点**。

### 4.1 全部端点

| 方法 | 端点 | 平面 | 鉴权 | 用途 |
|---|---|---|---|---|
| POST | `/api/auth/challenge` | 钱包原语 | 无 | 取签名 nonce |
| GET | `/connect` | 登录(OAuth) | 钱包（页面内） | Authorization Page（HTML） |
| POST | `/api/connect/authorize` | 登录(OAuth) | 钱包签名 | 授权页提交 → 授权码 |
| POST | `/api/oauth/token` | 登录(OAuth) | client_secret / PKCE+DPoP | 换码/刷新 → access+refresh token |
| POST | `/api/oauth/introspect` | Verifier | 限速 | 查 token 状态（RFC 7662） |
| POST | `/api/oauth/revoke` | Verifier/Admin | client/admin | 吊销 token（RFC 7009） |
| GET | `/.well-known/ouropass/jwks.json` | Verifier | 无 | 签名公钥（无证书链） |
| POST | `/api/activation/create` | 渠道激活 | 钱包签名 | 生成 activation token + deep link |
| POST | `/api/admin/auth/challenge` | Admin | 无 | 取登录 nonce |
| POST | `/api/admin/auth/verify` | Admin | owner 签名 | 登录 → session |
| GET | `/api/admin/members[/:sch]` | Admin | viewer+ | 派生名册 / 详情（按 stake_credential_hash） |
| POST | `/api/admin/members/:sch/revoke` | Admin | operator+ | 拉黑（写 Blacklist + 级联撤销 token/grant/session） |
| GET | `/api/subscriptions` · `POST .../:id/cancel` | Admin | viewer+/operator+ | 订阅管理 |
| GET/POST/PATCH | `/api/rules` | Admin | operator+ | 规则增改查 |
| POST | `/api/channels/:type/configure` · `/telegram/test` | Admin | operator+ | 渠道配置 |
| GET/POST | `/api/push/jobs` · `POST .../:id/cancel` | Admin | operator+ | 推送任务 |
| GET/POST | `/api/oauth-clients` | Admin | owner | 注册 client |
| POST | `/api/keys/issuer/generate` · `/rotate` | Admin | owner+step-up | 签名密钥生成/轮换 |
| GET | `/api/audit` | Admin | owner | 审计日志 |

Telegram Bot（非 REST，长轮询）：`/start <code>` · `/activate <code>` · `/status` · `/unsubscribe` · `/help`。

### 4.2 关键端点

`POST /api/oauth/token`

```json
// 请求（换码）
{ "grant_type":"authorization_code", "code":"...", "client_id":"...",
  "client_secret":"<confidential>", "code_verifier":"<PKCE>", "redirect_uri":"..." }
// 请求（刷新）
{ "grant_type":"refresh_token", "refresh_token":"...", "client_id":"...", "client_secret":"?" }
// 响应（两者一致）
{ "access_token":"<jwt>", "token_type":"Bearer", "refresh_token":"<opaque,轮换>",
  "expires_at":"...", "membership":{ "status":"eligible_member", "tier":"gold" } }
```

`access_token`（JWS/EdDSA）header `{ typ, alg:"EdDSA", kid }`；payload `{ iss, sub(派生伪匿名), aud, iat/nbf/exp, jti, tier, entitlements, cnf?(public 设备绑定) }`。**无 cert_hash、无证书链。**

## 5. 数据模型（实体一览）

```text
PoolConfig · IssuerKey(签名密钥)
MembershipRule · StakeSnapshotCache(可选) · Blacklist(可选)   # 身份键=stake_credential_hash，无 Member 表
IssuedToken · RefreshGrant · AuthorizationCode · ActivationCode · AuthNonce
OAuthClient · ChannelConfig · SubscriptionSession
PushJob · DeliveryLog
AdminUser · AdminSession · AuditLog
```

字段定义见详细设计 §2–§8。

## 6. 关键约定

- **Access token**：JWS/EdDSA，短有效期（access 1–7 天，activation 5–60 分钟），public client 设备 PoP（`cnf.jkt`）防转发。
- **签名密钥隔离**：服务只持 issuer 签名密钥（加密落盘）与 bot token；KES / VRF / payment key 不进服务。owner key 仅用于 admin 登录签名，不存服务。
- **Admin 登录**：owner stake key 签 nonce，服务端对照**链上 pool owner 列表**校验（不依赖任何本地证书）。
- **资格判定**：仅基于 active snapshot，按 epoch 边界由 Reconciliation Job 重算。

## 7. 演进：何时加回 license

本期是「第一方 OAuth 登录」。当出现以下需求时再引入冷钥锚定的离线可验证 license（即旧设计的三层证书链）：

- 第三方在**不信任/不调用**本 issuer 的前提下，需密码学验证 pool 会员身份；
- 需要面向生态的 verifier SDK；
- app 需**完全离线**自证资格且要求 pool 级信任锚。

届时 access token 与 license 可并存：登录走 OAuth（轻），离线/第三方验证走 license（重）。
