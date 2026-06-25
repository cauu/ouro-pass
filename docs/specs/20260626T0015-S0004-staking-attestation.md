# Ouro Pass 质押身份证明(attestation)

Spec-ID: S0004
Status: active
Created Time: 2026-06-25T01:30:00+08:00
Start Time: 2026-06-26T00:15:00+08:00
Completion Time:
Previous Spec-ID: S0002
Closure Reason:

## 1. Requirement Details

### Background
S0001 把 issuer 定位为"**会员判定方**":内置 `rules` 引擎把链上质押映射到 tier/entitlement,token 带判定结果。评审发现这套与链上现实多处不符(active_stake≈total_balance 近似、delegation age 失效),且把业务策略硬编进 issuer。经设计讨论改定位为:

> **issuer = 质押身份证明提供方(staking-identity attestation provider)**:只证明"用户相对本池的质押事实/状态",**业务策略(阈值→权益)下沉给 RP**;issuer 自己的第一方渠道(Telegram 会员/推送)保留一套**极薄的 tier 映射**(放 `PoolConfig`)。

带来一组后端/链上改造:Koios 集成升级、三态 membership 状态机、只缓 `active` 的 epoch 缓存、token claims、**删除 rules 子系统**。另含一个**解耦、可延后**的 delegator 枚举能力。本 spec 修改 S0001 的 rules/chain 子系统,并影响 S0002(删 Rules 页)。S0001/S0003 已 completed(不重开),以新 spec 承载。

### Scope
**A. 质押身份证明核心(主线)**
- **Koios 集成升级**:`account_info`(live `delegated_pool` + `status`)+ `account_stake_history`(真 `active_stake` + `epochs_active`),修掉 `ActiveStakeLovelace=total_balance` 近似、`EpochsDelegated=-1` 失效。
- **三态 membership 状态机**:`pending` / `active` / `none`(派生);leaving 由 epoch 口径自然收敛。
- **CachedSource(只缓 `active`)**:接入 `StakeSnapshotCache`;命中 iff `snapshot_epoch==当前`(本地算);pending/none 现算不缓;single-flight + 退休;reconciler epoch 边界刷新 + state 重算。
- **token claims**:token 带派生 state + **精确** `active_stake` + `epochs_active` + `member_since`(+ 可选第一方 `tier`);薄 issuer 闸。
- **rules 删除 + 薄 tier 进 PoolConfig**:删 `MembershipRule` 表 / repo / Rules 端点 / S0002 Rules 页;保留 pool 闸 + facts/state 提取;第一方 tier 映射(`state+active_stake→tier`)落 `PoolConfig.tier_rules`,仅给 issuer 自己渠道用。

**C. delegator 枚举(解耦,可延后/单独排期)**
- `chain.Delegators(poolID)`(Koios `/pool_delegators` 或 db-sync);`GET /api/admin/delegators`;(可选)S0002 页。与 A 解耦,仅共用 `chain.Source` + 既有 `StakeHashFromRewardAddress`。

### Constraints
- C1 **链上语义留在 issuer**:2-epoch 激活滞后、pending、leaving 尾巴由 issuer 解释,**token 给 RP 的是"已解释的事实/状态",不是 raw 链数据**。
- C2 **缓存只缓 `active`**:established 只信 `account_stake_history`,命中 iff `snapshot_epoch==当前 epoch`(本地算);`pending`/`none` 依赖 live 信号 → 现算不缓;无中途 TTL。
- C3 **不分桶(本期)**:token 带**精确** `active_stake_lovelace`(策略下沉 RP,需要精确值判档)。分桶/scope 隐私增强作未来可选,不在本期。
- C4 **陈旧有界**:token claims 是签发快照 → 绑短 access TTL,refresh 重派生;RP 靠刷新看状态迁移。
- C5 **pool 锚定留 issuer**:`OUROPASS_POOL_ID` + "本池质押者才发 token"的薄闸不下沉。
- C6 **第一方 tier 进 PoolConfig**:`PoolConfig.tier_rules` 存极薄 `state+active_stake→tier`,仅 issuer 自己渠道用;对外 RP 用 raw claims 自判。
- C7 **Koios 调用有界**:`account_info` 先行,仅当指向本池才拉 `account_stake_history`;CachedSource(只缓 active)+ 退休封顶。

### Non-goals
- RP 侧业务规则实现(各 RP 自理)。
- **owner↔pool 链上校验 / operator-viewer 管理(原 B)**:本期不做,owner 沿用现 `OUROPASS_OWNER_KEYS` env 配置信任。
- **质押金额分桶/隐私 scope**:本期不做(给精确值)。
- 自建链上索引;多池;CIP-95 钱包路径变更(S0003 已定)。

## 2. Outline Design

### 2.1 决策表
- D1 **定位**:质押身份证明提供方;策略下沉 RP;第一方薄 tier(放 PoolConfig)。
- D2 **有效质押口径**:established = epoch `active_stake`(`account_stake_history`);pending = live `delegated_pool`(`account_info`)仅入场过渡。
- D3 **三态**:`pending`(live 指本池 & active 未到)/ `active`(active_stake 在本池)/ `none`;leaving 不特判——active stake ~2 epoch 后离开本池,epoch 重判自然收敛;grace 下沉 RP/业务。
- D4 **缓存只缓 `active`**:命中 iff `row.snapshot_epoch==当前 epoch`(本地算,零 Koios);pending/none 依赖 live → 现算不缓(onboarding 与 bail 即时对称);`fetched_at` 仅 updated_at;single-flight + 超时;`StakeSnapshotCache`=active 性能缓存,`SubscriptionSession`=会员状态(reconciler 维护)。
- D5 **token claims**:`pool_membership_state` + **精确** `active_stake_lovelace` + `epochs_active` + `member_since`(+ 可选第一方 `tier`);薄 issuer 闸(本池质押者才发,`none` 不发);陈旧靠短 TTL + refresh。
- D6 **rules 删除**:删 `MembershipRule`/Rules 引擎 tier 判定/Rules 端点/S0002 Rules 页;留 pool 闸 + facts/state 提取;**薄第一方 tier → `PoolConfig.tier_rules`**。
- D7 **epoch 常量内置**:per-network(genesis 起始 + 432000s/epoch)内置进 config,**本地算 current_epoch**(无需用户配置)。
- D8 **Koios 失败策略(分场景)**:登录/发新 token 回源失败 → **fail-closed**(无数据不放行);**reconciler 刷新失败** → 保留旧 state 不动(软 fail-open,不误降会员)+ 告警。
- D9 **delegator 枚举(C)**:`chain.Delegators` via Koios `/pool_delegators`(**v1 透传分页、不缓存**;冷/只读/非授权,缓存只服务热授权路径)或 db-sync;与 A 解耦,可延后。

### 2.2 三态状态机(每次评估顺序)
```
评估(sch):
  snap = CachedSource.Snapshot(sch)
  若 account_stake_history 显示 active_stake 在本池:  state = active   // established
  否则若 account_info.delegated_pool==本池 && status=registered:  state = pending  // 入场过渡 ~2 epoch
  否则:  state = none
```
- pending 每 epoch 重判,自然收敛(active / 仍 pending / none)。
- leaving:active 用户撤委托后 active stake ~2 epoch 才离本池快照 → 那时 epoch 重判转 `none`;尾巴期保留 membership 符合"active stake 仍计本池";RP 自定 grace。
- 即时切断(风控/踢人)走已有 admin revoke(blacklist),与链信号无关。

### 2.3 CachedSource——只缓 `active`,pending/none 现算
**只缓 epoch 稳定事实**:`active` 只来自 `account_stake_history`(纯 epoch 稳定)→ 缓;`pending`/`none` 依赖 live 委托(中途可变)→ 都现算,保证 onboarding(none→pending)与 bail(pending→none)即时对称。

**数据模型**:`StakeSnapshotCache` 一种结构,缓存行**永远表示"epoch X 时 active-with-us"**;`snapshot_epoch` 是快照固有字段;`state` 读时派生(命中即 active);`fetched_at` 仅 updated_at。

**current_epoch 本地算**:网络 genesis + 432000s 纯算术(D7),判命中零 Koios;Koios 索引滞后时存"实际返回 epoch",`<当前`标"暂用最佳 + 待重取"。

**命中规则(唯一)**:
```
e = currentEpoch(now()); row = cache.Get(sch)
命中(走 DB):  row != nil && row.snapshot_epoch == e        // 缓存只含 active → 命中即 active
否则回源 → 派生 state:  active → 写库;  pending/none → 不写库,直接返回
```
- single-flight(同 sch 并发合一)+ 超时 + 失败按 D8;`evaluate()` 接口不改。
- 职责切清:`StakeSnapshotCache`=active 性能缓存;`SubscriptionSession`=会员生命周期(reconciler 维护)。
- 刷新/退休:reconciler epoch 边界刷活跃集合 + 回源重算(pending→active、leaving→expire);转 `none` 删缓存行 + 过期会话;不活跃 credential(无活跃订阅 + LRU)驱逐。
- 取舍:pending/none 不缓 → 每次回源,被钱包签名 + 限流兜;实测有量再加短负缓存(那时才让 fetched_at 参与有效性)。

### 2.4 Koios 数据映射(修正)
| 字段 | 来源 | 用途 |
|---|---|---|
| `delegated_pool`(live) | `account_info` | pending 判定 / 当前委托 |
| `status` | `account_info` | registered 闸 |
| `active_stake`(每 epoch) | `account_stake_history` 最新条目 | 真 active stake(替 total_balance) |
| `epochs_active` | `account_stake_history` 尾部连续本池条数 | 质押时长(替 -1) |
- 优化:`account_info` 先行;仅 live 指向本池才拉 `account_stake_history`。
- `Snapshot` 扩展:`DelegatedPoolLive` / `ActiveStakePoolID` / `ActiveStakeLovelace` / `EpochsActive` / `AccountStatus` / `Epoch` / `FetchedAt` / 派生 `State`。

### 2.5 token claims(质押证明)
```jsonc
{
  // 既有:sub(假名)、aud、iss、exp…
  "pool_membership_state": "active",        // active|pending(none 不发 token)
  "active_stake_lovelace": "1234567",       // 精确值(本期不分桶)
  "epochs_active": 17,
  "member_since": "2026-05-01T00:00:00Z",
  "tier": "gold"                            // 可选,第一方意见(PoolConfig.tier_rules);RP 可忽略
}
```
- 薄 issuer 闸:仅给本池质押者(pending/active)发 token,`none` → access_denied。
- 陈旧:短 access TTL;refresh 重派生;RP 靠刷新看迁移。

### 2.6 rules 删除 + 薄 tier 进 PoolConfig
- **删**:`MembershipRule` 表 + repo + `GET/POST /api/admin/rules` + S0002 Rules 页 + `rule_config`/grace 等;旧 `rules` 引擎的 tier 判定。
- **换**:facts+state 提取器(pool 闸 + active_stake + epochs + 三态)→ claims。链上语义留此。
- **薄第一方 tier**:`PoolConfig` 加 `tier_rules` JSON(有序 `[{min_state, min_active_stake → tier}]`,自上而下取第一个匹配),例如:
  ```json
  [{"tier":"gold","min_state":"active","min_active_stake":"1000000"},
   {"tier":"silver","min_state":"active","min_active_stake":"100000"},
   {"tier":"basic","min_state":"active"},
   {"tier":"basic","min_state":"pending"}]
  ```
  仅 issuer 自己渠道(Telegram 会员/Push 定向 = S0002 Members/Subscriptions/Push)消费;无 CRUD 引擎,改 PoolConfig 即可。

### 2.7 delegator 枚举(C,解耦)
- `chain.Source` 加 `Delegators(ctx, poolID, page) ([]string, error)`(Koios `/pool_delegators` 分页 → `StakeHashFromRewardAddress` 转 hash;db-sync 可选;node_lsq 返回 not-implemented)。
- `GET /api/admin/delegators`(viewer/owner)。
- **v1 不缓存:直接透传 Koios 分页**(admin 要第 N 页 → 拉 Koios 第 N 页)。理由:冷、只读、admin、非授权、新鲜度不敏感——**缓存只服务热的授权路径(A),不服务冷的管理查询**;且 delegator 是 pool 级列表,塞不进 credential 级的 `StakeSnapshotCache`。Koios 挂则名册报错(非授权,无需兜底)。
- **可选事后优化**:仅当"大池 + 频繁刷新"打到 Koios 限流,再加按 epoch 的轻量全量缓存(delegator 集合大致 epoch 稳定)。
- 口径:delegators=链上全量委托者(全集);members=活跃订阅者(子集)。与 A 无耦合,可延后。

### 2.8 Risk and rollback
- R1 链上口径错 → 三态/滞后/尾巴纯函数单测 + 真链/真钱包验证。
- R2 缓存陈旧授权 over-grant → 只缓 active(epoch 稳定)+ 短 access TTL + reconciler 重判 + D8。
- R3 Koios 依赖/限流 → CachedSource + 退休 + single-flight + 超时;db-sync 可选权威源。
- R4 删 rules 波及 S0002 Rules 页 → 在 S0002(若未 close)或后续协调。
- Rollback:forward-only;未发布按 working tree;已提交 `git revert`。

## References
- docs/specs/completed/20260623T0041-S0001-poolops-issuer-backend.md — rules/chain 基线
- docs/specs/20260624T2355-S0002-ouropass-web-frontend.md — Admin SPA(Rules 页删除受影响)
- Koios:`/account_info`、`/account_stake_history`(替弃用 `/account_history`)、`/pool_delegators`
- Cardano stake snapshot(2-epoch 激活滞后)、CIP-19
- server/internal/core/rules/engine.go(待删/重塑)、utils/chain/{chain,koios}.go、store/repo_stakesnapshotcache.go、repo_poolconfig.go、worker/reconciliation

## 3. Execution Plan
- [x] p1-1 Koios 升级:`account_info` + `account_stake_history`,`Snapshot` 扩展(live/active pool、真 active_stake、epochs_active、status);单测(滞后向量)。
- [x] p2-1 三态状态机:`State` 派生 + leaving 收敛;纯函数单测(入场/晋升/离场尾巴/金额跌破)。
- [x] p2-2 `CachedSource`:本地算 current_epoch(内置 epoch 常量);命中 iff `snapshot_epoch==当前`;**只缓 active**(pending/none 现算);single-flight + 超时 + D8;接入 `StakeSnapshotCache`。
- [ ] p2-3 reconciler:epoch 边界刷活跃集合 + state 重算 + 不活跃退休;集成测试。
- [ ] p3-1 token claims:签发/刷新写 state/active_stake/epochs/since(+可选 tier);薄 issuer 闸;e2e。
- [ ] p4-1 rules 删除:删 `MembershipRule`/Rules 端点/引擎 tier 判定;`PoolConfig.tier_rules` + 第一方 tier 映射(渠道/push);迁移既有测试;(S0002 删 Rules 页另计)。
- [ ] p5-1 (可选/可延后) delegator 枚举:`chain.Delegators(poolID,page)` + `GET /api/admin/delegators`(**透传 Koios 分页、不缓存**)+ 测试。
- [ ] p6-1 全量 `go test ./...` + 二进制 smoke + 文档(链数据架构/口径/claims/tier)。

## 4. Test and Acceptance Criteria
- TC-1 Koios:`account_stake_history` 取真 active_stake + epochs_active;`account_info` 取 live pool/status;仅本池委托才二次拉。
- TC-2 三态:入场→pending、~2 epoch→active、撤委托→尾巴→收敛 none、金额跌破→降档,纯函数覆盖。
- TC-3 CachedSource:命中(snapshot_epoch==当前,只含 active)、miss/epoch 滚动回源、pending/none 不入库(即时识别 onboarding/bail)、single-flight、退休、D8;current_epoch 本地算正确。
- TC-4 reconciler:epoch 边界刷新 + state 重算 + 驱逐(本地 PG / mock)。
- TC-5 token claims:state/active_stake/epochs/since 正确;薄闸(none 不发);refresh 重派生;可选 tier 来自 PoolConfig。
- TC-6 rules 删除:Rules 端点/表移除;pool 闸 + facts 保留;`PoolConfig.tier_rules` 驱动第一方 tier;既有测试迁移后绿。
- TC-7 (可选) delegator 枚举:`/api/admin/delegators` 分页(地址→hash);members vs delegators 口径区分。
- Pass/fail:每 item 仅在映射 TC 全 pass + 证据 append 后标 `[x]`。

## 5. Execution Log (append-only)
- 2026-06-25 S0004 草案创建(draft):承载"质押身份证明提供方"定位转变(Koios 升级 + 三态 + 只缓 active + token claims + 删 rules + 薄 tier 进 PoolConfig)。源于 S0002 验收期的设计讨论。尚未执行。
- 2026-06-25 范围收窄(用户拍板):①不分桶(token 给精确金额);②薄 tier 进 `PoolConfig.tier_rules`,**删 rules 表/端点/Rules 页**;③**砍掉原 B**(owner 不查链、沿用 env 配置;operator/viewer 暂不加);④epoch 常量内置(本地算 epoch);⑤delegator 枚举(C)与 A 解耦、可延后。

## 6. Validation Evidence (append-only)
- 2026-06-26 p1-1 完成：`chain.Snapshot` 加 `ActiveStakePoolID`（active 来源池）；`ActiveStakeLovelace`/`EpochsDelegated` 改由 `account_stake_history` 真值驱动（替 total_balance 近似 / -1 失效）。Koios `Snapshot` 增 `/account_stake_history` 拉取 + 纯函数 `koiosToSnapshot`/`latestStakeEntry`/`trailingActiveEpochs`。**决策**：① 为最小化 churn 保留既有字段名 `DelegatedPoolID`(=live 委托) / `EpochsDelegated`(=trailing active epochs)，仅加一个新字段，rules `InputFromSnapshot` 与 node_lsq/db_sync/mock 零改动即编译通过；② **对任何 registered 账户都拉 stake_history**（不按 live 池剪枝）——否则会漏掉"live 已移走、active stake 仍在本池"的 leaving 尾巴（§2.2 正确性 > §2.4 调用优化）；③ Source 保持 pool-agnostic，池比较留给 p2-1 的 `DeriveState`；④ node_lsq/db_sync 无 active-stake 历史 → 仅能产 pending/none（生产路径用 Koios），属已知降级；⑤ 端点名 `/account_stake_history` 依 §2.4，行/响应形状待 live Koios 核（R1）。
- 2026-06-26 p1-1 | stack: go | command: `go build ./...` + `go test ./internal/utils/chain/` + `go test ./...` | result: pass | note: 新增 `TestKoiosToSnapshot_Vectors`（pending 无史 / leaving 尾巴 live≠active / 换池 trailing 重置）+ 改 `TestKoiosSource_ParsesAccountInfoAndTip`（active_stake 取 history 最新、epochs=3、big lovelace C4 精确）；chain 包绿；全仓 build+test 0 FAIL（rules 引擎沿用字段，行为升级为真 active_stake）。

- 2026-06-26 p2-1 完成：新建 `server/internal/core/membership` 包——`State`(none/pending/active)+ 纯函数 `DeriveState(snap, poolID)`。规则：active iff `ActiveStakePoolID==poolID`（含 leaving 尾巴）；pending iff `registered && DelegatedPoolID==poolID`；否则 none；空 poolID/nil 防御性 none。**决策**：① DeriveState 不放 `chain` 包（chain 刻意 pool-agnostic、只给 raw facts），独立 `membership` 包承载"会员结论"，将作为删 rules 后的 facts/state 提取器归宿；② **state 与金额无关**——"金额跌破→降档"属 tier 关注点(p4-1 `tier_rules`)，非 state；active 只看是否本池 active-staked。
- 2026-06-26 p2-1 | stack: go | command: `go test ./internal/core/membership/` | result: pass | note: `TestDeriveState` 8 向量（入场/晋升/离场尾巴/收敛 none/active 他池/未注册/金额无关/nil）+ 空 poolID 守卫全绿。

- 2026-06-26 p2-2 完成：`chain.CurrentEpoch(network,now)` 纯算（mainnet/preprod/preview genesis+epoch 长度内置，未知/创世前→ok=false）；`membership.CachedSource` 包装 `chain.Source`、实现 `chain.Source`（evaluate 接口不改）：命中 iff 缓存行 `snapshot_epoch==本地当前 epoch`（零链 I/O）；**只缓 active**，pending/none `singleflight` 现算回源 + `context` 超时；bail(active→pending/none)删缓存行。`StakeSnapshotCache` 加 `epochs_active` 列（迁移 0009 sqlite+pg）+ repo `Delete`，使命中可零链重建完整 active snapshot。main 用 `CachedSource` 包 rawChain（oauth + reconciler 共用）。**决策**：① CachedSource 放 `membership` 包（与 DeriveState 同域"会员数据访问"，避免 `chain` util 反向依赖 store）；② **缓存行存本地 epoch（非 Koios /tip epoch）**——令命中自洽且有效、与 Koios 索引精度解耦；安全因为只缓 epoch 稳定的 active + reconciler 边界重判 + 短 access TTL；③ 错/未知 epoch 不会产生陈旧命中（存的 epoch 不会匹配），最坏退化为永远回源（仍正确）；④ D8 失败策略由调用方定，CachedSource 原样透传 error；⑤ epoch 常量 mainnet Shelley 锚点已单测校验，preprod/preview 待 live Koios 核（R1）。
- 2026-06-26 p2-2 | stack: go | command: `go test ./internal/utils/chain/ ./internal/core/membership/ ./...` + `go build ./...` | result: pass | note: `TestCurrentEpoch`（mainnet epoch208 锚点、preview/preprod 边界、未知/创世前 ok=false）；`TestCachedSource_*`（active 命中不回源 + epoch 滚动回源、pending 不缓每次回源、bail 删行）；wiring 改 `TestBuildServices` 断言 `mock+cache`；全仓绿。

## 7. Change Requests (append-only)
- 2026-06-25 核心决策(累积,用户拍板):① issuer = 质押身份证明提供方,业务策略下沉 RP;② token 带精确事实(state/active_stake/epochs/since),**不分桶**;③ **删除 rules 子系统**,薄第一方 tier 映射进 `PoolConfig.tier_rules`(仅自家渠道用);④ 有效质押 = epoch active_stake 口径,pending 仅入场过渡,leaving 由 epoch 自然收敛、grace 下沉 RP;⑤ 缓存**只缓 `active`**(epoch 稳定;命中 iff snapshot_epoch==当前、本地算 epoch),pending/none 现算不缓(onboarding/bail 即时对称);⑥ Koios 失败 D8 分场景(登录 fail-closed / reconciler 软 fail-open);⑦ epoch 常量内置 per-network;⑧ **砍掉 owner 链上校验 / operator-viewer 管理(原 B)**——owner 沿用 env 配置信任;⑨ delegator 枚举(C)解耦、可延后/单独排期。
