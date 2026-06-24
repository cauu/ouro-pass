# Ouro Pass 授权流后端改造 — walletauth 契约（COSE_Key/hash）+ issuer 服务的授权/绑定页

Spec-ID: S0003
Status: active
Created Time: 2026-06-24T02:10:00+08:00
Start Time: 2026-06-24T02:30:00+08:00
Completion Time:
Previous Spec-ID: (none)
Closure Reason:

## 1. Requirement Details

### Background
CIP-30 钱包**只能在 `signData` 之后**经返回的 `DataSignature.key`(COSE_Key)拿到裸 32B Ed25519 stake 公钥；`getRewardAddresses()` 只给地址(哈希)、无裸公钥。但 S0001 的 `walletauth.Challenge` **要求签名前就给 `stake_vkey`** 来绑定 nonce → **先有鸡还是先有蛋**，标准 CIP-30 钱包跑不通现有契约（实现期注释"rich page lives in web/"是当时的偏离假设）。

四条钱包签名流（issue / activation / admin_login / step_up）**共用 `walletauth`**，全部受影响。本 spec 是 web 前端（S0002）的**前置**：① 修 walletauth 契约；② 按设计把 **`GET /connect` 改为 issuer 直接返回完整授权页 HTML**（非占位），渠道绑定页同理。

### Scope（本 spec 覆盖，均在 `server/`）
- **`walletauth.Challenge` 改造**：收**stake 标识(reward 地址)**而非裸 vkey；后端解析出 28B stake key hash → 绑 nonce。
- **`walletauth.Verify` 改造**：收 **COSE_Key(`signData.key`) + COSE_Sign1(`signData.signature`)**；后端解 COSE_Key 抽 vkey(label `-2`)，做**双重校验**：`cose.Verify(vkey, nonce)` + `blake2b224(vkey) == 绑定 hash`；返回 stake credential hash。
- **新增 crypto/解析**：`StakeVkeyFromCOSEKey(coseKeyHex)`、reward 地址→28B hash（复用 `utils/chain/stakeaddr.go`，兼容 raw-hex / CBOR-bstr-wrapped / bech32）。
- **四条流 handler + 契约同步**：`POST /api/connect/authorize`、`POST /api/activation/create`、`POST /api/admin/auth/{challenge,verify}`、step-up。
- **nonce payload 编码契约**钉死：`signData` 的 payload = `hex(utf8(nonce))`，后端 `cose.Verify` 比对 `[]byte(nonce)`；加互操作测试。
- **授权页 + 渠道绑定页（issuer 服务的 HTML）**：`GET /connect` 返回**完整**授权页 HTML（Go 模板）+ 内嵌 vanilla JS（探测钱包 / enable / `getRewardAddresses` / `signData` / 转发两块 hex）；产物 `embed` 进二进制。绑定页同。

### Constraints
- C1 **安全不变量**：抽 vkey 后**必须**「验签名 + 比 hash」两道，缺一不可。stake vkey **非秘密**（链上委托证书会暴露），所以光比 hash 不够，签名验证才是真鉴权；`/challenge` 的 hash 仅是声明，由 `/authorize` 的签名+hash 匹配兜底。
- C2 **CBOR 解析稳健**：用 `fxamacker/cbor` 解成 `map[int]cbor.RawMessage`、读 label `-2`、**校验 len==32 + kty=OKP(1)/crv=Ed25519(6)**；设输入大小上限防恶意 CBOR。**不按固定字节偏移**（钱包 map 顺序/定长/多带 `kid` 各异）。
- C3 **一处改、四流一致**：改的是共享原语 `walletauth`，四条流契约同步、**不漏 admin 登录与 step-up**。
- C4 **授权页 = 后端 HTML 模板 + vanilla JS、浏览器零 CBOR/零切片**（只转发 hex）；**授权页绝不持有 token**，只产 code 并回跳 `redirect_uri`。
- C5 **reward 地址解析全在后端**，兼容各钱包 `getRewardAddresses` 的格式差异（前端原样转发）。

### Non-goals
- Admin SPA（S0002）。
- CIP-95 路径（备选，不采用）。
- 链上交易构建。

## 2. Outline Design

### 2.1 契约变更（新旧对比）
| 端点 | 旧 | 新 |
|---|---|---|
| `POST /api/auth/challenge` | `{purpose, stake_vkey}` | `{purpose, stake_address}`（reward 地址，hex/bech32） |
| `POST /api/connect/authorize` | `{..., stake_vkey, signature}` | `{..., cose_key, signature}` |
| `POST /api/activation/create` | `{channel_type, nonce, stake_vkey, signature}` | `{channel_type, nonce, cose_key, signature}` |
| `POST /api/admin/auth/challenge` | `{owner_vkey}` | `{owner_stake_address}` |
| `POST /api/admin/auth/verify` | `{nonce, owner_vkey, signature}` | `{nonce, cose_key, signature}` |
| step-up challenge/verify | `owner_vkey` | reward 地址 / `cose_key` |

### 2.2 内部签名
- `walletauth.Challenge(ctx, purpose, stakeAddress string) (nonce, exp, err)` — 解地址→28B hash→绑 nonce。
- `walletauth.Verify(ctx, purpose, coseKeyHex, nonce, signatureHex string) (stakeCredentialHash string, err)` — 抽 vkey + 双校验。
- `crypto.StakeVkeyFromCOSEKey(coseKeyHex string) (ed25519.PublicKey, error)` — 解 COSE_Key 取 label -2 + 校验 kty/crv/len。
- `chain.StakeHashFromRewardAddress(addr string) (string, error)`（或 walletauth 内）— 解 reward 地址→28B hash hex，兼容 raw/CBOR/bech32。

### 2.3 授权页 / 绑定页
- `GET /connect`：Go `html/template` 渲染完整页（client 信息 + 钱包选择 UI）；内嵌 vanilla JS：列 `window.cardano.*` → `enable()` → `getRewardAddresses()[0]` → `POST /api/auth/challenge {stake_address}` → `signData(rewardAddr, hex(nonce))` → `POST /api/connect/authorize {cose_key, signature, ...}` → 跟随 302。资源 `embed` 进二进制。
- 渠道绑定页：同结构，purpose=activation，展示 deep link/二维码。

### 2.4 Risk and rollback
- R1 **钱包 signData/地址/COSE 互操作**（最高，自实现 CIP-8 + 多钱包）→ go-cose 交叉验证(已有 p14-7 思路) + 真钱包 golden vector(向量入测) + 手测矩阵(Nami/Eternl/Lace/Typhon…)。
- R2 改共享原语波及四流 → 全量 `go test` + e2e + 二进制 smoke，逐流验证。
- R3 攻击者构造 COSE_Key → C1 双校验 + C2 稳健解析 + 输入上限。
- Rollback：未发布按 working tree 修；已提交 `git revert`；forward-only。

## References
- docs/specs/completed/20260623T0041-S0001-poolops-issuer-backend.md — 后端基线（walletauth/cose/handlers 现状）
- docs/specs/draft/S0002-ouropass-web-frontend.md — 依赖本 spec 的 Admin 前端
- CIP-30（DataSignature{signature, key=COSE_Key}）/ CIP-8 / CIP-19（reward 地址）/ RFC 8152（COSE_Key 标签）
- server/internal/core/walletauth/walletauth.go、utils/crypto/cose.go、utils/chain/stakeaddr.go、httpapi/handlers_{oauth,wallet,activation,admin}.go

## 3. Execution Plan
- [x] p1-1 `crypto.StakeVkeyFromCOSEKey`（解 COSE_Key 取 label -2 + 校验 kty/crv/len/上限）+ reward 地址→28B hash（复用 stakeaddr，兼容 raw/CBOR/bech32）；单测含 go-cose 产出的 COSE_Key 向量（TC-1）。
- [x] p1-2 `walletauth.Challenge/Verify` 改造（绑 hash / 抽 vkey + 双校验）+ 单测：合法、坏 COSE_Key、hash 不匹配、签名不符、payload 编码（TC-2）。
- [x] p1-3 四条流 handler 契约同步（connect/authorize、activation/create、admin challenge+verify、step-up）+ handler/e2e 测试更新（S0001 既有用 `stake_vkey` 的测试改为 COSE_Key/地址）（TC-3）。
- [ ] p2-1 授权页：`GET /connect` 渲染完整 HTML 模板 + 内嵌 vanilla JS（连钱包/sign/转发）+ `embed`；replace 占位（TC-4）。
- [ ] p2-2 渠道绑定页 HTML + vanilla JS（activation）+ deep link/二维码（TC-5）。
- [ ] p3-1 全量 `go test ./...` + 二进制 smoke + 真钱包手测矩阵（R1 标注；向量入自动化测试）（TC-6）。

## 4. Test and Acceptance Criteria
- TC-1 `StakeVkeyFromCOSEKey`：go-cose 产出的 COSE_Key 抽出正确 32B vkey；坏 kty/crv/len/截断 CBOR → error；reward 地址(raw/CBOR/bech32 三形态)→ 同一 28B hash。
- TC-2 walletauth：Challenge 绑 hash；Verify 合法→返回 hash；坏 COSE_Key / hash 不匹配 / 签名不符 / 过期/重放/purpose 各对应错误；payload 必须 `hex(utf8(nonce))`。
- TC-3 四流：authorize/activation/admin-login/step-up 用新契约跑通（含否定：不合格、越权、错 key）；S0001 既有测试改造后全绿。
- TC-4 授权页：`GET /connect` 返回完整 HTML（含 client 信息 + 钱包 UI），非占位；mock/手测：连钱包→拿 code→302 回跳。
- TC-5 绑定页：activation 流程出 deep link/二维码。
- TC-6 全量 `go test ./...`（含 e2e/集成）绿 + 二进制 smoke + ≥2 真钱包手测通过（Nami/Eternl/Lace 任二）。
- Pass/fail：每 item 仅在其映射 TC 全 pass + 证据 append 后标 `[x]`。

## 5. Execution Log (append-only)
- 2026-06-24 S0003 草案创建（draft）：源于 S0002 评审——授权页应是 issuer 服务的 HTML（设计文档 §9.4 证实），且 CIP-30 流程冲突需改 walletauth（challenge 绑 hash / verify 收 COSE_Key 抽 vkey）。尚未执行。
- 2026-06-24 S0003 激活（active）：draft→active，Start Time 记录，重命名加执行时间戳前缀。
- 2026-06-24 p1-1 完成：新增 `crypto/cosekey.go`（`StakeVkeyFromCOSEKey`：map 解 COSE_Key、强制 kty=OKP/crv=Ed25519、x len==32、输入上限 512B）+ `chain/rewardaddr.go`（`StakeHashFromRewardAddress` + `bech32Decode` + 无依赖 `unwrapCBORBytes`，兼容 bech32/raw-hex/CBOR-bstr 三形态）。测试 `cosekey_test.go`/`rewardaddr_test.go`：go-cose 独立产出的 COSE_Key 被正确抽取、label 乱序+额外 kid 容忍、坏 kty/crv/len/截断/非 map 全拒；三形态地址→同一 28B hash、坏校验和/混合大小写/错 header/错长度全拒。`cosekey.go` 生产代码不引 go-cose（仅测试引用）。
- 2026-06-24 p1-2 完成：`walletauth.Challenge(stakeAddress)`（解 reward 地址→hash 绑 nonce）+ `Verify(coseKeyHex,…)`（抽 vkey + 双校验：`cose.Verify` + `blake2b224(vkey)==绑定 hash`），删除旧 `decodeVkey`。`walletauth_test.go` 重写为新契约 + 新增 payload 不符（`ErrCOSEPayload`）、坏 COSE_Key、坏 reward 地址三用例，7 用例全绿。说明（exception）：本契约改动跨 p1-2/p1-3 lockstep —— 本提交仅含 `walletauth` 包（自洽、单测绿），handlers/core 各调用方在 p1-3 同步后全仓 `go build ./...` 恢复绿。
- 2026-06-24 p1-3 完成：四条流契约同步。线上字段：`/api/auth/challenge` `stake_vkey`→`stake_address`；`/connect/authorize`、`/activation/create`、admin `auth/verify`、step-up `owner_vkey`/`stake_vkey`→`cose_key`；admin `auth/challenge` `owner_vkey`→`owner_stake_address`。core 调用方 `oauth.AuthorizeRequest.CoseKey`、`oauth.CreateActivation(coseKey)`、`admin.Challenge/Verify/VerifyStepUp/ChallengeStepUp` 全部改名同步。测试侧（oauth/token/activation/admin core + httpapi admin/wallet/oauth + e2e）统一引入 `rewardAddrOf`/`coseKeyOf`（或 harness/wallet 字段）派生 reward 地址 + COSE_Key。全仓 `go build ./...`、`go vet ./...`、`go test ./...`（含 e2e）恢复绿（0 FAIL），lockstep 闭合。

## 6. Validation Evidence (append-only)
- 2026-06-24 TC-1 | stack: go | command: `go test -count=1 -v -run 'StakeVkeyFromCOSEKey\|StakeHashFromRewardAddress' ./internal/utils/crypto/ ./internal/utils/chain/` | result: pass | note: 5 用例全绿——COSE_Key go-cose 互操作抽取、乱序+extras、拒坏输入；reward 地址 5 形态（bech32/raw/CBOR-bstr/大写/空白）→同一 hash、6 类坏输入全拒。
- 2026-06-24 TC-2 | stack: go | command: `go test -count=1 -v ./internal/core/walletauth/` | result: pass | note: 7 用例全绿——往返+credential hash、replay→ErrConsumed、错 key（hash 不匹配）、篡改签名、payload 不符→ErrCOSEPayload、坏 COSE_Key、坏 reward 地址、错 purpose→ErrPurpose、过期 GC。
- 2026-06-24 TC-3 | stack: go | command: `go build ./...` + `go vet ./...` + `go test -count=1 ./...`（含 e2e、无 DSN） | result: pass | note: 四条流契约同步后全仓 0 FAIL；core/oauth、core/admin、httpapi、e2e 各包绿，覆盖 authorize/activation/admin-login/step-up 新契约 + 既有否定用例（不合格、越权、错 key、错 purpose、篡改）。

## 7. Change Requests (append-only)
- 2026-06-24 决策：把 COSE_Key→vkey 的 CBOR 解码 + reward 地址解析**全部放后端**（消除浏览器手搓 CBOR 的"粗糙"），授权页/绑定页改为 **issuer 服务的 HTML 模板 + vanilla JS（零前端构建）**；walletauth 契约从"裸 vkey"改为"challenge 绑 hash + verify 收 COSE_Key"，四条流一致。安全不变量：抽 vkey 后必须验签名 + 比 hash（vkey 非秘密）。
