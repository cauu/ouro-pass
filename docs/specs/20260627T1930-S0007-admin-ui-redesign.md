# Ouro Pass Admin 前端重构 — 设计规范化与页面统一（Linear/Vercel 极简风）

Spec-ID: S0007
Status: active
Created Time: 2026-06-27T18:30:00+08:00
Start Time: 2026-06-27T19:30:00+08:00
Completion Time:
Previous Spec-ID: S0005
Closure Reason:

> 本 spec 是**纯 `web/` 表现层重构**：在不改后端、不动 `web/src/api/admin.ts` 端点面、不新增数据查询的前提下，按已评审通过的 scoped 原型（`docs/prototype/admin-redesign-scoped.html`）重做整套 Admin 后台的视觉规范与页面布局/交互。所有页面只渲染 `web/src/lib/types.ts` 中已存在的字段。北极星版（`docs/prototype/admin-redesign.html`，含搜索/排序/分页/批量/详情抽屉/图表等）是**未来功能**的参考，**不在本 spec 范围**。

## 1. Requirement Details

### Background
S0002 交付了 Admin SPA 的全部业务页（10 业务页 + Setup + Login），功能正确、契约对齐。但视觉与交互未达行业后台水准：
- **缺设计规范**：`web/src/index.css` token 极简（仅基础几色、单一 radius），无统一中性灰阶/字号层级/间距/阴影；页头说明时长时短，Tiers/Channels 出现整段文字堆砌。
- **框架层薄**：`app/Layout.tsx` 侧边栏 11 项纯平铺无分组；无顶栏、面包屑、主题切换、用户区只有一个登出图标。
- **表格不统一**：装了 `@tanstack/react-table` 却各页手写 `<table>`；空/错/载态只是一行灰字；hash 列无复制。
- **交互低于标准**：`ChannelsPage` 用浏览器原生 `prompt()`（换 token）/`confirm()`（删除）；`AttestorsPage` 用 `confirm()`（删除）；`TiersPage` 用 `↑ ↓ ✕` 纯文本当按钮。
- **表单不一致**：Channels 常驻卡片表单、Push/Clients 用弹窗；必填/校验回显缺失（zod 已装但错误未surface）。

scoped 原型已逐页对齐真实 API、删除所有未实现功能（详见 §2.4），作为本次重构的**像素级目标**。

### Scope（本 spec 覆盖，均在 `web/src/`）
- **设计 token**：`index.css` 扩展为 Linear/Vercel 极简风的语义化 token（中性灰阶、强调色、状态色、字号、间距、圆角、阴影、等宽），亮/暗双主题，沿用 Tailwind v4 `@theme inline` 桥接。
- **应用壳**：`app/Layout.tsx` 改为**分组侧边栏**（Overview / Membership / Delivery / Identity & Security / System）+ **顶栏**（面包屑 + 主题切换 + 角色/用户区 + 登出）；RBAC 过滤逻辑不变。
- **共享 UI 基元**（`ui/`）：统一 Table 样式；新增 `StatusBadge`（状态→徽章变体映射）、`CopyButton`（等宽可复制单元）、`ConfirmDialog`（Radix，替换原生 `prompt/confirm`）、`EmptyState`、`QueryState` 的骨架屏载态；`Field` 补必填标记 + RHF 错误回显。
- **12 个页面重构**：Dashboard、Members、Subscriptions、Tiers、Channels、Push、OAuth Clients、Signing Keys、Attestors、Audit、Setup、Login（+ StepUpDialog / WalletPicker 重置样式），逐页对齐 scoped 原型（§2.3 映射表）。
- **消除原生对话框**：Channels 换 token / 删除、Attestors 删除一律改 Radix 弹窗；Tiers 排序/删除改 lucide 图标按钮。
- **亮/暗主题**：顶栏切换 + 持久化（`localStorage`），`.dark` class 已被 token 支持。

### Constraints
- C1 **技术选型冻结**：React 18 + Vite 6 + TS(strict) + Tailwind v4 + Radix(shadcn 风) + TanStack Query/Table + React Hook Form + Zod + lucide-react。**不新增重依赖**（无 UI 框架/图表库/状态库）。
- C2 **零后端改动**：不改 `server/`，不新增/修改 `web/src/api/admin.ts` 任何端点或调用；页面只消费现有 query/mutation。
- C3 **只渲染已有字段**：每个页面渲染的列/字段必须存在于 `lib/types.ts`（如 `Member` 仅 `stake_credential_hash/tier/channel_type`、`Jwk` 无时间戳）。**禁止**编造列或派生需要新接口的数据。
- C4 **行为与 RBAC 不回归**：所有现有 mutation、step-up 重签、`RequireAuth`/`RequireRole`（viewer<operator<owner）、错误 toast、network guard 行为保持等价。
- C5 **可访问性与响应式**：焦点可见（`focus-visible` ring）、对话框 ESC/遮罩关闭、键盘可达；侧栏在窄屏可折叠/收起（最低保证不破版）。
- C6 **构建产物嵌入不变**：仍是 `base:/admin/` 静态 SPA，`make web` 嵌入 Go（S0002 p4-1 既有机制），不改部署形态。

### Non-goals（明确不做；属北极星版/未来 spec）
- 列表页的**搜索 / 过滤 / 排序 / 分页 / 列控制 / 行选 / 批量操作**（当前 list 端点一次性返回全量，无相关参数）。
- **会员详情抽屉**（无 `GET /members/:sch` 详情端点）、会员的 active stake / last seen 列。
- Dashboard **趋势图 / 环比 / 送达率 / 系统健康面板**（无历史/聚合/健康接口）。
- Push **投递进度条 / 重试 / 取消 / 草稿 / 定时排程**（`PushCreate` 无 `scheduled_at`，无 cancel/retry 端点）。
- Channels **订阅数列 / 测试按钮**；Clients **启用·禁用 / Redirect URIs 列**；Keys **多余统计卡 / 创建时间列**；Attestors **产出事实 / 同步进度列**。
- Setup 的**管理员管理 / Token 策略 / Pool 配置编辑器**（无对应端点；保留 S0002 既有"上线清单"）。
- 命令面板（⌘K）、CSV 导出 —— 列为可选 stretch（p4-3），不计入验收。
- 任何后端、钱包契约、token 消费端改动。

## 2. Outline Design

### 2.1 设计 token（`index.css`）
在现有 `:root` / `.dark` / `@theme inline` 结构上扩展（保持 CSS 变量 + Tailwind v4 桥接，零新依赖）：
| 类别 | token（语义名） | 备注 |
|---|---|---|
| 表面 | `--bg / --surface / --surface-2 / --surface-3 / --elevated` | 中性灰阶，亮/暗各一套 |
| 文本 | `--text / --text-2 / --text-3` | 主/次/弱三级 |
| 描边 | `--border / --border-2` | 1px 细描边 |
| 强调 | `--accent / --accent-fg / --accent-soft / --accent-text` | 沿用 oklch 紫，仅用于主操作与焦点 |
| 状态 | `success / warning / danger / info`（各 + `-soft`） | 映射 active/grace/cancelled·failed/中性 |
| 形态 | `--radius{,-sm,-lg}`、`--shadow-{sm,md,lg}`、`--font`、`--mono` | 圆角 5/7/11，字号基准 13px |

> 决策：维持 Tailwind v4 的 `@theme inline` 把 CSS 变量暴露成 `--color-*` 工具类，业务组件继续用 `bg-surface/text-muted-foreground` 等类名；新增 token 同步进 `@theme`。亮/暗仅切 `<html>` 的 `.dark`。

### 2.2 应用壳与共享基元
```text
app/Layout.tsx        # 分组侧栏(NAV 增 group 字段) + 顶栏(面包屑/主题切换/用户区)；RBAC 过滤同 S0002
app/page.tsx          # PageHeader(标题/描述/action 不变) + QueryState 增 Skeleton 载态、EmptyState 空态
ui/table.tsx          # 表头大写小字、行悬停、sticky thead、右对齐工具类（仅样式，不加排序逻辑）
ui/status-badge.tsx   # 新增：status:string → Badge variant 映射（active/grace/done→success...）
ui/copy-button.tsx    # 新增：等宽值 + 复制图标，点击 navigator.clipboard + toast
ui/confirm-dialog.tsx # 新增：Radix Dialog 封装的危险确认（标题/描述/确认文案/onConfirm），替换原生 confirm/prompt
ui/empty-state.tsx    # 新增：图标 + 标题 + 说明 + 可选 action
ui/field.tsx          # 补必填星标 + RHF error 文案插槽
```

### 2.3 页面 → 文件 → 改造要点（对齐 scoped 原型）
| 页面 | 文件 | 改造要点（只渲染现有字段） |
|---|---|---|
| Dashboard | `features/dashboard/DashboardPage.tsx` | 3 张统计卡（members / active subs / JWKS）去 delta/spark；等级分布由 `listMembers` 客户端按 tier 聚合 |
| Members | `features/members/MembersPage.tsx` | 表 = hash(可复制) / tier(StatusBadge) / channel / 撤销(step-up)；去抽屉/批量/多余列 |
| Subscriptions | `features/subscriptions/SubscriptionsPage.tsx` | 表 = hash / channel / tier / status / expires / cancel；去 toolbar/分页 |
| Tiers | `features/tiers/TiersPage.tsx` | 保留 builder/JSON 双模；`↑↓✕`→lucide 图标按钮；去"命中数"等无来源标记 |
| Channels | `features/channels/ChannelsPage.tsx` | 表去订阅数/测试列；换 token / 删除改 `ConfirmDialog`（消除 `prompt/confirm`）|
| Push | `features/push/PushPage.tsx` | 列表 = title/channel/target/status/scheduled；创建弹窗去排程/草稿，保留 title/content/channel_id/tier/topic/entitlement |
| OAuth Clients | `features/clients/ClientsPage.tsx` | 表 = client(name+id+copy) / type / audiences / status / 重置secret(confidential)；去启停/Redirect 列 |
| Signing Keys | `features/keys/KeysPage.tsx` | 单一 JWKS 状态卡 + 表(kid/status/kty/crv/alg/retire)；去创建时间列 |
| Attestors | `features/attestors/AttestorsPage.tsx` | 表 = label/kind/pool·network/status/启停·删除；删除改 `ConfirmDialog` |
| Audit | `features/audit/AuditPage.tsx` | 表 = time/actor/action/target/ip；去结果列/过滤 |
| Setup | `features/setup/SetupPage.tsx` | 保留 S0002 上线清单语义，按新 token 重排样式 |
| Login | `features/auth/{LoginPage,StepUpDialog,WalletPicker}.tsx` | 按新 token 重置样式；钱包签名/step-up 逻辑不动 |

### 2.4 与 scoped 原型的对应
本 spec 的页面结构、列集合、操作集合**逐项等同** `docs/prototype/admin-redesign-scoped.html`。原型中每处带 HTML 注释标注了对应端点/字段来源（如 `<!-- listMembers 仅返回 ... -->`），实现时以该注释 + `lib/types.ts` 为准。

### 2.5 Risk and rollback
- R1 **token 大改引起全站视觉回归** → 先落 token + 壳（p1），逐页改并与原型对照（p3 每页独立可提交、独立回滚）。
- R2 **误引入需要新接口的"改进"** → C2/C3 红线；TC-4/TC-7 用 grep + 类型核对守门（页面字段必须能在 `lib/types.ts` 找到）。
- R3 **消除原生对话框时漏改** → TC-5 用 `grep -r "prompt(\|confirm("` 全仓为 0 收口。
- R4 **暗色对比度/可达性不足** → p4 专门一轮，对照 WCAG AA 抽检关键文本/状态色。
- Rollback：未发布按 working tree；已提交用 `git revert`，forward-only（遵循 immutable-spec 规则）。

## References
- docs/prototype/admin-redesign-scoped.html — **本 spec 的像素级目标**（当前实现范围版，逐页标注端点来源）
- docs/prototype/admin-redesign.html — 北极星参考（未来功能，非本 spec 范围）
- docs/specs/completed/20260624T2355-S0002-ouropass-web-frontend.md — 前端基线（页面/契约/RBAC/嵌入机制）
- docs/specs/completed/20260626T2142-S0005-multi-channel-instances.md — 渠道实例模型（Channels 页字段来源）
- docs/specs/completed/20260626T1352-S0006-onchain-credential-abstraction.md — Attestor/Tiers 事实模型
- web/src/api/admin.ts、web/src/lib/types.ts — **API 端点面与 wire 字段（实现唯一字段依据）**
- web/src/index.css、web/src/app/Layout.tsx、web/src/ui/*、web/src/features/*/* — 待改造对象

## 3. Execution Plan
### p1 设计系统与应用壳
- [x] p1-1 `index.css` 扩展语义化 token（中性灰阶/状态色/字号/间距/圆角/阴影/等宽，亮+暗），同步 `@theme inline`；不改业务组件类名前提下全站换肤（TC-1, TC-2）。
- [x] p1-2 `Layout.tsx` 分组侧栏（NAV 增 `group` 字段，按 Overview/Membership/Delivery/Identity&Security/System 分组渲染分组标题）+ 顶栏（面包屑 + 主题切换 + 用户/角色 + 登出）；RBAC 过滤与路由不变（TC-3, TC-7）。

### p2 共享 UI 基元
- [x] p2-1 `ui/table.tsx` 样式统一 + 新增 `ui/status-badge.tsx`、`ui/copy-button.tsx`；各表格 hash/kid 等改 `CopyButton`、状态改 `StatusBadge`（TC-2, TC-4）。
- [x] p2-2 新增 `ui/confirm-dialog.tsx`（Radix），替换 `ChannelsPage` 的 `prompt`(换 token)/`confirm`(删除) 与 `AttestorsPage` 的 `confirm`(删除)；全仓 `prompt(`/`confirm(` 归零（TC-5）。
- [x] p2-3 `QueryState` 增骨架屏载态 + 新增 `ui/empty-state.tsx` 空态；`ui/field.tsx` 补必填星标 + RHF error 回显（TC-6）。

### p3 页面逐页重构（对齐 scoped 原型，每页独立可提交）
- [x] p3-1 Dashboard：3 统计卡 + 等级分布（客户端聚合 `listMembers`）（TC-4, TC-7）。
- [x] p3-2 Members：精简表 + 撤销 step-up（TC-4, TC-5, TC-7）。
- [x] p3-3 Subscriptions：精简表 + 取消（TC-4, TC-7）。
- [x] p3-4 Tiers：图标按钮替换文本按钮，保留 builder/JSON 双模与 describe 渲染（TC-4, TC-7）。
- [ ] p3-5 Channels：表精简 + 添加/换 token/删除走对话框（TC-4, TC-5, TC-7）。
- [ ] p3-6 Push：列表精简 + 创建弹窗去排程/草稿（TC-4, TC-7）。
- [ ] p3-7 OAuth Clients：表精简 + 注册向导 + 一次性 secret + 重置 secret（TC-4, TC-7）。
- [ ] p3-8 Signing Keys：单状态卡 + 表（含 retire）（TC-4, TC-7）。
- [ ] p3-9 Attestors：表精简 + 删除走 `ConfirmDialog`（TC-4, TC-5, TC-7）。
- [ ] p3-10 Audit：五列只读表（TC-4, TC-7）。
- [ ] p3-11 Setup：上线清单按新 token 重排（语义不变）（TC-4, TC-7）。
- [ ] p3-12 Login / StepUpDialog / WalletPicker 重置样式（逻辑不动）（TC-2, TC-7）。

### p4 收尾
- [ ] p4-1 亮/暗主题切换 + `localStorage` 持久化；首屏无闪烁（TC-2）。
- [ ] p4-2 可达性与响应式：`focus-visible` ring、对话框 ESC/遮罩关闭、窄屏侧栏收起、关键文本/状态色 AA 对比抽检（TC-2, TC-6）。
- [ ] p4-3 **（可选 stretch，不计验收）** 命令面板 ⌘K（仅跳转 + 已有操作入口，零新接口）。

### p5 验收
- [ ] p5-1 全量校验：`pnpm typecheck && pnpm lint && pnpm test && pnpm build` 绿；全仓 `prompt(`/`confirm(` = 0；逐页字段对照 `lib/types.ts` 与 scoped 原型（TC-1..TC-7 汇总）。

## 4. Test and Acceptance Criteria
- TC-1 **构建/类型/静态检查**：`pnpm typecheck`、`pnpm lint`、`pnpm test`、`pnpm build` 全绿，无新依赖进 `package.json`（diff 核对）。
- TC-2 **设计系统**：新 token 生效；亮色与暗色两套均正确渲染（顶栏切换 + 持久化）；强调色仅出现在主操作/焦点。
- TC-3 **壳与导航**：侧栏按 5 组渲染并保留 RBAC 过滤（viewer 看不到 operator/owner 项）；顶栏面包屑随路由更新；路由表与 S0002 一致。
- TC-4 **字段忠实**：每个页面渲染的列/字段都能在 `lib/types.ts` 找到对应；无编造列（人工逐页对照 scoped 原型注释 + 类型）。
- TC-5 **无原生对话框**：`grep -rn "prompt(\|confirm(" web/src` 结果为 0；Channels 换 token/删除、Attestors 删除均走 Radix 对话框；危险操作仍走 step-up（行为不变）。
- TC-6 **状态完备**：列表/表单具备载态（骨架屏）、空态（EmptyState）、错误态；表单必填有星标、校验错误可见。
- TC-7 **行为零回归**：现有 query/mutation、step-up、RBAC 守卫、错误 toast、network guard 行为等价；`api/admin.ts` 无改动（git diff 为空）；端到端手测各页 CRUD 与 S0002 一致。
- Pass/fail：每 item 仅在其映射 TC 全 pass 且证据 append 后标 `[x]`；p5-1 为总收口。

## 5. Execution Log (append-only)
- 2026-06-27 S0007 草案创建（draft）：基于已评审通过的 scoped 原型（`docs/prototype/admin-redesign-scoped.html`）起草，确立"纯 web/ 表现层重构、零后端、只渲染现有字段"三条红线；范围=token + 应用壳 + 共享基元 + 12 页重构 + 消除原生对话框 + 亮/暗主题；北极星功能（搜索/排序/分页/批量/详情/图表）列为 Non-goals。尚未执行。

- 2026-06-27 S0007 激活（active）：draft→`docs/specs/20260627T1930-S0007-admin-ui-redesign.md`，记 Start Time 与 Previous Spec-ID=S0005。环境说明：本沙箱 registry 受限且 node_modules 携带 macOS 原生二进制（esbuild darwin-arm64 + 缺 rollup linux），`vite build`/`vitest` 无法在沙箱运行；沙箱内以 `tsc -b --noEmit`(typecheck) + `eslint .`(lint) 为门禁，`pnpm build`/`pnpm test`（TC-1）由宿主/CI 复核（Exception #3：外部阻塞）。

- 2026-06-27 p1-1 完成：`index.css` 扩展语义 token——新增 `surface/surface-strong`、`warning/info` 及各色 `-soft` 软背景、`border-strong`、`ring`、`primary-soft`，并在 `.dark` 给出对应暗色值；全部经 `@theme inline` 暴露为 `--color-*`，业务组件继续用 Tailwind 类名。既有 token（background/foreground/card/primary/muted/border/destructive/success）保持不变，零破坏。radius 收紧到 0.5rem，body 字体加 Inter 与抗锯齿。

- 2026-06-27 p1-2 完成：`app/Layout.tsx` 重做应用壳——侧栏按 5 组渲染（Overview / Membership / Delivery / Identity & Security / System），分组标题小字大写；NavLink active 用 `bg-muted` 高亮；owner-only 项加锁图标；RBAC 过滤改为「组内按 rank 过滤、空组不渲染」，与原 roleRank 守卫等价。新增顶栏（面包屑=当前路由 group/label + 主题切换）；侧栏底部保留角色徽章 + 头像缩写 + 登出。主题 useTheme（toggle .dark + localStorage 持久化，mount 回读）。路由表与 RBAC 不变。

- 2026-06-27 p2-1 完成：统一表格与状态/复制基元。`ui/table.tsx`——外框 `bg-card shadow-sm`、表头 `bg-surface` + `TH` 改 11px 大写字距、`TR` 悬停过渡、`Table` 新增可选 `footer` 槽（计数行落在边框内）。`ui/badge.tsx` 增 `warning`/`info` 两个变体（用新 token）。新增 `ui/status-badge.tsx`（status 字符串→变体单一映射 + 圆点，取代各页散落的 statusVariant），`ui/copy-button.tsx`（等宽值 + 悬停复制图标，clipboard + toast）。均为新增/兼容改造，未改调用方。

- 2026-06-27 p2-2 完成：新增 `ui/confirm-dialog.tsx`（`ConfirmDialog` 危险确认 + `PromptDialog` 单值输入，均为 Radix 触发式、await 异步 onConfirm、pending 态、失败转 toast）。替换 `ChannelsPage` 的 Re-token（原 prompt）→ PromptDialog、Delete（原 confirm）→ ConfirmDialog；`AttestorsPage` 的 Delete（原 confirm）→ ConfirmDialog；mutation 改 mutateAsync 以便对话框等待。全仓原生 prompt/confirm 调用归零。

- 2026-06-27 p2-3 完成：状态完备化。`app/page.tsx` 的 `QueryState` 载态改骨架屏（5 行表格占位）、错误态改带图标的告警卡、空态改 `EmptyState`（新增 `emptyTitle`）；`PageHeader` 标题字号/字距精修、description 放宽 ReactNode。新增 `ui/skeleton.tsx`、`ui/empty-state.tsx`。`ui/field.tsx` 增 `required` 星标。均向后兼容（既有 QueryState/Field 调用不变）。

- 2026-06-27 p3-1 完成：Dashboard 对齐 scoped。3 张统计卡（Members=listMembers 行数 / Active subscriptions=status==active 计数 / Signing keys=JWKS 计数 + StatusBadge healthy）改用 Skeleton 载态替代「…」；新增「Tier distribution」卡，由会员名册客户端按 tier 聚合（Member 已含 tier 字段，零新接口）渲染条形分布。删除北极星图表/环比/送达率/系统健康。

- 2026-06-27 p3-2 完成：Members 精简表对齐 scoped——列=Stake credential(CopyButton 复制全量 hash + short 展示) / Tier(Badge capitalize) / Channel / Actions(StepUpDialog 撤销，operator+，行为不变)；Table footer 显示会员计数；QueryState 空态用 EmptyState 文案。仅渲染 listMembers 的三字段，无详情抽屉/批量/多余列。

- 2026-06-27 p3-3 完成：Subscriptions 对齐 scoped——列=Stake credential(CopyButton) / Channel / Tier(Badge) / Status(StatusBadge 取代本地 statusVariant) / Expires(fmtTime) / Cancel(active 时可用，行为不变)；Table footer 计数；空态 EmptyState。删搜索/状态分段/导出/分页/绑定时间列。

- 2026-06-27 p3-4 完成：Tiers 交互精修——将 builder 中的纯文本按钮 ↑/↓/✕ 替换为 lucide 图标按钮（ArrowUp/ArrowDown/X，h-7 w-7 + title 提示），「Add condition / Add rule」加 Plus 图标。builder/JSON 双模、describe() 只读摘要、ADA⇄lovelace 与规则序列化逻辑全部保持不变（scoped 原型的「命中数」本就不在真实 TiersPage 中，无需删除）。

## 6. Validation Evidence (append-only)
- （待执行后按 `TC-<n> | stack: ui|node | command: ... | result: pass|fail | note: ...` 追加）

- TC-1 | stack: node | command: tsc -b --noEmit | result: pass | note: p1-1 后 typecheck 绿（CSS 改动不影响 TS）
- TC-1 | stack: node | command: eslint . | result: pass | note: 0 error / 2 既有 warning
- TC-2 | stack: ui | command: manual review index.css | result: pass | note: 亮/暗双套 token 完整，旧 token 名保留，新增状态色经 @theme 暴露（bg-warning/text-info/bg-success-soft 等可用）；vite build 由宿主复核

- TC-1 | stack: node | command: tsc -b --noEmit | result: pass | note: p1-2 后 typecheck 绿
- TC-1 | stack: node | command: eslint src/app/Layout.tsx | result: pass | note: 0 problem
- TC-3 | stack: ui | command: manual review Layout.tsx | result: pass | note: 5 组导航 + 组内 rank 过滤(空组隐藏)；面包屑随路由；顶栏主题切换；路由 to 与 App.tsx 一致；宿主复核视觉

- TC-1 | stack: node | command: tsc -b --noEmit | result: pass | note: p2-1 后 typecheck 绿
- TC-1 | stack: node | command: eslint src/ui/{badge,status-badge,copy-button,table}.tsx | result: pass | note: 0 problem
- TC-2 | stack: ui | command: manual review | result: pass | note: StatusBadge 映射覆盖 active/grace/cancelled/failed/disabled/rotating/syncing 等；CopyButton 复制全量值

- TC-1 | stack: node | command: tsc -b --noEmit | result: pass | note: p2-2 后 typecheck 绿（onConfirm 放宽为 Promise<unknown>|void）
- TC-5 | stack: node | command: grep -rnE '[^a-zA-Z](prompt|confirm)\(' src | grep -v // | result: pass | note: 代码中原生 prompt/confirm 调用 = 0（仅余文档注释中的词）

- TC-1 | stack: node | command: tsc -b --noEmit + eslint | result: pass | note: p2-3 后 typecheck/lint 绿
- TC-6 | stack: ui | command: manual review | result: pass | note: QueryState 三态(骨架/告警/EmptyState)；Field required 星标可用；宿主复核渲染

- TC-1 | stack: node | command: tsc -b --noEmit + eslint DashboardPage | result: pass | note: p3-1 绿
- TC-4 | stack: ui | command: 字段对照 lib/types.ts | result: pass | note: 仅用 Member.tier / Subscription.Status / Jwk[]；tier 分布客户端聚合，无新接口

- TC-1 | stack: node | command: tsc -b --noEmit + eslint MembersPage | result: pass | note: p3-2 绿
- TC-4 | stack: ui | command: 字段对照 | result: pass | note: 仅 stake_credential_hash/tier/channel_type；撤销走既有 revokeMember+step-up
- TC-7 | stack: ui | command: review | result: pass | note: canRevoke 角色门禁与 StepUpDialog 流程与 S0002 等价

- TC-1 | stack: node | command: tsc -b --noEmit + eslint SubscriptionsPage | result: pass | note: p3-3 绿
- TC-4 | stack: ui | command: 字段对照 | result: pass | note: 仅 Subscription 的 StakeCredentialHash/ChannelType/Tier/Status/ExpiresAt；cancel 走既有端点

- TC-1 | stack: node | command: tsc -b --noEmit + eslint TiersPage | result: pass | note: p3-4 绿
- TC-4 | stack: ui | command: review | result: pass | note: 仅改按钮表现层；规则数据流(getPool/setTierRules)未动
- TC-7 | stack: ui | command: review | result: pass | note: builder/JSON 切换、保存、序列化行为等价

## 7. Change Requests (append-only)
- （无）
