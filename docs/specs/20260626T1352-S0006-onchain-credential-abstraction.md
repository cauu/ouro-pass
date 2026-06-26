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
- [ ] p2-2 token claims→`credentials` 数组 + `iss` 解耦(部署级 issuer id);e2e;RP 迁移说明。
- [ ] p3-1 tier_rules 泛化语法(over 聚合)+ 订阅/渠道 tier 接入;单测(多池/边界)。
- [ ] p4-1 配置：删 `OUROPASS_POOL_ID` + 加 `OUROPASS_ISSUER`(=iss base URL);`network` 下放 attestor + `chain.Source` 按 network 工厂;冷启动未配置态;迁移既有部署。
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
- 2026-06-26 S0006 激活（draft→active），Previous-Spec S0004。
- 2026-06-26 p1-1 完成：新包 `server/internal/core/attestor`——`Attestor` 接口(`Kind/ID/Attest`)+ `Attestation{Kind,ID,Held,Claim,Facts}` + `Registry`(Kind→Builder,`DefaultRegistry` 仅注册 `pool_stake`,NFT 不注册=故意 C5)+ `PoolStakeAttestor`(包 `membership.DeriveState`,产出 token claim `{kind,pool,network,state,active_stake_lovelace,epochs_active,member_since}` + 命名聚合事实 `pool:<id>.<name>`)+ `SourceFor` 按 network 取源的接口缝(D4,真实工厂留 p4-1)。`Held = state != none`(active/pending 都算持有)。member_since 逻辑从 oauth 迁来、改为 per-pool。

## 6. Validation Evidence (append-only)
- 2026-06-26 p1-1 | stack: go | command: `go build ./... && go test ./internal/core/attestor/ && go vet ./internal/core/attestor/` | result: pass | note: TC-1。`TestPoolStake_MultiPool`(两 pool_stake attestor over 同 subjects,各自独立 active/pending/none + Held)、`TestPoolStake_ClaimAndFacts`(claim 携带 active_stake/epochs/member_since;命名事实 `pool:<id>.*`)、`TestRegistry`(default 注册 pool_stake;NFT 未注册报错;缺 pool_id 报错)全绿;全仓 build 绿、vet 净。

- 2026-06-26 p1-2 完成：新模型 `domain.AttestorConfig{AttestorID,Kind,Label,Params,Status}` + 迁移 `0012_attestor_config`(sqlite/postgres)——建 `AttestorConfig`(`UNIQUE(kind,label)`)+ `IssuerConfig`(单例,持全局 tier_rules);**backfill**:每条 `PoolConfig` → 一条 `pool_stake` attestor(pool_id/network/ticker/name 折进 params json),其 `tier_rules` 抬到 `IssuerConfig`。repo:`Attestors()` CRUD(Create/Get/List/ListActive/Update/SetStatus/Delete by id)+ `Issuer()` GetTierRules/SetTierRules(单例 upsert;无行返 `[]`)。**注**:`PoolConfig` 表暂留(旧 `oauth.firstPartyTier`/network 读路径在后续 item 切换),本 item 仅加新模型 + backfill,保证逐 commit 不破。
- 2026-06-26 p1-2 | stack: go | command: `go test ./internal/store/ && go vet ./internal/store/ ./internal/domain/` | result: pass | note: TC-2。`TestAttestorConfigRepo_CRUD`(增/查/列/改/启停/删 + `UNIQUE(kind,label)` 冲突 + 删后 NotFound)、`TestIssuerConfig_TierRules`(无行=`[]`、set→get、单例重 upsert 不增行)、`TestMigration_BackfillPoolToAttestor`(部分迁移到 0011 → 注入 PoolConfig → 跑 0012,断言 pool_stake attestor + params 含 pool_id/network/ticker、tier_rules 抬到 IssuerConfig)全绿;全 store 套件 + vet 净。

- 2026-06-26 p2-1 完成（多 attestor 求值 + 聚合 + ANY 闸 + oauth 切换）：① `attestor.Set`/`BuildSet`/`Evaluate`→`Result{Attestations,Held,Facts}`,聚合派生 `any_held`/`any_active`/`total_active_stake`(后两者从命名事实 `*.state`/`*.active_stake_lovelace` 汇总,kind 无关)。② 修 `poolstake` bug:active-stake 系事实(amount/epochs/since)仅在 `state==active`(=本池=ActiveStakePoolID)时产出,否则多池共享同一 snapshot 会把别池的 active stake 错记。③ oauth 切换:删 `classify`(单 `cfg.PoolID`+`cfg.Chain`),新 `evaluate`(黑名单→空 Result)+ `representativeState`(any_active→active / Held→pending / 否则 none)+ `primaryHeld`;`Membership`/`Attest`/Authorize 闸全走 ANY-of;`firstPartyTier` 改读 `IssuerConfig.GetTierRules`(脱离 PoolConfig)。reconciler/telegram 接口签名不变(消费 representative state),零改动。④ `oauth.Config` 加注入式 `Attestors func(ctx)(*Set,error)`;main.go 按 `Attestors().ListActive`→`BuildSet` 每调用解析(admin 改配置即时生效),`srcFor` 暂统一返回 chainSrc(per-network 工厂留 p4-1)。`Config.Chain/PoolID/Network` 暂留(p4-1 删)。**transitional**:flat token claims 仍由 `primaryHeld` 代表值产出(p2-2 换 `credentials[]`);`attestation.credentials/facts` 已备好待 p2-2/p3-1。
- 2026-06-26 p2-1 | stack: go | command: `go test ./... && go vet ./... && go build ./cmd/issuer` | result: pass | note: TC-1/TC-3。`attestor`:`TestSet_EvaluateAggregate`(多池聚合 any_active/total_active_stake/命名事实)、`TestSet_EvaluateNotHeld`(零持→Held=false/total=0)、`TestBuildSet`(配置建集 + 未知 kind 失败)。oauth/e2e/httpapi 全套件随接口切换更新(注入 `Attestors` + tier_rules 改 seed 到 `IssuerConfig`)后全绿;全仓 `go test ./...` 0 失败、二进制 build 绿。vet 唯一告警在 `handlers_admin_resources_test.go:111`(HEAD 既有、与本 item 无关)。

## 7. Change Requests (append-only)
- 2026-06-26 初始决策（草案，用户已认可主线）：① subject 不变=钱包 stake credential;② pool 降格为 `AttestorConfig` 的一个 `Kind`(pool_stake),多池=多条,NFT 预留;③ token=`credentials` 自描述数组;④ tier_rules 全局、对**聚合事实**求值(订阅判定+tier);⑤ 薄闸=持任一 attestor(ANY,可配);⑥ 去 `OUROPASS_POOL_ID`,全走后端配置,加部署级 `OUROPASS_ISSUER`(`iss` 来源)。
- 2026-06-26 open decisions（已提出，下条为用户裁定）：(a) token 向后兼容 (b) iss 取值 (c) tier_rules 表达力 (d) network 是否下放 (e) S0006/S0005 次序。
- 2026-06-26 **open decisions 裁定（用户）**：(a) **无兼容包袱**——未上线,直接换 `credentials` 数组、删扁平 claims。(b) **`iss` = issuer 公开 base URL**(`OUROPASS_ISSUER`,必填),RP 从 `iss` 发现 JWKS。(c) **tier_rules = 命名聚合事实上的布尔 DSL**(`all`/`any`/`not` + `{fact,op,value}`),非图灵完备,只逻辑组合。(d) **network 下放到 attestor**(`Params.network`,跨链 ready;`chain.Source` 按 network 工厂)。(e) **S0006 先于 S0005**。
