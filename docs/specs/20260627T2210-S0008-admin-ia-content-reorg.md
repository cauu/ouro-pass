# Ouro Pass Admin 前端信息架构与内容编排重构（IA / modal / 抽屉）

Spec-ID: S0008
Status: active
Created Time: 2026-06-27T22:10:00+08:00
Start Time: 2026-06-27T22:10:00+08:00
Completion Time:
Previous Spec-ID: S0007
Closure Reason:

> 本 spec 在 S0007（纯视觉规范化，已 delivered）之上，按已评审通过的 `docs/prototype/admin-ia-content-review.md` 结论，做**纯 `web/` 表现层的信息架构（IA）与内容编排重构**：把摆错位置的内容（常驻创建表单、双重常驻的规则编辑器、一次性上手清单常驻顶层页）改到合适的承载方式（modal / 抽屉 / 上手卡），并合并/取消冗余页面。沿用 S0007 三条红线：**零后端、不改 `web/src/api/admin.ts`、只渲染 `lib/types.ts` 已有字段**。

## 1. Requirement Details

### Background
S0007 交付了统一的视觉规范与页面布局，但 `admin-ia-content-review.md`（OAuth/身份平台视角评审）指出 IA 与内容编排仍有偏差：
- **导航 = 名词（资源）**惯例被破坏：Setup 是一次性上手清单却占常驻顶层入口（永远三个绿勾，零信息量）。
- **同一流水线被拆页**：Attestors 产出命名事实 → Tier 规则消费事实，却拆成两个相邻页，认知割裂。
- **创建表单常驻列表上方**：Channels / Attestors 把"添加"表单常驻为 `Card`，而列表才是主体、新增是偶发动作。
- **规则编辑器与只读摘要同时常驻**：Tiers 页等级规则出现两遍（摘要 + 重型 builder）。
- **被裁的信息无处可见**：OAuth Client 的 `RedirectURIs/AllowedAudiences/CreatedAt`、Push 的完整 `Content/CreatedBy` 等后端已返回的字段，S0007 精简后无承载。

### Scope（本 spec 覆盖，均在 `web/src/`；经用户确认 = 全部 P1+P2+P3，Subscriptions 保持现状）
- **P1 信息架构纠偏**
  - Setup **取消独立页**：完成态逻辑（`hasKey/hasClient/hasTelegram`，由 `fetchJwks/listClients/listChannels` 推导）迁为 Dashboard 顶部「快速上手」卡，**仅当有未完成项时显示**；移除 `/setup` 路由与侧栏入口。
  - Attestors + Tiers **合并为「Eligibility」**一页：两分区 **Sources**（原 Attestors：数据源列表 + 添加）/ **Tier rules**（原 Tiers：规则摘要 + 编辑）。
  - Channels「添加 Telegram 实例」、Attestors（→Eligibility>Sources）「添加 attestor」**常驻表单改 modal**（按钮触发）。
  - 侧栏分组按评审 §4 重排：**Overview / Identities / Access & Rules / Delivery / Security**；RBAC 过滤逻辑不变。
- **P2 补回被裁信息（零新接口，用现有 list 字段）**
  - **OAuth Client 详情抽屉**：行 → 右侧抽屉，展示 `RedirectURIs / AllowedAudiences / ClientType / Status / CreatedAt` + secret 状态（`ClientSecretHash` 有无）+ 重置 secret 动作（沿用现有 step-up）。
  - **Push 任务详情抽屉**：行 → 抽屉，展示完整 `Content`、定向（`TargetTier/TargetTopic/RequiredEntitlement`）、`Status/ScheduledAt/CreatedBy/CreatedAt`。
  - **Tier 规则编辑器进抽屉/编辑态**：Eligibility>Tier rules 默认只读摘要为主视图，「Edit rules」打开抽屉承载 builder + JSON 双模（消除"同一规则常驻两遍"）。
- **P3 语义收尾**
  - Push「schedule」措辞改「create」（toast / 按钮 / 列名，与 S0007 Non-goal「无排程」一致）。

### Constraints（沿用 S0007）
- C1 **技术选型冻结 / 不新增依赖**：抽屉与 modal 复用已装的 `@radix-ui/react-dialog`；分区切换用本地 state（不引入新库）。
- C2 **零后端改动**：不改 `server/`，不改 `web/src/api/admin.ts` 任何端点或调用。
- C3 **只渲染已有字段**：抽屉/卡片渲染的字段必须存在于 `lib/types.ts`（已核对 OAuthClient / PushJob 字段齐备）。**禁止**编造列或需要新接口的数据。
- C4 **行为与 RBAC 不回归**：所有现有 mutation、step-up、`RequireAuth`/`RequireRole`（viewer<operator<owner）、错误 toast、network guard 等价；Eligibility 取 operator（= 原 Attestors/Tiers）。
- C5 **可访问性与响应式**：抽屉/modal 支持 ESC + 遮罩关闭、焦点可见；沿用 S0007 窄屏栅格不破版。
- C6 **构建产物嵌入不变**：仍是 `base:/admin/` 静态 SPA，`make web` 嵌入 Go，不改部署形态。

### Non-goals（明确不做）
- **Subscriptions 更名 Sessions / 并入 Members / Session 详情抽屉** —— 经用户确认**本期保持现状**（评审 §2.3 留待以后）。
- **会员详情抽屉** —— `listMembers` 无详情端点、字段仅三项（`[需后端]`）。
- 任何后端、新端点、新字段、钱包契约改动。
- 列表页搜索/排序/分页/批量/列控制（继承 S0007 Non-goal，仍属北极星版）。
- 命令面板 ⌘K、CSV 导出。

## 2. Outline Design

### 2.1 路由与导航（`App.tsx` + `app/Layout.tsx`）
- 删除 `/setup` 路由；`/attestors`、`/tiers` → `Navigate` 重定向到 `/eligibility`（保旧链接不 404）。
- 新增 `/eligibility`（`RequireRole min="operator"`）→ `features/eligibility/EligibilityPage.tsx`。
- 侧栏 `NAV_GROUPS` 重排（RBAC `min` 不变）：
```text
Overview        → Dashboard(viewer)
Identities      → Members(viewer), Subscriptions(viewer)        # 保持现状，不更名
Access & Rules  → OAuth Clients(owner), Eligibility(operator)
Delivery        → Channels(operator), Push(operator)
Security        → Signing Keys(owner), Audit log(owner)
```
- 面包屑沿用"组/页"逻辑；空组隐藏逻辑不变。

### 2.2 共享基元（`ui/`）
```text
ui/drawer.tsx   # 新增：基于 @radix-ui/react-dialog 的右侧抽屉
                #   导出 Drawer/DrawerTrigger/DrawerContent/DrawerHeader/DrawerTitle/
                #   DrawerDescription/DrawerClose；内容从右滑入，ESC/遮罩关闭、焦点环
```
- modal 直接复用现有 `ui/dialog.tsx`（Channels/Eligibility 创建表单）。

### 2.3 页面改造要点
| 页面/文件 | 改造 | 字段来源 |
|---|---|---|
| `features/dashboard/DashboardPage.tsx` | 顶部加「快速上手」卡：吸收 Setup 三步完成态，仅未完成时显示；全完成自动隐藏 | `fetchJwks/listClients/listChannels` |
| `features/eligibility/EligibilityPage.tsx`（新） | 合并 Attestors + Tiers：Sources（列表 + 添加 modal）/ Tier rules（只读摘要 + Edit 抽屉） | `Attestor`、`PoolInfo.tier_rules`、`TierRule/TierCondition` |
| `features/channels/ChannelsPage.tsx` | 常驻"添加实例"`Card` → 「Add instance」按钮 + Dialog | `ChannelInstance` |
| `features/clients/ClientsPage.tsx` | 行 → 详情抽屉（找回 redirect/audiences/createdAt/secret 状态）；保留注册向导 + 重置 secret | `OAuthClient` |
| `features/push/PushPage.tsx` | 行 → 任务详情抽屉；「schedule」措辞改「create」 | `PushJob` |
| `App.tsx` / `app/Layout.tsx` | 路由 + 导航重排（§2.1） | — |
| 删除：`features/setup/SetupPage.tsx`、`features/attestors/AttestorsPage.tsx`、`features/tiers/TiersPage.tsx` | 逻辑迁入 Dashboard / Eligibility | — |

> 删除旧页文件属"前向变更"：内容迁移到新承载，非历史回退。

### 2.4 Risk and rollback
- R1 **路由/RBAC 回归** → p1 改路由后立即对照 `RequireRole min`；TC-2/TC-3 守门。
- R2 **合并 Eligibility 丢逻辑**（builder/JSON 序列化、ADA⇄lovelace、describe） → 整段搬运不改算法；TC-6 行为核对。
- R3 **抽屉/modal 误引入新依赖** → C1 红线；复用 `@radix-ui/react-dialog`。
- R4 **抽屉字段编造** → C3；TC-4 对照 `lib/types.ts`。
- Rollback：未发布按 working tree；已提交用 `git revert`，forward-only。

## References
- docs/prototype/admin-ia-content-review.md — **本 spec 的结论与依据**（逐页 IA 评审 + §4 合并后导航 + §5 优先级）
- docs/specs/completed/20260627T1930-S0007-admin-ui-redesign.md — 前置视觉重构（token/壳/基元/RBAC/嵌入机制）
- web/src/api/admin.ts、web/src/lib/types.ts — **API 端点面与 wire 字段（唯一字段依据）**
- web/src/App.tsx、web/src/app/Layout.tsx、web/src/features/*、web/src/ui/* — 待改造对象

## 3. Execution Plan

### p1 信息架构纠偏（P1）
- [ ] p1-1 Channels「添加实例」常驻表单 → 「Add instance」按钮 + Dialog（列表为主体）（TC-1, TC-5, TC-6）。
- [ ] p1-2 Setup 取消独立页：Dashboard 顶部「快速上手」卡（仅未完成显示，全完成隐藏）；移除 `/setup` 路由 + 侧栏入口 + 删 `SetupPage.tsx`（TC-1, TC-2, TC-3, TC-6）。
- [ ] p1-3 新增 `features/eligibility/EligibilityPage.tsx`：搬运 Attestors（Sources）+ Tiers（Tier rules）逻辑为两分区；新增 `/eligibility`(operator) 路由，`/attestors`、`/tiers` 重定向；删 `AttestorsPage.tsx`/`TiersPage.tsx`（TC-1, TC-2, TC-3, TC-4, TC-6）。
- [ ] p1-4 侧栏 `NAV_GROUPS` 重排为 Overview / Identities / Access & Rules / Delivery / Security；RBAC 过滤不变（TC-2, TC-6）。
- [ ] p1-5 Eligibility>Sources「添加 attestor」表单 → Dialog（接 p1-3）（TC-1, TC-5, TC-6）。

### p2 补回被裁信息（P2，零新接口）
- [ ] p2-1 新增 `ui/drawer.tsx`（Radix Dialog 右侧抽屉基元；ESC/遮罩关闭、焦点环、零新依赖）（TC-1, TC-7）。
- [ ] p2-2 OAuth Client 详情抽屉：行→抽屉，展示 `RedirectURIs/AllowedAudiences/ClientType/Status/CreatedAt` + secret 状态 + 重置动作（TC-1, TC-4, TC-5, TC-6）。
- [ ] p2-3 Push 任务详情抽屉：行→抽屉，展示完整 `Content`/定向/`CreatedBy/CreatedAt/ScheduledAt/Status`（TC-1, TC-4, TC-5）。
- [ ] p2-4 Eligibility>Tier rules 编辑器进抽屉：默认只读摘要，「Edit rules」打开抽屉承载 builder + JSON 双模（消除常驻双份）（TC-1, TC-5, TC-6）。

### p3 语义收尾（P3）
- [ ] p3-1 Push「schedule」措辞改「create」（toast/按钮/列名）（TC-1, TC-6）。

### p4 验收
- [ ] p4-1 全量校验：`pnpm typecheck && pnpm lint && pnpm test && pnpm build` 绿；`api/admin.ts` git diff 空；无新依赖；路由/RBAC/字段逐项对照（TC-1..TC-7 汇总）。

## 4. Test and Acceptance Criteria
- TC-1 **构建/类型/静态检查**：`pnpm typecheck`、`pnpm lint`、`pnpm test`、`pnpm build` 全绿，`package.json` 无新依赖（diff 核对）。
- TC-2 **导航 IA**：侧栏按 5 组（Overview/Identities/Access & Rules/Delivery/Security）渲染；无 Setup 独立入口；Eligibility 含 Sources + Tier rules 两分区；RBAC 过滤后各角色可见项与 S0007 等价（viewer 见 Dashboard/Members/Subscriptions；operator 增 Eligibility/Channels/Push；owner 增 Clients/Keys/Audit）。
- TC-3 **路由**：`/setup` 移除；`/attestors`、`/tiers` 重定向到 `/eligibility`；其余路由与 `basename:/admin` 不变；deep-link `/eligibility` 在 operator 可达、viewer 被守卫拦截。
- TC-4 **字段忠实**：Client/Push 抽屉与 Dashboard 上手卡渲染的字段均可在 `lib/types.ts` 找到（`OAuthClient.RedirectURIs/AllowedAudiences/CreatedAt/ClientSecretHash`、`PushJob.Content/CreatedBy/CreatedAt/...`）；无编造列、无新接口。
- TC-5 **内容编排**：Channels / Eligibility>Sources 添加表单为 modal（列表上方不再常驻表单）；Client / Push 详情走抽屉；Tier 编辑走抽屉（只读摘要不再与 builder 同时常驻）。
- TC-6 **行为零回归**：现有 query/mutation、step-up（撤销/取消/换 token/删除/注册 client/重置 secret/rotate/retire/保存 tier rules/增删 attestor）、RBAC 守卫、错误 toast、network guard 等价；`api/admin.ts` git diff 为空；Setup 完成态逻辑等价迁移。
- TC-7 **a11y/响应式**：抽屉/modal 支持 ESC + 遮罩关闭、焦点可见；窄屏不破版（沿用 S0007 抽屉栅格）。
- Pass/fail：每 item 仅在其映射 TC 全 pass 且证据 append 后标 `[x]`；p4-1 为总收口。

## 5. Execution Log (append-only)
- 2026-06-27 S0008 创建并激活（active）：前置 S0007 已 delivered；依据 `admin-ia-content-review.md`，经用户确认范围=全部 P1+P2+P3、Subscriptions 保持现状。red lines 沿用 S0007（零后端/只用现有字段/RBAC 不回归）。

## 6. Validation Evidence (append-only)
- （待执行后按 `TC-<n> | stack: ui|node | command: ... | result: pass|fail | note: ...` 追加）

## 7. Change Requests (append-only)
- （无）
