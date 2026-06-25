# Ouro Pass 质押身份证明 + 链上能力加固

Spec-ID: S0004
Status: draft
Created Time: 2026-06-25T01:30:00+08:00
Start Time:
Completion Time:
Previous Spec-ID: (none)
Closure Reason:

## 1. Requirement Details

### Background
S0001 把 issuer 定位为"**会员判定方**":内置 `rules` 引擎,把链上质押映射到 tier/entitlement,token 里带判定结果。评审中发现这套与链上现实有多处不符,且把"业务策略"硬编进了 issuer。经设计讨论(见 §2 / 决策表),改定位为:

> **issuer = 质押身份证明提供方(staking-identity attestation provider)**:只证明"用户相对本池的质押事实/状态",**业务策略(阈值→权益)下沉给 RP(token 消费方)**;issuer 自己的第一方渠道(Telegram 会员/推送)保留一套**极薄的 tier 映射**(option a)。

这带来一组相关的后端/链上改造:Koios 集成升级、三态 membership 状态机、epoch 口径缓存、token claims 重塑、rules 瘦身,外加评审中暴露的 admin 身份缺口(owner↔pool 链上校验、operator/viewer 管理)与 delegator 枚举能力。

本 spec 修改 S0001 的若干子系统(rules / walletauth-admin / chain),并影响 S0002(Rules 页降级、新增 Admins 页)。S0001/S0003 已 completed(不重开),按 immutable-spec 以新 spec 承载新范围。

### Scope
**A. 质押身份证明核心(主线)**
- **Koios 集成升级**:`account_info`(live `delegated_pool` + `status`)+ `account_stake_history`(真 `active_stake` + `epochs_active`),修掉现状 `ActiveStakeLovelace=total_balance` 近似、`EpochsDelegated=-1` 失效。
- **三态 membership 状态机**:`pending` / `active` / `none`(派生);leaving 由 epoch 口径自然收敛。
- **CachedSource(单一 epoch 规则)**:接入 `StakeSnapshotCache`;命中 iff `snapshot_epoch==当前`、**只缓 `active`**(pending/none 现算)、single-flight、退休;reconciler 兼任 epoch 边界刷新 + state 重算。
- **token claims**:把派生 state + 质押事实写入 token;隐私分桶;薄 issuer 闸。
- **rules 重塑**:删阈值→tier 策略 / Rules 引擎判定 / Rules 管理端点(对外),保留 pool 闸 + facts/state 提取 + **薄第一方 tier 映射**(给渠道/push)。

**B. admin 身份加固(可独立)**
- **owner↔pool 链上校验**:`chain.PoolOwners(poolID)`;`admin.Verify` 改为对照链上 pool 注册 owner;`OUROPASS_OWNER_KEYS` 降为可选离线兜底;理顺"撤销语义"。
- **operator/viewer 管理**:新增 admin-users CRUD 端点(owner + step-up);S0002 配套 Admins 页。

**C. delegator 枚举(可独立)**
- `chain.Delegators(poolID)`(Koios `/pool_delegators` 或 db-sync);`GET /api/admin/delegators`;S0002 配套页(或并入 Dashboard)。

### Constraints
- C1 **链上语义留在 issuer**:2-epoch 激活滞后、pending、leaving 尾巴等坑由 issuer 解释,**token 给 RP 的是"已解释的事实/状态",不是 raw 链数据**。
- C2 **缓存 = 只缓 `active`**:established(active)只信 `account_stake_history`,命中 iff `snapshot_epoch == 当前 epoch`(本地算);`pending`/`none` 依赖 live 信号 → 现算不缓;不引入中途时间 TTL。
- C3 **隐私/最小披露**:精确质押金额敏感 → token 默认给**分桶/区间**;精确值作 scope 化可选 claim。
- C4 **陈旧有界**:token claims 是签发快照 → 绑短 access TTL,refresh 时重派生;RP 靠刷新看状态迁移。
- C5 **pool 锚定留 issuer**:`OUROPASS_POOL_ID` + "本池质押者才发 token"的薄闸不下沉。
- C6 **第一方 tier = option a**:issuer 自己渠道保留极薄 `state+amount→tier` 映射;对外 RP 用 raw claims 自判。
- C7 **Koios 调用有界**:`account_info` 先行,仅当指向本池才拉 `account_stake_history`;CachedSource + 退休封顶调用量。

### Non-goals
- RP 侧的业务规则实现(各 RP 自理)。
- 自建链上索引(继续依赖 Koios / db-sync;issuer 不索引)。
- 多池(一 issuer 一池不变)。
- CIP-95 等钱包路径变更(S0003 已定)。

## 2. Outline Design

### 2.1 决策表(本 spec 的设计依据)
- D1 **定位**:质押身份证明提供方;策略下沉 RP;第一方薄 tier(option a)。
- D2 **有效质押口径**:established = epoch `active_stake`(`account_stake_history`);pending = live `delegated_pool`(`account_info`)仅入场过渡。
- D3 **三态**:`pending`(live 指本池 & active 未到)/ `active`(active_stake 在本池)/ `none`;leaving 不特判——active stake ~2 epoch 后离开本池,epoch 重判自然收敛;**grace 下沉 RP/业务**。
- D4 **缓存 = 单一规则,只缓 `active`**:命中 iff `row.snapshot_epoch == 当前 epoch`(epoch 本地算,零 Koios);**只缓 `active`(epoch 稳定),`pending`/`none`(依赖 live)现算不缓** → onboarding 与 bail 都即时、对称;`fetched_at` 仅 updated_at;single-flight + 超时;`StakeSnapshotCache`=active 性能缓存,`SubscriptionSession`=会员状态(reconciler 维护)。pending/none 每次回源由钱包签名+限流兜,必要时再加短负缓存。
- D5 **token claims**:`pool_membership_state` + `active_stake`(分桶/scope)+ `epochs_active` + `member_since`;薄 issuer 闸(本池质押者);陈旧靠短 TTL+refresh。
- D6 **rules**:删阈值策略/Rules 引擎 tier 判定/对外 Rules 管理 + grace 策略下沉;留 pool 闸 + facts/state 提取;留薄第一方 tier 映射。
- D7 **owner↔pool**:改链上 `PoolOwners` 校验,`OUROPASS_OWNER_KEYS` 降可选兜底 + 撤销语义。
- D8 **delegator 枚举**:`chain.Delegators` via Koios `/pool_delegators` 或 db-sync。
- D9 **Koios 失败策略(分场景)**:登录/发新 token 的回源失败 → **fail-closed**(拒发,无数据不放行);**reconciler 刷新失败** → 保留旧 state 不动(软 fail-open,不误降已有会员)+ 告警。可选总开关再细调。

### 2.2 三态状态机(每次评估顺序)
```
评估(sch):
  snap = CachedSource.Snapshot(sch)          // epoch 口径,见 2.3
  若 account_stake_history 显示 active_stake 在本池:
      state = active                          // established;amount/epochs 取真值
  否则若 account_info.delegated_pool == 本池 && status=registered:
      state = pending                         // 入场过渡(~2 epoch);immediate
  否则:
      state = none
```
- pending 每 epoch 重判,自然收敛(active 生效 / 仍 pending / 跑路 none)。
- leaving:active 用户撤委托后,active stake ~2 epoch 才离开本池快照 → 那时 epoch 重判转 `none`;尾巴期保留 membership 符合"active stake 仍计本池"的事实;**RP 自定 grace**(用 `member_since` / 状态 claim)。
- 即时切断(风控/踢人)走已有 **admin revoke(blacklist)**,与链信号无关。

### 2.3 CachedSource(包住真 Source)——只缓 `active`,pending/none 现算
**只缓 epoch 稳定的事实**:`active` 的判据只来自 `account_stake_history`(active_stake 在本池),纯 epoch 稳定 → 缓。`pending`/`none` 都依赖 live 委托信号(account_info,中途可变)→ **都不缓、每次现算**,保证 onboarding(none→pending)与 bail(pending→none)都即时、对称。

**数据模型**:`StakeSnapshotCache` **一种结构**,缓存行**永远表示"epoch X 时 active-with-us"**;`snapshot_epoch` 是快照固有字段(Koios 返回 `epoch_no`),非外挂元数据;`state` 读时派生(命中即 active);`fetched_at` 仅作 `updated_at`(驱逐/排查),不参与命中。

**current_epoch 本地算**:网络 genesis 时间 + epoch 长度(432000s,常量进 config)纯算术,**判命中零 Koios**。

**命中规则(唯一一条)**:
```
e   = currentEpoch(now())               // 本地,零网络
row = cache.Get(sch)
命中(走 DB):  row != nil && row.snapshot_epoch == e     // 缓存里只会有 active → 命中即 active
否则回源 → 派生 state:
   active            → 写库(epoch 快照镜像)
   pending / none    → 不写库,直接返回现算结论
```
- 接口不变(`Snapshot(sch)`),`evaluate()` 不改;single-flight(同 sch 并发回源合一)+ 超时 + 失败按 D9。
- **职责切清**:`StakeSnapshotCache` = "active-with-us 这个 epoch 稳定事实"的性能缓存(只缓 active);**`SubscriptionSession`** = 会员生命周期状态(pending/active/grace)的持久记录,由 reconciler 维护。pending 成员靠 session + reconciler 跟踪,**不靠快照缓存**。
- 高频稳定的 established 成员(active)命中缓存;pending(新加入者,过渡态、低并发)与 none(非会员)现算回源,被钱包签名 + 限流卡住,代价小且可接受。
- 刷新/退休:**reconciler** epoch 边界刷**活跃集合**(有活跃订阅)+ 回源重算 state(驱动 pending→active、leaving→expire);转 `none` 的成员删缓存行 + 过期会话;不活跃 credential(无活跃订阅 + LRU)驱逐。
- 边界细节:Koios 索引滞后时 `account_stake_history` 可能仍是上一 epoch → 存"实际返回 epoch",`< 当前`则标"暂用最佳 + 待重取"。
- 取舍:pending/none 不缓 → 每次尝试打一次 Koios;若实测非会员/pending 量成问题,**再加短负缓存**(那时才让 `fetched_at` 参与有效性)——先按"只缓 active"起步。

### 2.4 Koios 数据映射(修正)
| 字段 | 来源 | 用途 |
|---|---|---|
| `delegated_pool`(live) | `account_info` | pending 判定 / 当前委托 |
| `status` | `account_info` | registered 闸 |
| `active_stake`(每 epoch) | `account_stake_history` 最新条目 | **真 active stake**(替 total_balance) |
| `epochs_active` | `account_stake_history` 尾部连续本池条数 | 质押时长(替 -1) |
- 优化:`account_info` 先行;仅当 live 指向本池才拉 `account_stake_history`(省调用)。
- `Snapshot` 结构扩展:`DelegatedPoolLive` / `ActiveStakePoolID` / `ActiveStakeLovelace` / `EpochsActive` / `AccountStatus` / `Epoch` / `FetchedAt` / 派生 `State`。

### 2.5 token claims(质押证明)
```jsonc
{
  // 既有:sub(假名)、aud、iss、exp…
  "pool_membership_state": "active",        // active|pending|none(issuer 已解释)
  "stake_bucket": "100k-1M",                // 默认分桶(隐私)
  "active_stake_lovelace": "…",             // 仅 scope 授权的 RP 给精确值
  "epochs_active": 17,
  "member_since": "2026-05-01T00:00:00Z"
}
```
- 薄 issuer 闸:仅给"本池质押者(至少 pending/active)"发 token(`none` 不发,沿用现 access_denied)——保留 token 语义 + 限流姿态。
- 陈旧:绑短 access TTL;refresh 重派生 state;RP 靠刷新看迁移。
- 隐私:默认 `stake_bucket`;精确 `active_stake_lovelace` 走 scope。

### 2.6 rules 重塑(删 / 换 / 留)
- **删/下沉 RP**:`rule_config` 阈值(min_active_stake/min_active_epochs/required_status)、Rules 引擎 tier 判定、对外 Rules 管理端点 + S0002 Rules 页、`grace_epochs` 策略。
- **换成**:facts+state 提取器(pool 闸 + active_stake + epochs + 三态)→ claims。链上语义留此。
- **留(option a)**:issuer 自己渠道用的**薄 tier 映射**(`state+amount→tier`,给 Telegram 会员/Push 定向);S0002 的 Members/Subscriptions/Push 据此续用。

### 2.7 admin 身份加固(B)
- `chain.Source` 加 `PoolOwners(ctx, poolID) ([]string, error)`(Koios `/pool_info` owners / node / db-sync)。
- `admin.Verify`:owner = `keyHash ∈ 链上 PoolOwners(poolID)`(可缓存);`OUROPASS_OWNER_KEYS` 降为可选离线兜底;**理顺撤销**(env/链上移除后 else 分支不再误授 owner)。
- operator/viewer:`POST/GET/DELETE /api/admin/users`(owner + step-up);S0002 配 Admins 页。

### 2.8 delegator 枚举(C)
- `chain.Delegators(ctx, poolID)`(分页):Koios `/pool_delegators`(bech32 stake 地址 → 用 `StakeHashFromRewardAddress` 转 hash 对齐内部口径)或 db-sync。
- `GET /api/admin/delegators`(viewer/owner,分页 + 缓存)。
- 口径区分:delegators = 链上全量委托者(全集);members = 活跃订阅者(子集)。

### 2.9 Risk and rollback
- R1 **链上口径/隐私**:口径错或泄精确金额 → 单测覆盖三态/滞后/尾巴;默认分桶 + scope。
- R2 **缓存陈旧授权**:over-grant → epoch 口径 + 短 TTL + reconciler 重判 + D9 失败策略。
- R3 **Koios 依赖/限流**:CachedSource + 退休 + single-flight + 超时;db-sync 作可选权威源。
- R4 **rules 重塑波及 S0002**:Rules 页降级/Admins 页新增 → 在 S0002(若未 close)或后续 spec 协调。
- Rollback:forward-only;未发布按 working tree;已提交 `git revert`;大改回退另立 spec。

## References
- docs/specs/completed/20260623T0041-S0001-poolops-issuer-backend.md — rules/chain/walletauth-admin 基线
- docs/specs/completed/20260624T0230-S0003-walletauth-cose-and-authz-pages.md — 钱包契约(reward 地址/COSE_Key)
- docs/specs/20260624T2355-S0002-ouropass-web-frontend.md — Admin SPA(Rules 页降级 / Admins 页新增受影响)
- Koios API:`/account_info`、`/account_stake_history`(替弃用 `/account_history`)、`/pool_info`、`/pool_delegators`
- CIP-19(reward 地址)、Cardano stake snapshot(2-epoch 激活滞后)
- server/internal/core/rules/engine.go、utils/chain/{chain,koios,node_lsq}.go、store/repo_stakesnapshotcache.go、core/admin/admin.go、worker/reconciliation

## 3. Execution Plan
- [ ] p1-1 Koios source 升级:`account_info` + `account_stake_history`,`Snapshot` 扩展(live/active pool、真 active_stake、epochs_active、status);单测(三态数据 + 滞后向量)。
- [ ] p2-1 三态状态机:`State` 派生(pending/active/none)+ leaving 收敛;纯函数单测(入场/晋升/离场尾巴/金额跌破)。
- [ ] p2-2 `CachedSource`:本地算 current_epoch;命中 iff `snapshot_epoch==当前`;**只缓 `active`**(pending/none 现算不写);single-flight + 超时 + D9 失败策略;接入 `StakeSnapshotCache`(扩展字段)。
- [ ] p2-3 reconciler 兼任:epoch 边界刷活跃集合 + state 重算 + 不活跃退休;集成测试。
- [ ] p3-1 token claims:签发/刷新写入 `pool_membership_state`/`stake_bucket`/`epochs_active`/`member_since`;薄 issuer 闸;隐私分桶 + scope 精确值;e2e。
- [ ] p4-1 rules 重塑:删阈值策略/对外 Rules 端点;保留 pool 闸 + facts/state 提取;**薄第一方 tier 映射**(渠道/push);迁移既有测试。
- [ ] p5-1 owner 链上校验:`chain.PoolOwners` + `admin.Verify` 改造 + env 降级 + 撤销语义 + 测试。
- [ ] p6-1 operator/viewer 管理端点(owner + step-up)+ 测试;（S0002 配套 Admins 页另计）。
- [ ] p7-1 delegator 枚举:`chain.Delegators` + `GET /api/admin/delegators` + 测试;（前端页另计）。
- [ ] p8-1 全量 `go test ./...` + 二进制 smoke + 文档(链数据架构/口径/claims)。

## 4. Test and Acceptance Criteria
- TC-1 Koios 映射:`account_stake_history` 取真 `active_stake` + `epochs_active`;`account_info` 取 live pool/status;仅本池委托才二次拉。
- TC-2 三态:入场→pending、~2 epoch→active、撤委托→尾巴保留→epoch 收敛 none、金额跌破→降档,纯函数全覆盖。
- TC-3 CachedSource:命中(`snapshot_epoch==当前`,缓存只含 active)、miss/epoch 滚动回源、**pending/none 不入库**(下次即时识别 onboarding/bail)、single-flight、退休、D9(登录 fail-closed / reconciler 软 fail-open);current_epoch 本地算正确。
- TC-4 reconciler:epoch 边界刷新 + state 重算 + 驱逐,集成(本地 PG / mock)。
- TC-5 token claims:state/bucket/epochs/since 正确;薄闸(none 不发);精确值受 scope;refresh 重派生。
- TC-6 rules 重塑:对外 Rules 端点移除/降级;pool 闸 + facts 保留;薄第一方 tier 映射驱动渠道;既有测试迁移后绿。
- TC-7 owner 链上校验:`keyHash∈PoolOwners`→owner;不在→拒;env 兜底可选;撤销生效。
- TC-8 operator/viewer 管理:owner+step-up 增删查;RBAC。
- TC-9 delegator 枚举:`/api/admin/delegators` 分页返回(地址→hash 对齐);members vs delegators 口径区分。
- Pass/fail:每 item 仅在映射 TC 全 pass + 证据 append 后标 `[x]`。

## 5. Execution Log (append-only)
- 2026-06-25 S0004 草案创建(draft):承载"质押身份证明提供方"定位转变及配套链上/admin 改造。源于 S0002 验收期间的一系列设计讨论(口径/缓存/三态/claims/rules 重塑/owner 链上/operator-viewer/delegator)。尚未执行。用户已认可:定位=attestation、策略下沉 RP、第一方薄 tier(option a)。

## 6. Validation Evidence (append-only)

## 7. Change Requests (append-only)
- 2026-06-25 核心决策(用户拍板):① issuer 定位 = 质押身份证明提供方,业务策略下沉 RP;② 第一方渠道保留薄 tier 映射(option a);③ rules 的"阈值→tier 策略"删除/下沉,保留 pool 闸 + 三态/facts 提取 + token claims;④ 有效质押 = epoch active_stake 口径,pending 仅入场过渡,leaving 由 epoch 自然收敛、grace 下沉 RP;⑤ 缓存只缓 `active`(epoch 稳定;命中 iff snapshot_epoch==当前、本地算 epoch),pending/none 依赖 live 故现算不缓(onboarding/bail 即时对称),fetched_at 仅 updated_at;StakeSnapshotCache=active 性能缓存 / SubscriptionSession=会员状态;single-flight+超时+退休;D9 分场景(登录 fail-closed / reconciler 软 fail-open);非会员每次回源由钱包签名+限流兜,必要时再加短负缓存;⑥ 附带 admin owner 链上校验、operator/viewer 管理、delegator 枚举三项后端加固(可独立排期)。
