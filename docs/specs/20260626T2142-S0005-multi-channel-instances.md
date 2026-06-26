# Ouro Pass 多渠道 / 同平台多实例

Spec-ID: S0005
Status: active
Created Time: 2026-06-26T11:32:00+08:00
Start Time: 2026-06-26T21:42:00+08:00
Completion Time:
Previous Spec-ID: S0006
Closure Reason:

## 1. Requirement Details

### Background
当前渠道(channel)模型是**一池一 `channel_type` 一实例**(S0004 p8-3 修复并明确为设计边界)：
- `ChannelConfig` 行虽以随机 `channel_id` 为主键，但下游一律按 `channel_type` 定址：`GetByType` 取单行；worker 的 token 解析 `GetByType("telegram")` 取单行。
- `SubscriptionSession` 唯一键 `(pool_id, channel_type, channel_user_id)`——绑的是**渠道类型**，不是某个实例。
- 激活码 / 激活深链、推送投递都按 `channel_type` 走，无实例概念。
- worker 在 `main` 里固定起一个 telegram worker（动态 token，但仍是单实例）。

用户要求：渠道应支持**配置多个渠道（多平台）**，且**同一平台也不限制只配一个实例**（如一个池跑两个 telegram bot：会员 bot + 公告 bot，或按 tier 分流）。本 spec 把渠道实例提升为**一等可定址实体**，端到端贯穿 worker / 订阅 / 激活 / 推送 / 管理 UI。

> 本 spec **不改动** S0004 的质押身份证明核心（链数据 / 三态 / 缓存 / token claims / tier_rules）。

### Scope
- **渠道实例可定址**：每个渠道实例有稳定 `channel_id` + 人类可读 `name/label`；一个 `(pool_id, channel_type)` 下可有 N 个 active 实例。
- **worker 监督者（supervisor）**：按 active telegram 实例**逐实例起 worker**，每实例独立 token/transport/long-poll offset；实例增删时**动态启停**（无需重启、无 goroutine 泄漏）。
- **订阅绑实例**：`SubscriptionSession` 加 `channel_id`，会员订阅绑到具体实例；激活码 / 激活深链指向具体实例。
- **推送绑实例**：`PushJob` 可按 `channel_id` 定向，经该实例 transport 投递。
- **管理 API + UI**：渠道实例 **CRUD**（列表 / 新建 / 改 / 删 / 启停）；Channels 页管理 N 个实例（取代单表单）；Setup 反映。
- **数据迁移**：为既有单 telegram 实例平滑迁移（回填 `channel_id` 到现有订阅 / 激活码）。

### Constraints
- C1 **向后兼容**：既有单 telegram 配置 + 现存订阅在迁移后继续可用；env `OUROPASS_TELEGRAM_TOKEN` 仍可作为"默认实例"来源（运维/兼容），但 DB 实例为主路径。
- C2 **密钥仍加密**：渠道 secret（bot token）继续 field cipher 加密落库，永不明文、永不回传。
- C3 **不破坏 S0004**：attestation / token / tier_rules 不动；订阅的 `tier` 仍由 `PoolConfig.tier_rules` 在消费点现算。
- C4 **worker 生命周期安全**：supervisor 必须可靠启停子 worker（ctx 取消传播），实例删除/停用后其 worker 在有限时间内退出；不重复起同一实例。
- C5 **平台可扩展但本期只接 telegram**：模型支持多 `channel_type`，但运行时实现仍只交付 telegram；新平台（discord/slack…）的具体实现是后续单独工作（见 Non-goals）。

### Non-goals
- 新平台（discord/slack/email…）的具体 transport 实现——本期仅把模型/管理做成多平台-ready，运行时只接 telegram。
- 改动质押证明 / token claims / tier_rules（S0004 已定）。
- 跨实例的会员去重 / 合并策略（同一 stake 在多个 bot 各自订阅，视为独立订阅）。
- 渠道级细粒度权限（谁能管哪个实例）——沿用现有 operator 角色闸。

## 2. Outline Design

### 2.1 决策表（待执行期确认/细化）
- D1 **实例定址**：`channel_id` 稳定（创建时生成，不随保存变化）；`(pool_id, channel_type, name)` 唯一，避免重名。
- D2 **订阅绑实例**：`SubscriptionSession.channel_id`（FK 概念，非强约束）；唯一键由 `(pool_id, channel_type, channel_user_id)` 改为 `(channel_id, channel_user_id)`（同一 user 可在不同 bot 各订一份）。
- D3 **worker supervisor**：单 supervisor goroutine 周期对账 active telegram 实例集合 → 起缺失实例 worker、停已删/停用实例 worker；每实例 `context.WithCancel` 子 ctx。token 经 `telegram.DecodeToken` 解密。
- D4 **激活**：`ActivationCode.channel_id`；`/bind` 深链 `?start=<code>` 指向该实例 bot；处理器把 `subscription.channel_id` 写为该实例。
- D5 **推送**：`PushJob.channel_id`（可空=按 type/tier 广播到该 type 全部实例？或必填到单实例）——执行期定**必填单实例**还是**可广播**，默认必填单实例最简。
- D6 **迁移既有单实例**：迁移脚本为现存唯一 telegram `ChannelConfig` 设 `name='default'`；现存 `SubscriptionSession`/`ActivationCode` 回填该实例 `channel_id`。env-token 路径作为"无 DB 实例时的隐式 default 实例"。
- D7 **删除语义**：删/停用渠道实例 → 其 worker 退出 + 关联 active 订阅置 `cancelled`（或保留但停投递？）；执行期定，默认级联 cancel 订阅。
- D8 **CRUD 幂等**：更新按 `channel_id`；S0004 p8-3 的 `ReplaceByType` 单实例语义被 CRUD 取代（保留 repo 但管理路径走 by-id）。

### 2.2 数据模型变更
- `ChannelConfig`：加 `name TEXT`（实例标签）；`channel_id` 改为创建期稳定生成；`UNIQUE(pool_id, channel_type, name)`。
- `SubscriptionSession`：加 `channel_id TEXT`；唯一键迁移到 `(channel_id, channel_user_id)`。
- `ActivationCode`：加 `channel_id TEXT`。
- 迁移：回填既有数据到 `default` 实例（D6）。

### 2.3 接口（admin API）
- `GET /api/admin/channels` — 列实例（type/name/status/configured，不回 secret）。
- `POST /api/admin/channels` — 建实例 `{channel_type, name, config:{bot_token}}`（operator；telegram token 加密）。
- `POST /api/admin/channels/{id}` — 改（名/token/状态）。
- `POST /api/admin/channels/{id}/disable` / `.../enable` — 启停。
- `DELETE /api/admin/channels/{id}` — 删（级联按 D7）。
- 激活创建端点带 `channel_id`（选定 bind 目标实例）。

### 2.4 worker supervisor 草图
```
supervisor.Run(ctx):
  running = map[channel_id]cancelFn
  每 tick:
    active = Channels.ListActive(pool, "telegram")
    for inst in active 且 inst.id ∉ running:  起子 worker(inst) → running[inst.id]=cancel
    for id in running 且 id ∉ active:          cancel() → delete running[id]
```
每子 worker = 现有 `telegram.Worker`，transport token 固定为该实例解密 token，offset 独立。

### 2.5 Risk and rollback
- R1 worker 泄漏/重复 → supervisor 单点对账 + 子 ctx；集成测试覆盖增删启停。
- R2 迁移破坏既有订阅 → default 实例回填 + 迁移幂等 + 迁移前后订阅可投递验证。
- R3 唯一键变更（订阅）冲突 → 迁移期处理重复；测试覆盖。
- R4 删除级联误伤订阅 → D7 明确语义 + 审计 + 可先"停用不删数据"。
- Rollback：forward-only；未发布按 working tree；已提交 `git revert`；DB 迁移加对应回滚迁移（或新 spec）。

## References
- docs/specs/20260626T0015-S0004-staking-attestation.md — 渠道单实例修复(p8-3) + 延后项 p9-1（本 spec 承接）
- docs/specs/completed/20260623T0041-S0001-poolops-issuer-backend.md — 渠道/订阅/推送基线（§6）
- server/internal/store/repo_channelconfig.go、repo_subscriptionsession.go、repo_activationcode.go、repo_pushjob.go
- server/internal/worker/telegram/（worker/transport/processor/channelconfig）、cmd/issuer/main.go（worker 接线）
- web/src/features/channels/ChannelsPage.tsx、features/setup/SetupPage.tsx

## 3. Execution Plan
- [x] p1-1 数据模型 + 迁移：`ChannelConfig.name` + 稳定 `channel_id` + 唯一约束；`SubscriptionSession.channel_id`（唯一键迁移）；`ActivationCode.channel_id`；既有单 telegram 回填 `default` 实例（D6）。
- [x] p1-2 repo：渠道实例 CRUD（List/Get/Create/Update/SetStatus/Delete by id）；订阅/激活 by `channel_id`；单测。
- [x] p2-1 worker supervisor：逐实例 telegram worker + 动态启停 + 子 ctx + 每实例 token/offset；集成测试（增/删/停 → worker 起停）。
- [x] p2-2 激活绑实例：`/bind` 深链 + 激活码带 `channel_id`；处理器写 `subscription.channel_id`；e2e。
- [x] p3-1 推送绑实例：`PushJob.channel_id` 定向 + 经实例 transport 投递（D5 语义落定：可空=type 级广播；非空=单实例定向，最简）。
- [x] p4-1 admin API：channels CRUD 端点 + RBAC + 审计；删除级联（D7）。
- [x] p5-1 前端：Channels 页管理 N 实例（列表 + 增/改/删/启停 + configured 状态）；Setup 反映；类型/ api。
- [x] p6-1 全量 `go test ./...` + `pnpm build/lint` + 二进制 smoke（多实例 worker 起停）+ 文档（渠道实例模型/生命周期）。
- [x] p7-1 （交付后追加）token 尾号指纹：`GET /channels` 返回 `token_hint`（前4…后4），UI Channels 表展示 Token 列；完整 token 仍永不回传（C2 部分放宽，见 §7）。

## 4. Test and Acceptance Criteria
- TC-1 数据模型：实例可建多份（同 type 不同 name）；迁移把既有单 telegram + 其订阅/激活回填到 `default`，迁移前后订阅可投递。
- TC-2 repo CRUD：by-id 增改删启停正确；订阅/激活按 `channel_id` 定址；唯一键新语义。
- TC-3 supervisor：新增实例→worker 起；删/停实例→对应 worker 在有限时间退出；不重复起；ctx 取消传播（无泄漏，集成测试）。
- TC-4 激活：深链指向选定实例；激活后 `subscription.channel_id` 正确；不同实例同 user 各自独立订阅。
- TC-5 推送：按 `channel_id` 投到正确实例；跨实例不串。
- TC-6 admin API + RBAC：operator 可 CRUD；非授权拒；删除级联按 D7 + 审计。
- TC-7 前端：Channels 页可管理多实例；Setup 反映 ≥1 实例已配。
- TC-8 token 指纹（p7-1）：`GET /channels` 返回 `token_hint`=前4…后4；响应中**不含**完整 token；UI 展示该指纹。
- Pass/fail：每 item 仅在映射 TC 全 pass + 证据 append 后标 `[x]`。

## 5. Execution Log (append-only)
- 2026-06-26 S0005 草案创建（draft）：承接 S0004 p9-1（多渠道/同平台多实例）。把渠道实例提升为可定址一等实体，贯穿 worker/订阅/激活/推送/UI；本期运行时仍只接 telegram，多平台模型-ready。尚未执行。
- 2026-06-26T21:42:00+08:00 S0005 激活（draft→active）：从 docs/specs/draft/ 提升为唯一 active spec，Start Time 落定，Previous Spec-ID=S0006。spec 转入 append-only；执行计划 p1-1 起逐项交付。
- 2026-06-26T21:42:00+08:00 p1-1 started：数据模型 + 迁移。
- 2026-06-26T21:42:00+08:00 p1-1 completed：domain `ChannelConfig.Name`/`SubscriptionSession.ChannelID`/`ActivationCode.ChannelID`；migration `0014_channel_instances.sql`（sqlite + postgres）：`ChannelConfig.name`（默认 'default'）+ `UNIQUE(pool_id, channel_type, name)`；`ActivationCode.channel_id` + 回填；`SubscriptionSession` 重建（sqlite）/ALTER+换约束（postgres）将唯一键改为 `(channel_id, channel_user_id)` 并回填既有 telegram 订阅到其实例。对齐既有 repo SQL（Upsert/cols/scan/ON CONFLICT）。新增迁移回填测试 `TestMigrate_ChannelInstancesBackfill`。
- 2026-06-26T21:42:00+08:00 p1-2 completed：`ChannelConfigRepo` by-id CRUD（Create/Get/List/ListActive/SetStatus/Delete + `scanChannel`）；`SubscriptionRepo` by-instance（GetByInstanceUser/ListActiveByInstance/CancelByChannelID）；`domain.ErrConflict`；repo 单测。
- 2026-06-26T21:42:00+08:00 p2-1 completed：`telegram.Supervisor`（Run/desired/reconcile + `Runner`/`Factory` 注入）逐实例对账启停、子 ctx、token 变更指纹重建、env-token 隐式 default fallback；`telegram.NewInstanceProcessor`（channel_id 感知 lookup/写订阅）；main.go 用 supervisor 取代单 worker，新增 `instanceToken`/`telegramReconcileInterval`/`envInstanceID`，push 暂沿用 type 级 token（p3-1 改实例级）。集成测试 `TestSupervisor_*`（-race）。
- 2026-06-26T21:42:00+08:00 p2-2 completed：激活码带 `channel_id`（`CreateActivation` 增参 + 落库）；`Consume` 增 channelID 实例校验（绑定码仅本实例可兑，遗留无绑定码保持 type 级）；processor.activate 写 `subscription.channel_id`（rec.ChannelID 优先，回退 bot 自身实例）；activation handler 按 `channel_id` 解析实例 `bot_username` 构深链、校验实例存活；telegram config 增明文 `bot_username` + `EncodeConfig`/`DecodeUsername`。测试 `TestActivate_InstanceBinding`/`TestDecodeUsername`。
- 2026-06-26T21:42:00+08:00 p7-1 completed（交付后追加，用户验收前迭代）：`telegram.TokenHint`（前4…后4，≤8 全掩码）；handler `channelTokenHint` 解密算指纹，`channelView` 增 `token_hint`（仅 telegram）；types `ChannelInstance.token_hint`；ChannelsPage 增 Token 列（mono + title 提示）。完整 token 仍不回传。测试：`TestTokenHint` 边界 + `TestAdminChannelCRUD` 断言 hint 存在/形如 9876…、响应不含完整 token。`make web` 重新嵌入 SPA（修复 make dev 仍服务旧 UI / 旧 configure 端点的问题）。
- 2026-06-26T21:42:00+08:00 p6-1 completed：全量 `go test ./...` + `go vet ./...` + issuer 二进制构建全绿；`pnpm build`/`pnpm lint` 全绿（p5-1 已记）；issuer 二进制 smoke 跑通多实例 worker 生命周期（2 实例起 / disable 停 / SIGINT 干净退出），supervisor 增停机日志（可观测性）；新增 `docs/multi-channel-instances.md`。所有计划项 p1-1..p6-1 均 `[x]`，等待用户验收后关闭。
- 2026-06-26T21:42:00+08:00 p5-1 completed：前端 ChannelsPage 重写为多实例 CRUD（增表单 + 实例表 + 启停/Re-token/删除，删除提示级联 cancel）；api/admin.ts channels 区改 CRUD（listChannels→ChannelInstance[]、create/update/setChannelEnabled/deleteChannel）；types 增 `ChannelInstance`、PushCreate 增可选 `channel_id`；SetupPage `hasTelegram` 改 `some(telegram && configured)`。`pnpm build` + `pnpm lint` 全绿。
- 2026-06-26T21:42:00+08:00 p4-1 completed：admin channels CRUD 取代单实例 configure：`GET /channels`（列实例，`channelView` 无密钥）、`POST /channels`（建，dup name→409、telegram 加密 token + 明文 bot_username）、`POST /channels/{id}`（改名/状态/config，username-only 保留 token，`encodeChannelConfig` 复用）、`POST /channels/{id}/enable|disable`、`DELETE /channels/{id}`（D7 级联 `CancelByChannelID` + 删除）；全部 operator RBAC + 审计。移除 `/channels/{type}/configure`。测试重写 `TestAdminChannelCRUD` + RBAC viewer 拒 + 非法枚举。
- 2026-06-26T21:42:00+08:00 p3-1 completed：`PushJob.ChannelID *string`（migration `0015_pushjob_channel`：sqlite+postgres 加 nullable `channel_id`；repo 重构 `pushJobCols`/`scanPushJob`/`scanMany`）；Scheduler `Options.Route func(PushJob)(Sender,error)` 逐 job 选 sender、候选集 `ListActiveByInstance`（channel-scoped）vs `ListActiveByChannel`（legacy），路由失败 fail job；main.go push `pushRoute`（实例 token→transport，回退 default）；admin `createPushJob` 增 `channel_id` + 实例校验。测试 `TestRun_ChannelScopedRoutesToInstance`/`TestRun_RouteFailureFailsJob`。D5 落定：channel_id 可空=type 级广播，非空=单实例定向。

## 6. Validation Evidence (append-only)
- TC-1 | stack: go | command: go test ./internal/store/ -run TestMigrate -count=1 | result: pass | note: 0014 回填既有 telegram 渠道 name='default' + 订阅/激活 channel_id；新唯一键 (channel_id, channel_user_id) 允许同 user 跨实例订阅、拒绝同实例重复。
- TC-1 | stack: go | command: go build ./... && go test ./internal/store/ ./internal/worker/... ./internal/core/oauth/ ./internal/httpapi/ ./internal/e2e/ -count=1 | result: pass | note: 列名/唯一键变更后既有订阅/激活/推送/worker/e2e 全绿（迁移前后订阅可投递路径未回归）。
- TC-2 | stack: go | command: go test ./internal/store/ -run 'TestChannelConfig_CRUD\|TestSubscription_InstanceAddressing' -count=1 | result: pass | note: ChannelConfigRepo by-id CRUD（Create 同 (pool,type) 同名→ErrConflict；Get/List 按 type,name 排序；ListActive 排除 disabled；SetStatus/Delete）；SubscriptionRepo by-instance（GetByInstanceUser 按 channel_id 消歧、同 user 跨实例独立；ListActiveByInstance/CancelByChannelID 级联仅限本实例）。
- TC-3 | stack: go | command: go test ./internal/worker/telegram/ -run TestSupervisor -race -count=1 | result: pass | note: Supervisor 增实例→worker 起、停用/删→有限时间退出、token 变更→重建、每实例仅 build 一次（不重复起）、ctx 取消后全部 drain（-race 无泄漏/竞态）；env-token fallback 仅在无 DB 实例时运行。
- TC-3 | stack: go | command: go test ./internal/worker/telegram/ ./internal/e2e/ ./internal/worker/push/ -count=1 | result: pass | note: Processor 实例化（NewInstanceProcessor + lookup by (channel_id,user)）+ main.go supervisor 接线后既有 bot/e2e/push 未回归。
- TC-4 | stack: go | command: go test ./internal/worker/telegram/ -run 'TestActivate_InstanceBinding\|TestDecodeUsername' -count=1 | result: pass | note: 实例 A 绑定码被实例 B bot 拒（ErrPurpose→Invalid），被 A 接受并写 subscription.channel_id=chA；同 user 在 B 用 B 绑定码得独立订阅（不同 session_id、channel_id=chB）；两实例 /status 并存；bot_username 明文存储 + DecodeUsername 供深链。
- TC-4 | stack: go | command: go test ./internal/core/oauth/ ./internal/httpapi/ ./internal/store/ ./internal/e2e/ -count=1 | result: pass | note: CreateActivation 增 channel_id 参数 + 记录到激活码；activation handler 按 channel_id 解析实例 bot_username 构深链、校验实例 active/telegram；Consume 增 channelID 实例校验；既有激活/oauth/e2e 未回归。
- TC-5 | stack: go | command: go test ./internal/worker/push/ -run 'TestRun_ChannelScoped\|TestRun_RouteFailure' -count=1 | result: pass | note: channel-scoped job 仅投本实例订阅（ListActiveByInstance）经 Route 解析的本实例 sender，跨实例不串（senderB 零投递）；路由失败→job 置 failed、零投递；D5 落定 channel_id 可空=type 级、非空=单实例。
- TC-5 | stack: go | command: go test ./internal/worker/push/ ./internal/httpapi/ ./internal/store/ -count=1 | result: pass | note: PushJob.channel_id（domain/migration 0015/repo scan 重构）+ Scheduler Route 选 sender + 候选集按实例；admin createPushJob 增 channel_id 并校验实例存活/类型；既有 type 级推送（Route=nil）未回归。
- TC-6 | stack: go | command: go test ./internal/httpapi/ -run 'TestAdminChannelCRUD\|TestAdminRBAC_Matrix\|TestAdminF2' -count=1 | result: pass | note: operator 可创建多实例（同 type 不同 name），token 落库加密、bot_username 明文；重名→409、缺 token→400、非法 type→400；改名/启停/username-only 保留 token；DELETE 级联 cancel 订阅（D7）+ 删除实例；GET 列出实例无密钥；viewer GET 可、POST 创建→403；审计记录 create/update/delete。
- TC-7 | stack: ui | command: pnpm build (tsc -b && vite build) | result: pass | note: ChannelsPage 改为多实例 CRUD（新增表单 + 实例表：名称/类型/bot/状态 + 启停/Re-token/删除）；api/admin.ts listChannels→ChannelInstance[]、新增 create/update/setEnabled/deleteChannel；types 增 ChannelInstance + PushCreate.channel_id；SetupPage hasTelegram 改 some(active+configured)。tsc 全绿。
- TC-7 | stack: ui | command: pnpm lint (eslint .) | result: pass | note: 0 errors（2 个既有 react-refresh 警告，非本次改动文件）。
- p6-1 | stack: go | command: go test ./... | result: pass | note: 全量服务端测试全绿（含新增 store/telegram/push 测试）。
- p6-1 | stack: go | command: go vet ./... && go build ./cmd/issuer | result: pass | note: vet 无告警；issuer 二进制构建成功。
- p6-1 | stack: go | command: issuer 二进制 smoke（sqlite + 2 seeded telegram 实例 + mock chain） | result: pass | note: 日志显示 telegram-supervisor 起，逐实例 worker 起（inst-members + inst-announce）；disable inst-announce → "worker stopped reason=removed-or-disabled"（~4s 内）、inst-members 续跑；SIGINT → "all workers stopped" 干净退出（无泄漏）。新增 supervisor 停机日志（可观测性）。
- p6-1 | stack: doc | command: 新增 docs/multi-channel-instances.md | result: pass | note: 渠道实例模型 / supervisor 生命周期 / 激活 / 推送 / 删除级联 / env fallback / 关键文件，沿用既有 feature-doc 风格。
- TC-8 | stack: go | command: go test ./internal/worker/telegram/ -run TestTokenHint -count=1 | result: pass | note: ""→""、正常→1234…cret（仅露首尾4）、短(≤8)→全 • 掩码。
- TC-8 | stack: go | command: go test ./internal/httpapi/ -run TestAdminChannelCRUD -count=1 | result: pass | note: GET /channels 返回 token_hint=9876…、响应 JSON 不含完整 token、不含中段 "secret"；每个 telegram 实例都有 hint。
- TC-8 | stack: ui | command: pnpm lint + make web（重建并重新嵌入 SPA） | result: pass | note: 0 errors（2 既有警告）；ChannelsPage 增 Token 列展示 token_hint；嵌入式 dist 已用新构建覆盖（解决 make dev 仍打老 configure 端点）。

## 7. Change Requests (append-only)
- 2026-06-26 p7-1（用户验收前迭代）：放宽 C2 的"永不回传"边界——`GET /channels` 增 `token_hint`（bot token 的前4…后4），用于运维核对"是哪把钥匙 / 是否换过"。仅暴露首尾各 4 字符，完整 token 与中段 secret 仍永不回传、永不明文落库；非 telegram / 无 cipher / 无 token 时为空。范围限于读取展示，不改 token 写入路径。
- 2026-06-26 初始决策（草案，待执行期确认）：① 渠道实例可定址（稳定 channel_id + name，`(pool,type,name)` 唯一）；② 订阅唯一键改 `(channel_id, channel_user_id)`，同 user 跨实例独立订阅；③ worker supervisor 单点对账逐实例启停；④ 激活/推送绑实例；⑤ 既有单 telegram 迁移为 `default` 实例、env-token 作隐式 default；⑥ 删除默认级联 cancel 订阅（D7，可改为仅停用）；⑦ 本期只接 telegram，新平台后续单独排期。
