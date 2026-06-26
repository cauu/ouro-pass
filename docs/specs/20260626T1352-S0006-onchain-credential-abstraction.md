# Ouro Pass 链上身份凭证抽象（多凭证 / 多池 / NFT-ready）

Spec-ID: S0006
Status: active
Created Time: 2026-06-26T13:05:00+08:00
Start Time: 2026-06-26T13:52:00+08:00
Completion Time:
Previous Spec-ID: S0004
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
- **身份/网络解耦**：issuer 自身身份(`iss`)= 公开 base URL(`OUROPASS_ISSUER`),与 attestor 分开;去掉 `OUROPASS_POOL_ID`;**`network` 下放到每个 attestor**(跨链 ready),`chain.Source` 按 network 构建。
- **token 形状**：`credentials` 自描述数组(per-attestor 事实);`iss` 改 base URL。**未上线,无兼容包袱——直接换、删扁平 claims**。
- **tier_rules 泛化**：全局、对**聚合事实**求值;角色 = 第一方渠道的**订阅判定 + tier**(无匹配=非订阅者/无 tier)；规则语法从"质押三字段"升级为"对一组命名事实的有序条件"。
- **薄闸泛化**：subject **持有任一 active attestor** → 发 token(ANY，可配)；零持有 → access_denied。
- **新鲜度/缓存**：每 attestor 自声明新鲜度;聚合每次签发重算;短 access TTL 兜底;`CachedSource` 泛化(质押仍 epoch 缓,异构源各自策略)。
- **admin API + UI**：attestor 配置 CRUD + 全局 tier_rules 编辑(扩展 S0004 Tiers 页)。

### Constraints
- C1 **subject 不变**：身份主体始终是钱包 stake credential;NFT 等也按该 stake account 下的链上事实求值。
- C2 **不破坏 S0004 核心**：链数据采集、三态、缓存只缓 active(质押)保留;只是把"单池"提升为"多 attestor"。
- C3 **token 无兼容包袱**(用户确认未上线)：直接换 `credentials` 数组,删扁平 claims,不保留旧字段过渡。
- C4 **冷启动可引导**：零 attestor 时签发返回非资格,但 admin(owner-key env)仍可登录配置。
- C5 **NFT 不实现**：仅接口 + 配置 kind 预留 + 主体范围约定。

### Non-goals
- NFT/其它平台 attestor 的具体实现(本期只 `pool_stake`)。
- 跨 network 的**具体多链 attestor 实现**——本期把 `network` 下放到 attestor(跨链 ready),但只实现 `pool_stake`;真正同时跑多链的验证留待具体接入。
- 渠道多实例(S0005 负责)；本 spec 只改"谁算订阅者/tier",投递归 S0005。
- 自建链上索引;改 token 签名/JWKS 机制。

## 2. Outline Design

### 2.1 决策表
- D1 **Attestor 接口**：`Attest(ctx, subject)→{Kind, Held bool, Facts}`(S0004 讨论已认可)。注册表按 `Kind` 把 `AttestorConfig` 实例化为 evaluator。
- D2 **AttestorConfig**(泛化"pool 对象")：`{AttestorID, Kind, Label, Params(json), Status}`；`pool_stake` 的 `Params={pool_id,ticker,name}`；`nft`(预留)`={policy_id}`。现池 → 一条 `pool_stake`。
- D3 **iss = issuer 公开 base URL**(用户拍板)：`OUROPASS_ISSUER`(必填,如 `https://pass.mypool.io`);RP 从 `iss` 发现 JWKS(`<iss>/.well-known/ouropass/jwks.json`)验签。与"池"彻底解耦。
- D4 **network 下放 attestor**(用户拍板)：每个 `AttestorConfig.Params` 带 `network`;`chain.Source` 按 network 构建(源池,缓存)——跨链 ready。`OUROPASS_NETWORK` 仅作新建 attestor 的默认值。
- D5 **token = `credentials` 数组,无兼容包袱**(用户拍板:未上线)：每条自描述 `{kind, …per-attestor facts}`;**直接换数组、删扁平 claims**,不保留旧字段。
- D6 **tier_rules 全局 + 聚合求值 + 布尔 DSL**(用户拍板)：有序规则首匹配;每条 `{tier, when}`,`when` 为对**命名聚合事实**的布尔表达式——`all`(AND)/`any`(OR)/`not` + 原子 `{fact, op, value}`(op=`>= > == <= < !=`)。命名事实:`any_active`、`total_active_stake`、`pool:<id>.state`/`.active_stake`、future `nft:<policy>.count`。**非图灵完备**(无循环/算术/用户代码);无匹配=非订阅者。
- D7 **薄闸 = ANY**(可配)：持有任一 active attestor 即发 token;策略组合仍下沉 RP。
- D8 **新鲜度分层**：attestor 声明 freshness;`pool_stake` 沿用 epoch 稳定 + 只缓 active;异构源(NFT)各自 TTL/不缓;**聚合在签发时重算**,短 access TTL。
- D9 **迁移**：现 `PoolConfig` 拆解——池身份(+network)→`pool_stake` AttestorConfig 的 `Params`;`tier_rules`→全局 issuer 级 tier 配置(布尔 DSL 重写)；`PoolConfig` 表退役。

### 2.2 数据模型变更
- 新 `AttestorConfig` 表：`attestor_id PK, kind, label, params TEXT(json), status, created_at, updated_at`，`UNIQUE(kind, label)` 或按需。
- `tier_rules` 迁出 `PoolConfig`：进一个 issuer 级单例(新 `IssuerConfig` 行 / 专表),因为它现在是**全局对聚合求值**。
- `PoolConfig` 池身份(ticker/name)→ 迁入对应 `pool_stake` attestor 的 `Params`;迁移后 `PoolConfig` 表可退役或仅留 network(或 network 也移部署级)。
- 迁移脚本:现有单池 → 一条 `pool_stake` AttestorConfig(`Params` 含 pool_id/ticker/name)+ 现 `tier_rules` → 全局 tier 配置。

### 2.3 token 形状（§2.5 of S0004 的泛化；无兼容包袱，直接换）
```jsonc
{
  "iss": "https://pass.mypool.io",          // issuer 公开 base URL(OUROPASS_ISSUER);RP 从此发现 JWKS
  "sub": "<pseudonym>", "aud": "...", "exp": ...,
  "credentials": [
    {"kind":"pool_stake","pool":"pool1…","network":"mainnet","state":"active","active_stake_lovelace":"…","epochs_active":17,"member_since":"…"},
    {"kind":"pool_stake","pool":"pool2…","network":"mainnet","state":"pending"}
    // 未来: {"kind":"nft","policy":"…","count":3}
  ],
  "tier": "gold"                            // 可选第一方意见(全局 tier_rules over 聚合)
}
```
扁平 `pool_membership_state`/`active_stake_lovelace`/… **不再出现**(未上线,无需保留)。RP 读其关心的 `credentials` 条目;`tier` 仍仅第一方渠道用。

### 2.4 tier_rules 泛化语法（布尔 DSL，非图灵完备）
有序规则首匹配;每条 `{tier, when}`,`when` = 对**命名聚合事实**的布尔表达式:
- 组合子:`{"all":[…]}`(AND) / `{"any":[…]}`(OR) / `{"not":…}`;可嵌套。
- 原子:`{"fact":"<name>","op":">=|>|==|<=|<|!=","value":"<str>"}`(数值用 big.Int 比较,bool/str 用等值)。
```jsonc
[
  {"tier":"gold","when":{"fact":"total_active_stake","op":">=","value":"1000000000000"}},
  {"tier":"vip","when":{"all":[
      {"fact":"pool:poolA.state","op":"==","value":"active"},
      {"any":[
        {"fact":"total_active_stake","op":">=","value":"500000000000"},
        {"fact":"nft:policyX.count","op":">=","value":"1"}   // future
      ]}
  ]}},
  {"tier":"basic","when":{"fact":"any_active","op":"==","value":"true"}}
]
```
命名事实由 attestor 集合产出:`any_active`(任一 Held)、`total_active_stake`(跨池 active 和)、`pool:<id>.state`/`.active_stake_lovelace`/`.epochs_active`、future `nft:<policy>.count`。无任一匹配 → **非订阅者**(无 tier)。执行期定死事实命名表 + op 集合,不做循环/算术/用户代码。

### 2.5 新鲜度 / 缓存
- 每 attestor `Freshness()`:`pool_stake`=epoch 稳定(只缓 active,沿用 S0004 `CachedSource` 逻辑);异构源各自 TTL/不缓。
- **聚合事实每次签发重算**(遍历 attestor),不缓聚合;靠短 access TTL + refresh 重派生限制陈旧(C4 of S0004)。
- `CachedSource` 从"质押专用"泛化为"每凭证类型自带缓存策略"。

### 2.6 配置 / 引导
- **删 `OUROPASS_POOL_ID`**;加 **`OUROPASS_ISSUER`**(必填,issuer 公开 base URL,= token `iss`,RP 据此发现 JWKS)。
- **`network` 下放到 attestor**(`Params.network`);`chain.Source` 按 network 构建并缓存(源工厂从"单例"变"按 network 取");`OUROPASS_NETWORK` 仅作新建 attestor 的默认值,不再是全局口径。
- attestor 集合 + 全局 tier_rules **全走后端配置(admin API/UI;迁移期一次性 seed)**。
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
- [x] p1-1 Attestor 接口 + 注册表 + `pool_stake` evaluator（包 S0004 membership/facts），多池;纯函数/单测。
- [x] p1-2 `AttestorConfig` 模型 + 迁移：现池→`pool_stake` attestor;`tier_rules`→全局 issuer 配置;repo CRUD;单测。
- [x] p2-1 多 attestor 求值 + 聚合事实 + 薄闸 ANY;替换 oauth/reconciler 里的单 `PoolID` 路径。
- [x] p2-2 token claims→`credentials` 数组 + `iss` 解耦(部署级 issuer id);e2e;RP 迁移说明。
- [x] p3-1 tier_rules 泛化语法(over 聚合)+ 订阅/渠道 tier 接入;单测(多池/边界)。
- [x] p4-1 配置：删 `OUROPASS_POOL_ID` + 加 `OUROPASS_ISSUER`(=iss base URL);`network` 下放 attestor + `chain.Source` 按 network 工厂;冷启动未配置态;迁移既有部署。
- [x] p5-1 新鲜度/缓存泛化：`CachedSource` per-attestor 策略;质押路径回归。
- [x] p6-1 admin API + UI：attestor CRUD + 全局 tier_rules 编辑(扩展 Tiers 页);RBAC + 审计。
- [x] p7-1 全量 `go test ./...` + `pnpm build/lint` + 二进制 smoke + 文档(凭证模型/token/tier/迁移)。
- [x] p6-2 **（验收反馈）** Attestor 表单砍冗余字段:删 `Ticker`/`Display name`(PoolConfig 迁移残留、无任何消费),仅留 `Label`(实例唯一显示名)+ `pool_id` + `network`。
- [x] p6-3 **（验收 bug）** Attestor disable/delete 不生效:`attestor_id` 含 `:`(`kind:` 前缀 / 迁移 `pool_stake:`),前端 `encodeURIComponent`→`%3A`,chi 按 raw path 路由 → `{id}` 不匹配 → 静默 404。修:① 新 id 改纯 hex(`crypto.RandomID()`,URL-safe);② 迁移 `0012` id 改 `'ps-'||pool_id`(无冒号);③ update/delete handler 加 `url.PathUnescape` 兜底(旧冒号 id 经 `%3A` 也能匹配)。
- [x] p6-4 **（验收反馈）** Tiers 前端 UI 不支持新 DSL(原来只是裸 JSON 文本框):重写 `TiersPage` 为**结构化规则构建器**——fact 下拉(从已配置 attestor 自动列 `pool:<id>.*` + 静态 `any_active`/`any_held`/`total_active_stake`)、op 下拉、ALL/ANY 组合、叶条件增删、规则排序(首匹配优先级);嵌套 `not`/嵌套表达式落"JSON 高级模式"(双向切换)。
- [x] p6-5 **（验收反馈）** value 按 fact 类型渲染:`factType` 映射 → `any_active`/`any_held`=布尔(true/false 下拉)、`*.state`=枚举(active/pending/none 下拉)、`total_active_stake`/`*.active_stake_lovelace`/`*.epochs_active`=数字(numeric 输入);op 按类型收窄(布尔/枚举仅 `== !=`,数字 6 个);切 fact 时自动重置 value 默认值 + 校正非法 op。

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
- 2026-06-26 S0006 激活（draft→active），Previous-Spec S0004。
- 2026-06-26 p1-1 完成：新包 `server/internal/core/attestor`——`Attestor` 接口(`Kind/ID/Attest`)+ `Attestation{Kind,ID,Held,Claim,Facts}` + `Registry`(Kind→Builder,`DefaultRegistry` 仅注册 `pool_stake`,NFT 不注册=故意 C5)+ `PoolStakeAttestor`(包 `membership.DeriveState`,产出 token claim `{kind,pool,network,state,active_stake_lovelace,epochs_active,member_since}` + 命名聚合事实 `pool:<id>.<name>`)+ `SourceFor` 按 network 取源的接口缝(D4,真实工厂留 p4-1)。`Held = state != none`(active/pending 都算持有)。member_since 逻辑从 oauth 迁来、改为 per-pool。

## 6. Validation Evidence (append-only)
- 2026-06-26 p1-1 | stack: go | command: `go build ./... && go test ./internal/core/attestor/ && go vet ./internal/core/attestor/` | result: pass | note: TC-1。`TestPoolStake_MultiPool`(两 pool_stake attestor over 同 subjects,各自独立 active/pending/none + Held)、`TestPoolStake_ClaimAndFacts`(claim 携带 active_stake/epochs/member_since;命名事实 `pool:<id>.*`)、`TestRegistry`(default 注册 pool_stake;NFT 未注册报错;缺 pool_id 报错)全绿;全仓 build 绿、vet 净。

- 2026-06-26 p1-2 完成：新模型 `domain.AttestorConfig{AttestorID,Kind,Label,Params,Status}` + 迁移 `0012_attestor_config`(sqlite/postgres)——建 `AttestorConfig`(`UNIQUE(kind,label)`)+ `IssuerConfig`(单例,持全局 tier_rules);**backfill**:每条 `PoolConfig` → 一条 `pool_stake` attestor(pool_id/network/ticker/name 折进 params json),其 `tier_rules` 抬到 `IssuerConfig`。repo:`Attestors()` CRUD(Create/Get/List/ListActive/Update/SetStatus/Delete by id)+ `Issuer()` GetTierRules/SetTierRules(单例 upsert;无行返 `[]`)。**注**:`PoolConfig` 表暂留(旧 `oauth.firstPartyTier`/network 读路径在后续 item 切换),本 item 仅加新模型 + backfill,保证逐 commit 不破。
- 2026-06-26 p1-2 | stack: go | command: `go test ./internal/store/ && go vet ./internal/store/ ./internal/domain/` | result: pass | note: TC-2。`TestAttestorConfigRepo_CRUD`(增/查/列/改/启停/删 + `UNIQUE(kind,label)` 冲突 + 删后 NotFound)、`TestIssuerConfig_TierRules`(无行=`[]`、set→get、单例重 upsert 不增行)、`TestMigration_BackfillPoolToAttestor`(部分迁移到 0011 → 注入 PoolConfig → 跑 0012,断言 pool_stake attestor + params 含 pool_id/network/ticker、tier_rules 抬到 IssuerConfig)全绿;全 store 套件 + vet 净。

- 2026-06-26 p2-1 完成（多 attestor 求值 + 聚合 + ANY 闸 + oauth 切换）：① `attestor.Set`/`BuildSet`/`Evaluate`→`Result{Attestations,Held,Facts}`,聚合派生 `any_held`/`any_active`/`total_active_stake`(后两者从命名事实 `*.state`/`*.active_stake_lovelace` 汇总,kind 无关)。② 修 `poolstake` bug:active-stake 系事实(amount/epochs/since)仅在 `state==active`(=本池=ActiveStakePoolID)时产出,否则多池共享同一 snapshot 会把别池的 active stake 错记。③ oauth 切换:删 `classify`(单 `cfg.PoolID`+`cfg.Chain`),新 `evaluate`(黑名单→空 Result)+ `representativeState`(any_active→active / Held→pending / 否则 none)+ `primaryHeld`;`Membership`/`Attest`/Authorize 闸全走 ANY-of;`firstPartyTier` 改读 `IssuerConfig.GetTierRules`(脱离 PoolConfig)。reconciler/telegram 接口签名不变(消费 representative state),零改动。④ `oauth.Config` 加注入式 `Attestors func(ctx)(*Set,error)`;main.go 按 `Attestors().ListActive`→`BuildSet` 每调用解析(admin 改配置即时生效),`srcFor` 暂统一返回 chainSrc(per-network 工厂留 p4-1)。`Config.Chain/PoolID/Network` 暂留(p4-1 删)。**transitional**:flat token claims 仍由 `primaryHeld` 代表值产出(p2-2 换 `credentials[]`);`attestation.credentials/facts` 已备好待 p2-2/p3-1。
- 2026-06-26 p2-1 | stack: go | command: `go test ./... && go vet ./... && go build ./cmd/issuer` | result: pass | note: TC-1/TC-3。`attestor`:`TestSet_EvaluateAggregate`(多池聚合 any_active/total_active_stake/命名事实)、`TestSet_EvaluateNotHeld`(零持→Held=false/total=0)、`TestBuildSet`(配置建集 + 未知 kind 失败)。oauth/e2e/httpapi 全套件随接口切换更新(注入 `Attestors` + tier_rules 改 seed 到 `IssuerConfig`)后全绿;全仓 `go test ./...` 0 失败、二进制 build 绿。vet 唯一告警在 `handlers_admin_resources_test.go:111`(HEAD 既有、与本 item 无关)。

- 2026-06-26 p2-2 完成（token = credentials[] + iss 解耦）：`jose.AccessClaims` 删扁平 `MembershipState/ActiveStakeLovelace/EpochsActive/MemberSince`,改 `Credentials []map[string]any` → 发 `credentials` claim(每条自描述 `{kind,pool,network,state,active_stake_lovelace?,epochs_active?,member_since?}`)。仅 **held**(active/pending)凭证进数组(`claimsOf` 过滤;薄闸保证 ≥1)。`token.mint` 改填 `Credentials`;oauth `attestation` 删 `epochsActive/memberSince`(连同 `claimInt/claimTime` 辅助)。`iss` 注释定为部署 issuer 身份(非池派生);**注**:`OUROPASS_ISSUER` 改必填 + base-URL 约定在 p4-1 落（本 item 仅 token 层解耦)。introspect 只读 `tier`,不受影响。
- 2026-06-26 p2-2 | stack: go | command: `go test ./... && go build ./cmd/issuer` | result: pass | note: TC-4。`jose_test`(签发→JWKS 验签→断言 `credentials[0]` kind/state/active_stake + 扁平 `pool_membership_state` 已消失)、`token_test`(authcode 换 token→`credentials[0]` pool/state/active_stake/member_since + 扁平claim消失)更新后绿;e2e(introspect tier=gold)绿;全仓 0 失败、二进制 build 绿。
- 2026-06-26 **RP 迁移说明（无兼容包袱,未上线）**：access token 不再带扁平 `pool_membership_state`/`active_stake_lovelace`/`epochs_active`/`member_since`;改为 `credentials` 数组——RP 遍历找 `kind=="pool_stake"` 且关心的 `pool`,读其 `state`/`active_stake_lovelace`/`epochs_active`/`member_since`。`tier`(第一方,可选)与 `iss`/`sub`/`aud`/`exp` 不变。`iss` 现为部署 issuer 身份(p4-1 起 = 公开 base URL,RP 从 `iss` 发现 JWKS)。

- 2026-06-26 p3-1 完成（tier_rules 布尔 DSL over 聚合 + 接入）：新包 `internal/core/tier`——`Rule{Tier,When}` + `Condition{All/Any/Not/Fact,Op,Value}` 递归布尔 DSL;`Eval(rules, facts) string`(有序首匹配,无匹配=""=非订阅者)、`Validate`(每条需 tier、condition 恰一形态、op∈`== != >= > <= <`、op/value 须配 fact)。数值 op 用 big.Int(缺失数值事实=0),`==`/`!=` 字符串等值;空 `when`=catch-all。**接入**:oauth `firstPartyTier(ctx, facts)` 改 `tier.Eval(IssuerConfig.GetTierRules, res.Facts)`——删 `attestation.activeStake`/`primaryHeld`/`claimStr`(不再需代表值);telegram/订阅 tier 经 `Attest` 自动走新 DSL。admin `/pool/tier-rules` 切到 `IssuerConfig` + `tier.Validate`(读写均 issuer-global,脱离 PoolConfig)。删旧 `membership/tier.go`(`TierFor`/`ValidateTierRules`/`TierRule`,已无引用)。
- 2026-06-26 p3-1 | stack: go | command: `go test ./... && go vet ./internal/core/tier/ ./internal/core/oauth/ && go build ./cmd/issuer` | result: pass | note: TC-5。`tier`:`TestEval_ThresholdsFirstMatch`(gold/silver/basic 阈值 + 边界 + 非 active=无 tier + 空事实)、`TestEval_BooleanCombinators`(all/any/not 嵌套,OR 两分支 vip,not 落 member)、`TestValidate`(缺 tier/坏 op/op 无 fact/双形态/非数组 全拒)。oauth/e2e/httpapi tier seed 改新 DSL 后绿;admin tier-rules 端点测试改布尔 DSL(坏 op→400、gold/basic 持久化)绿;全仓 0 失败、vet 净、二进制 build 绿。

- 2026-06-26 p4-1 完成（去 POOL_ID + ISSUER 必填 + per-network 源工厂 + 冷启动）：① config 删 `OUROPASS_POOL_ID`,`OUROPASS_ISSUER` 改**必填**(无池派生默认,= iss/issuer 身份,base-URL 约定);新增 `cfg.Scope`(= Issuer)= 第一方订阅/渠道/admin 命名空间(替代 pool 作用域)。② main.go 用 `cfg.Scope` 接 telegram/reconciler/admin/Deps;**per-network 源工厂** `srcFor(network)`(mutex 守 map,按 network 建/缓 raw `chain.Source`);默认网络源给 reconciler epoch tick + 委托 roster。③ oauth.Config 删尽 legacy `Chain/PoolID/Network`(分类早已不读)。④ admin `adminDelegators` 改用 `primaryPoolID`(从首个 active `pool_stake` attestor 解析真实 pool_id,而非 scope);adminGetPool/tier 走 IssuerConfig。**决策**(标准指令落盘):订阅作用域 = issuer 派生(单部署单命名空间);委托 roster 的真实 pool 从 attestor 配置解析(多池/多网络选择留 p6-1);**主动缓存(CachedSource)在 p4-1 暂移除、p5-1 按 attestor 泛化重引入**(本 item 用 raw 源,正确性不变)。冷启动:零 attestor → BuildSet 空 → 不持有 → 拒签发;admin(owner-key env)可登录配置,attestorsFor 每调用从 DB 解析即时生效。
- 2026-06-26 p4-1 | stack: go | command: `go test ./... && go build ./cmd/issuer` + 二进制冷启动 smoke | result: pass | note: TC-6。config_test(ISSUER 必填、缺则 err、Scope=Issuer)、main_test(buildServices 全/降级,源名=`mock`)更新绿;attestor `TestSet_Empty`(零 attestor→Held=false/any_active=false/total=0=冷启动)新增绿;admin 委托测试 seed pool_stake attestor 后绿;全仓 0 失败。**二进制 smoke**:仅 `OUROPASS_ISSUER`(无 POOL_ID)+ mock + 零 attestor 启动 → `issuer listening`、`/healthz`=200、`/.well-known/ouropass/jwks.json`=200。

- 2026-06-26 p5-1 完成（缓存泛化:pool-agnostic + network-scoped）：重引入主动成员缓存(p4-1 暂移除)。`CachedSource` 去 `poolID`,改**pool-agnostic**:缓存判据 `snap.ActiveStakePoolID != ""`(active somewhere=epoch 稳定),`delegated_pool_id` 存**真实** active pool;同 network 所有 pool_stake attestor 共享一份 snapshot,各自 `DeriveState` 求 per-pool 状态。**network-scoped**:`StakeSnapshotCache` 加 `network` 列、复合主键 `(stake_credential_hash, network)`(迁移 `0013` 因缓存可重建,直接 DROP+CREATE);repo Get/Upsert/Delete 按 (sch, network) 定址。main.go `srcFor` 每 network 的 raw 源外包一层 `NewCachedSource`。**决策/已知边界**:已 active 的凭证在两池间迁移时,目的池的 pending 延迟到 epoch 边界才识别(active 本就 epoch 稳定);**onboarding(none→pending)仍即时**(active-nowhere 不缓存/删除),故入场对称性不变。
- 2026-06-26 p5-1 | stack: go | command: `go test ./... && go build ./cmd/issuer` | result: pass | note: TC-7。`membership` 缓存测试更新:`ActiveHitsAndEpochRollover`(命中不再取源、epoch 翻转重取)、`PendingNeverCached`(active-nowhere 每次现算)、`PoolAgnosticUpdateAndBail`(迁池→缓存更新为新 active pool、us 见 none/other 见 active;active-nowhere→删行)全绿;store 全套(含新复合键迁移)+ main_test(源名=`mock+cache`)绿;全仓 0 失败、二进制 build 绿。

- 2026-06-26 p6-1 完成（admin API + UI:attestor CRUD + tier 编辑）：**后端** `handlers_admin_attestors.go`——`GET /attestors`(viewer 列表,params 无密直返)、`POST /attestors`(operator 建,校验 kind+params、`pool_id` 必填、network 白名单、重复 (kind,label)→409、未支持 kind→400)、`POST /attestors/{id}`(operator 改 label/params/status,kind 不可变,坏 status→400)、`DELETE /attestors/{id}`(operator 删,缺→404);全部 `audit`(attestor.create/update/delete)。`adminGetPool`/`adminSetTierRules` 已于 p3-1 切到 IssuerConfig + tier DSL。**前端**:新 `AttestorsPage`(列表 + 加 pool_stake 表单[label/pool_id/network/ticker/name] + 启停 + 删,React Query 失效 `["attestors"]`)、路由 `/attestors`(operator)、侧栏导航项;`api/client.ts` 加 `del`;`api/admin.ts` 加 attestor CRUD;`lib/types.ts` 加 `TierCondition`/`Attestor`;`TiersPage` 改新布尔 DSL(SAMPLE/表格渲染 `when`/说明)。
- 2026-06-26 p6-1 | stack: go+ui | command: `go test ./internal/httpapi/ ./...` + `pnpm build` + `pnpm lint` | result: pass | note: TC-8。`TestAdminAttestors_CRUD`(seed 1 + 建/列/重复 409/nft 400/缺 pool_id 400/改 status disabled/坏 status 400/删 200/再删 404)、`TestAdminAttestors_RBAC`(viewer GET 200、create/delete 403)绿;全仓 `go test ./...` 0 失败;前端 `tsc -b && vite build` 绿(JS 412KB/gzip 133KB)、`pnpm lint` 0 error(2 既有 warning,非本次文件)。

- 2026-06-26 p7-1 完成（全量验证 + 文档）：文档新增 `docs/onchain-credentials.md`(凭证模型/token 形状/tier DSL/配置迁移/admin API)+ `docs/staking-attestation.md` 顶部加 S0006 承接说明;`server/Makefile` dev 目标 `OUROPASS_POOL_ID`→`OUROPASS_ISSUER`(+ DEV_ISSUER var)。顺带修一处既有 vet 告警(`handlers_admin_resources_test.go` channels GET 未查 err)使 vet 全净。
- 2026-06-26 p7-1 | stack: go+ui | command: `go test ./... && go vet ./... && go build ./cmd/issuer` + `pnpm build && pnpm lint` + 二进制 smoke | result: pass | note: 全量收口。`go test ./...` 19 包全 ok / 0 fail;`go vet ./...` **全净**(无任何告警);`go build ./...` + 二进制绿;前端 `tsc -b && vite build` 绿、`pnpm lint` 0 error。**二进制 smoke**(全新 DB):仅 `OUROPASS_ISSUER`(无 POOL_ID)启动 → 13 个迁移全应用(末 `0013`)、`AttestorConfig`/`IssuerConfig`/`StakeSnapshotCache` 建表、`/healthz`+`/.well-known/ouropass/jwks.json`=200。`make -n dev` 解析通过。

- 2026-06-26 p6-2 完成（验收反馈:砍冗余字段）：用户指出 Attestor 表单 Label/Ticker/Display name 冗余。核查确认 `Ticker`/`Name` 是 PoolConfig 迁移残留、**无任何消费**(不进 token claim、不进 tier 事实、UI 不显示)。删 `PoolStakeParams.Ticker`/`.Name`(后端)+ 表单 ticker/name 字段(前端),仅留 `Label`(实例唯一显示名,backs `UNIQUE(kind,label)`)+ `pool_id` + `network`;Label 提示改"This attestor's display name (unique)"。迁移 `0012` 仍写 ticker/name 进 params(已应用、无害——unmarshal 忽略未知键),不改历史。`make web` 已重新 stage 嵌入 SPA。
- 2026-06-26 p6-2 | stack: go+ui | command: `go test ./... && pnpm build && pnpm lint` + `make web` | result: pass | note: 全仓 `go test ./...` 0 失败(attestor/store/httpapi 套件含迁移 backfill 测试均绿——backfill 用 map 读 params,不依赖 struct 字段);前端 build 绿、lint 0 error;嵌入 bundle 仅余 Label 字段(`Ticker (optional)`/`Display name` 已无)。

- 2026-06-26 p6-3 完成（验收 bug:disable/delete 不生效）：根因 = `attestor_id` 的 `:` 经前端 `encodeURIComponent`→`%3A`,chi 按 raw path 路由使 `{id}` 捕获到编码态、与库内 id 不匹配 → update/delete 静默 404。修三处:create 用纯 hex id;迁移 `0012`(sqlite+postgres)id 改 `'ps-'||pool_id`;handler 新增 `attestorIDParam`(`url.PathUnescape` 兜底,旧冒号 id 也可删)。`make web` 重新 stage 嵌入 SPA。
- 2026-06-26 p6-3 | stack: go | command: `go test ./... && go vet ./...` + `make web` | result: pass | note: `TestAdminAttestors_CRUD`(新增断言:返回 id 不含 URL 保留字符)、`TestAdminAttestors_LegacyColonID`(直插 `pool_stake:legacyabc` 旧 id,经 `%3A` 编码路径 DELETE→200)、`TestAdminAttestors_RBAC` 全绿;全仓 `go test ./...` 0 失败、`go vet ./...` 全净;UI 已重 stage + 二进制重 build。

- 2026-06-26 p6-4 完成（Tiers 结构化构建器）：`TiersPage` 重写。`availableFacts`(从 `listAttestors` 列出每个 pool_stake 的 `pool:<pool_id>.{state,active_stake_lovelace,epochs_active}` + 静态聚合事实)喂 fact 下拉;每规则 = tier 名 + ALL/ANY 组合 + 叶条件(fact 下拉 / op 下拉 / value);规则可增删/上下移(首匹配序)。`flatten`/`toWhen`/`serialize` 在 builder 模型 ↔ `{tier,when}` DSL 间双向转换;`not`/嵌套条件标为 advanced,落 JSON 模式编辑(builder↔JSON 切换)。空条件=catch-all(`when` omit)。
- 2026-06-26 p6-4 | stack: ui | command: `pnpm build && pnpm lint` + `make web` | result: pass | note: TC-8(UI)。`tsc -b && vite build` 绿(JS 415KB/gzip 134KB)、`pnpm lint` 0 error(2 既有 warning);嵌入 bundle 含 builder 文案(`Add condition`/`Back to builder`/`Always matches`);二进制重 build。构建器产出的 `{tier,when}` 即 p3-1 已测的 DSL(`tier.Validate`/`Eval` + `TestAdminTierRules` 端点覆盖),无需新后端测试。

- 2026-06-26 p6-5 完成（value 按类型渲染）：`factType(fact)→bool|state|number|text`;value 组件分支:bool=true/false 下拉、state=active/pending/none 下拉、number=numeric Input;`opsFor(type)` 收窄 op(bool/state 仅 `==`/`!=`,number 全 6);`setFact` 切 fact 时按新类型 `defaultValueFor` 重置 value、非法 op 回 `==`;`+ Add condition` 默认值也按默认 fact 类型给。未知/自定义 fact → text + 全 op + 保留原值为额外 option(兼容 JSON 高级写法)。
- 2026-06-26 p6-5 | stack: ui | command: `pnpm build && pnpm lint` + `make web` | result: pass | note: TC-8(UI)。`tsc -b && vite build` 绿(JS 416KB/gzip 134KB)、`pnpm lint` 0 error;嵌入 SPA 重 stage、二进制重 build。产出仍是 p3-1 已测 DSL(值为字符串:bool→"true"/"false"、state→"active"…、number→十进制串),后端 `tier.Eval` 的 `compare`(数值 big.Int、bool/str 等值)原样消费,无需新后端测试。

## 7. Change Requests (append-only)
- 2026-06-26 初始决策（草案，用户已认可主线）：① subject 不变=钱包 stake credential;② pool 降格为 `AttestorConfig` 的一个 `Kind`(pool_stake),多池=多条,NFT 预留;③ token=`credentials` 自描述数组;④ tier_rules 全局、对**聚合事实**求值(订阅判定+tier);⑤ 薄闸=持任一 attestor(ANY,可配);⑥ 去 `OUROPASS_POOL_ID`,全走后端配置,加部署级 `OUROPASS_ISSUER`(`iss` 来源)。
- 2026-06-26 open decisions（已提出，下条为用户裁定）：(a) token 向后兼容 (b) iss 取值 (c) tier_rules 表达力 (d) network 是否下放 (e) S0006/S0005 次序。
- 2026-06-26 **open decisions 裁定（用户）**：(a) **无兼容包袱**——未上线,直接换 `credentials` 数组、删扁平 claims。(b) **`iss` = issuer 公开 base URL**(`OUROPASS_ISSUER`,必填),RP 从 `iss` 发现 JWKS。(c) **tier_rules = 命名聚合事实上的布尔 DSL**(`all`/`any`/`not` + `{fact,op,value}`),非图灵完备,只逻辑组合。(d) **network 下放到 attestor**(`Params.network`,跨链 ready;`chain.Source` 按 network 工厂)。(e) **S0006 先于 S0005**。
