# Ouro Pass Admin — 信息架构与内容编排评审（OAuth 平台视角）

> 目的：在 S0007 纯视觉重构之上，从专业 OAuth / 身份管理平台的角度，评审每个页面的**内容是否摆在对的位置**——哪些常驻内容应改用 modal / 抽屉承载、哪些页面应合并或取消。本文件只给**结论与依据**，不含实现。
> 标注：`[现成]` = 用现有 API/字段即可落地（纯前端）；`[需后端]` = 依赖新接口，超出当前范围。

---

## 0. 评判框架

参照主流 OAuth/身份平台（Auth0、Okta、WorkOS、Clerk、Keycloak、Stripe Dashboard）的两条惯例：

**(A) 导航 = 名词（资源），不是动作或一次性流程。**
顶层导航应是"可反复查看的资源集合"（Applications / Users / Roles / Connections / Logs / Keys）。一次性的"上手流程"、纯配置编辑器不该占一个常驻顶层入口。

**(B) 内容编排三分法：**
| 内容性质 | 承载方式 |
|---|---|
| 记录的**列表**（可反复浏览、是页面主体） | 页面 + 表格 |
| 单条记录的**创建 / 短表单 / 危险确认** | **Modal**（聚焦、阻断、即用即走） |
| 单条记录的**详情 / 多字段只读 / 多分区编辑** | **侧边抽屉**（保留列表上下文、信息密度高） |
| **一次性 / 偶发**的配置（单例设置、规则编辑器） | 设置区或"编辑态"，不常驻主视图 |

当前 admin 在 (A)(B) 上都有偏差：三个页面把**创建表单常驻**在列表上方，一个页面把**只读摘要 + 重型编辑器同时常驻**，且把**一次性上手清单**做成常驻顶层页。

---

## 1. 逐页结论

| 页面 | 结论 | 关键动作 |
|---|---|---|
| Dashboard | **保留**（落地页） | 吸收 Setup 清单为"未完成才显示"的上手卡 |
| Members | **保留**（核心身份名册） | 详情抽屉受限于无详情接口，暂不做 |
| Subscriptions | **保留但更名 Sessions**，弱独立性 | 加会话详情抽屉；可作为 Members 下的二级 Tab |
| Tiers | **保留为配置，但重构** | 默认只读摘要；编辑器进"编辑态/抽屉"，并与 Attestors 合并 |
| Channels | **保留** | 把常驻"添加实例"表单移入 modal/抽屉 |
| Push | **保留** | 创建已是 modal（✓）；加任务详情抽屉；修"schedule"措辞 |
| OAuth Clients | **保留**（OAuth 平台核心） | 加 client 详情抽屉（暴露现有但被藏起的 redirect/audiences/创建时间） |
| Signing Keys | **保留** | 已全 modal 化（✓）；归入"安全"区 |
| Attestors | **与 Tiers 合并**为「Eligibility」 | 把常驻"添加 attestor"表单移入 modal |
| Audit log | **保留**（合规日志） | 无需改编排 |
| Setup | **取消独立页** | 并入 Dashboard 上手卡（完成后自动隐藏） |

---

## 2. 应该合并 / 取消的页面

### 2.1 Setup → 并入 Dashboard（取消独立页）`[现成]`
Setup 现状是「签名密钥 / OAuth client / Telegram 渠道」三步上手清单，完成态由 `fetchJwks/listClients/listChannels` 推导。一旦三项就绪，它**永远是三个绿勾**——一个零信息量的常驻顶层入口，违反惯例 (A)。
**建议**：做成 Dashboard 顶部的「快速上手」卡片，**仅当存在未完成项时显示**，完成后自动消失；从侧栏移除 Setup 入口。（owner 仍可通过 Dashboard 看到。）

### 2.2 Attestors + Tiers → 合并为「Eligibility（资格）」`[现成]`
两者是**同一条流水线的上下游**：Attestors 产出命名事实（`pool:*.state` / `total_active_stake` …）→ Tier 规则消费这些事实算等级。现在拆成两个相邻页，编辑规则时看不到可用事实、配置数据源时看不到它被谁消费，认知割裂。
**建议**：合并为一页「Eligibility」，两个分区/Tab：
- **Sources（来源）** = 现 Attestors（数据源列表 + 添加）
- **Tier rules（等级规则）** = 现 Tiers（规则摘要 + 编辑）

对齐 Auth0 把 Connections 与消费它的 Rules 收在同一"认证/规则"心智下。

### 2.3 Subscriptions → 更名 Sessions，考虑并入 Members `[现成]`
Subscriptions 本质是"会员在各渠道的会话/绑定"，是 Members 的派生关系。独立顶层入口可保留，但它是**最弱的独立页**。
**建议（二选一）**：① 保持独立但更名 **Sessions**（语义更准）；② 作为 Members 的二级 Tab（`Members | Sessions`），形成"身份中心"。倾向 ②，但属中等置信，留人工定。

---

## 3. 不应常驻、应改用 Modal / 抽屉的内容

### 3.1 改 Modal（短表单 / 创建 / 确认）
- **Channels「添加 Telegram 实例」表单** `[现成]`：现为列表上方常驻 `Card`（`ChannelsPage.tsx:108`）。应改为「添加实例」按钮 → modal。列表是主体，新增是偶发动作。
- **Attestors「添加 pool_stake attestor」表单** `[现成]`：同上（`AttestorsPage.tsx:92`），改 modal。
- 已正确为 modal/确认的（无需改，作为正面样板）：OAuth Client 注册、重置 secret、Re-token、各删除确认、密钥 Rotate/Retire。

### 3.2 改侧边抽屉（详情 / 多字段 / 多分区）
这些字段**后端已在 list 响应里返回，只是当前没展示**，做抽屉零新接口：

- **OAuth Client 详情抽屉** `[现成]`：行 → 抽屉，展示 `RedirectURIs`、`AllowedAudiences`、`ClientType`、`CreatedAt`、secret 状态 + 重置/禁用动作。真实 OAuth 平台每个 application 都有详情页/抽屉；现在 redirect/audiences 被裁掉后**无处可见**，是信息缺口。
- **Push 任务详情抽屉** `[现成]`：行 → 抽屉，展示完整 `Content`、定向（tier/topic/entitlement）、`CreatedBy`、`CreatedAt`。列表只放标题，正文进抽屉。
- **Session（原 Subscription）详情抽屉** `[现成]`：展示 `Topics`、`Entitlements`、`ChannelUserID`、`LastVerifiedAt`、`CreatedAt`、`ExpiresAt`。列表给概览，抽屉给全貌。
- **Tier 规则编辑器进抽屉/编辑态** `[现成]`：Tiers 现在**同时常驻**只读摘要（`TiersPage.tsx:250`）+ 重型 builder（`:284`），等级规则在页面上出现两遍。应：默认只读摘要为主视图，「编辑规则」打开抽屉/切编辑态承载 builder + JSON 双模。降低常驻认知负荷。

### 3.3 维持页面承载（不动）
- Members / Sessions / Clients / Keys / Channels / Attestors / Audit / Push **列表本体**：是页面主体，保留表格。
- Dashboard 概览：保留。

---

## 4. 建议的合并后导航（9 → 7 入口）

```
Overview
  └─ Dashboard            （含"快速上手"卡，未完成才显示；取消独立 Setup）
Identities
  ├─ Members
  └─ Sessions             （原 Subscriptions，更名；或并为 Members 的 Tab）
Access & Rules
  ├─ OAuth Clients        （+ 详情抽屉）
  └─ Eligibility          （Attestors + Tiers 合并，Sources / Tier rules 两分区）
Delivery
  ├─ Channels            （添加实例 → modal）
  └─ Push                （+ 任务详情抽屉）
Security
  ├─ Signing Keys
  └─ Audit log
```

对照 OAuth 平台心智：OAuth Clients≈Applications、Members≈Users、Eligibility≈Connections+Roles/Rules、Signing Keys≈Settings>Keys、Audit≈Logs。

---

## 5. 落地可行性与优先级

全部建议**纯前端可落地**（合并=路由/布局；抽屉/modal=用现有 list 字段；无新端点、不破 S0007 的三条红线）。建议优先级：

- **P1（信息架构纠偏，收益最大）**：① 三处常驻创建表单 → modal（Channels/Attestors）；② Setup 并入 Dashboard 取消独立页；③ Attestors+Tiers 合并为 Eligibility。
- **P2（补回被裁的信息，零新接口）**：④ OAuth Client 详情抽屉（找回 redirect/audiences）；⑤ Tier 规则编辑进抽屉/编辑态；⑥ Push/Session 详情抽屉。
- **P3（语义/收尾）**：⑦ Subscriptions 更名/并入 Members；⑧ Push「schedule」措辞改「create」（与 Non-goal 一致）。

> 说明：本轮**不含会员详情抽屉**（`listMembers` 无详情接口、字段仅三项，`[需后端]`），故不列入。

---

## 6. 建议下一步
将以上经你确认的条目，按 immutable-spec 立 **S0008（前端 IA 与内容编排重构）** 草案执行；红线沿用 S0007（零后端、只用现有字段）。
