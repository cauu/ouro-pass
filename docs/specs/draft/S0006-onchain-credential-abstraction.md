# Ouro Pass 链上身份凭证抽象（多凭证 / 多池 / NFT-ready）

Spec-ID: S0006
Status: draft
Created Time: 2026-06-26T13:05:00+08:00
Start Time:
Completion Time:
Previous Spec-ID:
Closure Reason:

## 1. Requirement Details

### Background
S0004 把 issuer 定位为"**相对单个池的质押身份证明提供方**"：硬绑 `OUROPASS_POOL_ID`，token 携带扁平的单池质押 claims（`pool_membership_state`/`active_stake_lovelace`/…），第一方 tier 由 `PoolConfig.tier_rules` 对**该池质押**求值。

产品方向升级：用户身份有效性可能基于**多个池**的质押,未来还要扩展到 **NFT 持有**等。这些本质都是"**某种链上身份凭证**(on-chain identity credential)"。本 spec 把"相对某池的质押"抽象为可插拔的 **Attestor**：subject(钱包 stake credential)不变,**凭证种类**变成插件;issuer 配置一组 attestor;token 携带 subject 在这组上的**聚合事实**;第一方 tier 由全局 `tier_rules` 对**聚合事实**求值(决定"是否订阅者 + tier")。

配置全部下沉后端,**去掉 `OUROPASS_POOL_ID`** 环境变量。

> 本期运行时只实现 **`pool_stake`**(含多池)；**NFT 仅留接口/配置 kind,不实现**(future)。不改 S0004 的链数据采集(Koios)与三态机制本身,而是把"单池"泛化为"多 attestor"。

### Scope
- **Attestor 抽象**：接口 + 注册表(`Kind`→evaluator)；`pool_stake` 求值器(复用 S0004 `membership.DeriveState`/facts),支持多池。
- **AttestorConfig 模型**：泛化"被证明对象",取代单 `PoolConfig` 的池身份；迁移现有单池为一条 `pool_stake` attestor。
- **部署单例解耦**：把 `network` / **issuer 自身身份(`iss`)** 与 attestor 分开；去掉 `OUROPASS_POOL_ID`，加 `OUROPASS_ISSUER`(部署级)。
- **token 形状**：`credentials` 自描述数组(per-attestor 事实);定向后兼容/迁移路径(`iss` 形状一并变)。
- **tier_rules 泛化**：全局、对**聚合事实**求值;角色 = 第一方渠道的**订阅判定 + tier**(无匹配=非订阅者/无 tier)；规则语法从"质押三字段"升级为"对一组命名事实的有序条件"。
- **薄闸泛化**：subject **持有任一 active attestor** → 发 token(ANY，可配)；零持有 → access_denied。
- **新鲜度/缓存**：每 attestor 自声明新鲜度;聚合每次签发重算;短 access TTL 兜底;`CachedSource` 泛化(质押仍 epoch 缓,异构源各自策略)。
- **admin API + UI**：attestor 配置 CRUD + 全局 tier_rules 编辑(扩展 S0004 Tiers 页)。

### Constraints
- C1 **subject 不变**：身份主体始终是钱包 stake credential;NFT 等也按该 stake account 下的链上事实求值。
- C2 **不破坏 S0004 核心**：链数据采集、三态、缓存只缓 active(质押)保留;只是把"单池"提升为"多 attestor"。
- C3 **token 向后兼容策略待定**(见 open decision)：若已有外部 RP 消费扁平 claims,需"加 `credentials` 数组 + 旧字段保留过渡期";若无,直接换数组。
- C4 **冷启动可引导**：零 attestor 时签发返回非资格,但 admin(owner-key env)仍可登录配置。
- C5 **NFT 不实现**：仅接口 + 配置 kind 预留 + 主体范围约定。

### Non-goals
- NFT/其它平台 attestor 的具体实现(本期只 `pool_stake`)。
- 跨 network(多链)——本期 network 仍部署单例(见 open decision，若未来跨链再下放到 attestor)。
- 渠道多实例(S0005 负责)；本 spec 只改"谁算订阅者/tier",投递归 S0005。
- 自建链上索引;改 token 签名/JWKS 机制。

## 2. Outline Design

### 2.1 决策表
- D1 **Attestor 接口**：`Attest(ctx, subject)→{Kind, Held bool, Facts}`(S0004 讨论已认可)。注册表按 `Kind` 把 `AttestorConfig` 实例化为 evaluator。
- D2 **AttestorConfig**(泛化"pool 对象")：`{AttestorID, Kind, Label, Params(json), Status}`；`pool_stake` 的 `Params={pool_id,ticker,name}`；`nft`(预留)`={policy_id}`。现池 → 一条 `pool_stake`。
- D3 **部署单例**：`network` + `iss`(issuer 身份)与 attestor 分离;`OUROPASS_POOL_ID` 删除,`OUROPASS_ISSUER` 新增。
- D4 **token = `credentials` 数组**：每条自描述 `{kind, …per-attestor facts}`；`iss` 改为部署级 issuer id(不再 `ouropass:<pool>`)。
- D5 **tier_rules 全局 + 聚合求值**(用户拍板)：对**跨 attestor 聚合事实**有序求值,首匹配→tier,无匹配→非订阅者。规则语法升级为"对命名事实(如 `total_active_stake`、`nft:<policy>.count`)的条件组"。
- D6 **薄闸 = ANY**(可配)：持有任一 active attestor 即发 token;策略组合仍下沉 RP。
- D7 **新鲜度分层**：attestor 声明 freshness;`pool_stake` 沿用 epoch 稳定 + 只缓 active;异构源(NFT)各自 TTL/不缓;**聚合在签发时重算**,短 access TTL。
- D8 **迁移**：现 `PoolConfig` 拆解——池身份→`pool_stake` AttestorConfig 的 `Params`;`tier_rules`→全局 issuer 级 tier 配置;`network`→部署级保留。

### 2.2 数据模型变更
- 新 `AttestorConfig` 表：`attestor_id PK, kind, label, params TEXT(json), status, created_at, updated_at`，`UNIQUE(kind, label)` 或按需。
- `tier_rules` 迁出 `PoolConfig`：进一个 issuer 级单例(新 `IssuerConfig` 行 / 专表),因为它现在是**全局对聚合求值**。
- `PoolConfig` 池身份(ticker/name)→ 迁入对应 `pool_stake` attestor 的 `Params`;迁移后 `PoolConfig` 表可退役或仅留 network(或 network 也移部署级)。
- 迁移脚本:现有单池 → 一条 `pool_stake` AttestorConfig(`Params` 含 pool_id/ticker/name)+ 现 `tier_rules` → 全局 tier 配置。

### 2.3 token 形状（§2.5 of S0004 的泛化）
```jsonc
{
  "iss": "<deployment issuer id>",          // 不再 ouropass:<pool>
  "sub": "<pseudonym>", "aud": "...", "exp": ...,
  "credentials": [
    {"kind":"pool_stake","pool":"pool1…","state":"active","active_stake_lovelace":"…","epochs_active":17,"member_since":"…"},
    {"kind":"pool_stake","pool":"pool2…","state":"pending"}
    // 未来: {"kind":"nft","policy":"…","count":3}
  ],
  "tier": "gold"                            // 可选第一方意见(全局 tier_rules over 聚合)
}
```
RP 读其关心的 `credentials` 条目;`tier` 仍仅第一方渠道用。

### 2.4 tier_rules 泛化语法（草图，执行期细化）
有序规则,首匹配胜;每条 = tier + 对**聚合命名事实**的条件组:
```jsonc
[
  {"tier":"gold",   "when":{"total_active_stake_min":"1000000000000"}},
  {"tier":"silver", "when":{"total_active_stake_min":"100000000000"}},
  {"tier":"basic",  "when":{"any_active":true}}
  // future: {"tier":"vip","when":{"nft:<policy>.count_min":1}}
]
```
无任一匹配 → 非订阅者(无 tier)。聚合事实由 attestor 集合产出(如 `total_active_stake`=跨池 active 之和、`any_active`=任一 attestor Held)。

### 2.5 新鲜度 / 缓存
- 每 attestor `Freshness()`:`pool_stake`=epoch 稳定(只缓 active,沿用 S0004 `CachedSource` 逻辑);异构源各自 TTL/不缓。
- **聚合事实每次签发重算**(遍历 attestor),不缓聚合;靠短 access TTL + refresh 重派生限制陈旧(C4 of S0004)。
- `CachedSource` 从"质押专用"泛化为"每凭证类型自带缓存策略"。

### 2.6 配置 / 引导
- 删 `OUROPASS_POOL_ID`;加 `OUROPASS_ISSUER`(iss);`network` 暂留部署级 env。
- attestor 集合 + 全局 tier_rules **全走后端配置(admin API/UI 或迁移 seed)**。
- 冷启动:零 attestor → 签发 not-eligible;admin(owner-key env)可登录配置后即生效。

### 2.7 Risk and rollback
- R1 token/iss 破坏性变更 → 按 open decision 走"数组+旧字段过渡"或直接换;e2e + 文档明确迁移。
- R2 异构新鲜度聚合陈旧 → 短 TTL + 签发重算 + per-attestor freshness;质押路径回归不变。
- R3 tier 语法泛化引入复杂度 → 限定"命名事实 + 有序条件",不做图灵完备规则语言;充分单测。
- R4 迁移破坏现有单池 → 迁移脚本幂等 + 迁移前后"现有质押者仍可发 token/拿 tier"验证。
- R5 冷启动误判 → 明确未配置态 + admin 引导;smoke 覆盖。
- Rollback：forward-only;DB 迁移配回滚迁移;`git revert`。

## References
- docs/specs/20260626T0015-S0004-staking-attestation.md — 单池证明基线(本 spec 泛化其 §2.2/§2.5/§2.6)
- docs/specs/draft/S0005-multi-channel-instances.md — 渠道多实例(消费"订阅者/tier",需协调次序)
- server/internal/core/membership/（DeriveState/TierFor/CachedSource）、core/oauth/（attest/Membership/claims）、utils/jose/（AccessClaims）、utils/chain/、config/、store/repo_poolconfig.go

## 3. Execution Plan
- [ ] p1-1 Attestor 接口 + 注册表 + `pool_stake` evaluator（包 S0004 membership/facts），多池;纯函数/单测。
- [ ] p1-2 `AttestorConfig` 模型 + 迁移：现池→`pool_stake` attestor;`tier_rules`→全局 issuer 配置;repo CRUD;单测。
- [ ] p2-1 多 attestor 求值 + 聚合事实 + 薄闸 ANY;替换 oauth/reconciler 里的单 `PoolID` 路径。
- [ ] p2-2 token claims→`credentials` 数组 + `iss` 解耦(部署级 issuer id);e2e;RP 迁移说明。
- [ ] p3-1 tier_rules 泛化语法(over 聚合)+ 订阅/渠道 tier 接入;单测(多池/边界)。
- [ ] p4-1 配置：删 `OUROPASS_POOL_ID` + 加 `OUROPASS_ISSUER`;冷启动未配置态;迁移既有部署。
- [ ] p5-1 新鲜度/缓存泛化：`CachedSource` per-attestor 策略;质押路径回归。
- [ ] p6-1 admin API + UI：attestor CRUD + 全局 tier_rules 编辑(扩展 Tiers 页);RBAC + 审计。
- [ ] p7-1 全量 `go test ./...` + `pnpm build/lint` + 二进制 smoke + 文档(凭证模型/token/tier/迁移)。

## 4. Test and Acceptance Criteria
- TC-1 Attestor：`pool_stake` evaluator 对多池各自产出 Held/facts;注册表按 kind 实例化。
- TC-2 配置/迁移：现单池迁为一条 `pool_stake` attestor + 全局 tier_rules;迁移前后现有质押者仍可发 token + 同 tier。
- TC-3 聚合 + 薄闸：多池聚合事实正确;持任一→发 token,零持→拒。
- TC-4 token：`credentials` 数组形状正确;`iss`=部署 issuer id;refresh 重派生。
- TC-5 tier_rules：全局规则 over 聚合(总 stake/any_active)首匹配;无匹配=非订阅者;多池场景覆盖。
- TC-6 配置：去 `OUROPASS_POOL_ID` 后启动正常;零 attestor 冷启动 admin 可配置后生效。
- TC-7 缓存：质押仍 epoch 缓(回归);聚合签发重算不陈旧越界。
- TC-8 admin API/UI：attestor CRUD + tier_rules 编辑;RBAC;审计。
- Pass/fail：每 item 仅在映射 TC 全 pass + 证据 append 后标 `[x]`。

## 5. Execution Log (append-only)
- 2026-06-26 S0006 草案创建（draft）：承接 S0004→把"单池质押证明"泛化为"链上身份凭证(Attestor)"抽象;多池本期实现,NFT 留接口;tier_rules 全局 over 聚合;去 `OUROPASS_POOL_ID` 全走后端配置;`iss`/token 形状随之变更。尚未执行。

## 6. Validation Evidence (append-only)

## 7. Change Requests (append-only)
- 2026-06-26 初始决策（草案，用户已认可主线）：① subject 不变=钱包 stake credential;② pool 降格为 `AttestorConfig` 的一个 `Kind`(pool_stake),多池=多条,NFT 预留;③ token=`credentials` 自描述数组;④ tier_rules 全局、对**聚合事实**求值(订阅判定+tier);⑤ 薄闸=持任一 attestor(ANY,可配);⑥ 去 `OUROPASS_POOL_ID`,全走后端配置,加部署级 `OUROPASS_ISSUER`(`iss` 来源)。
- 2026-06-26 **待用户拍板的 open decisions**：(a) **token 向后兼容**——现是否已有外部 RP 消费扁平 claims?有→"加数组+保留旧字段过渡期";无→直接换数组(更干净)。(b) **`iss` 具体取值**(部署 issuer id 命名/来源)。(c) **tier_rules 聚合语法表达力边界**(命名事实 + 有序条件,刻意不做图灵完备)。(d) **network 是否下放 attestor**(若未来跨链)——本期暂留部署单例。(e) **次序**:S0006 建议先于/协调 S0005(改"订阅者/tier"定义,且 token 越早改越省 RP 迁移)。
