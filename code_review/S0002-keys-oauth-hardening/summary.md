# 多智能体评审汇总 — S0002 keys & OAuth-client 硬化

评审范围：`b9f87b8..HEAD`（本会话 8 个提交：密钥生命周期 + OAuth client 硬化），27 文件 +510/−211。
规格标准：`docs/specs/20260624T2355-S0002-ouropass-web-frontend.md`（验收 p3-1-fix2 … p6-4 → TC-3）。
评审者：**claude**、**cursor**（auto 模型）。**codex 跳过**（本地未恢复，用户指示）。

## 总体结论

**APPROVE / 无阻塞**。两位评审独立确认：PKCE「对所有客户端强制」实现正确且**闭合了 confidential 兑换的真实缺口**（`authenticateClient` 确实在 `tokenAuthCode` 中被调用、先验 PKCE 再验 secret）；密钥退役守卫、secret 再生、死字段删除与迁移均正确。无 P0/P1。

发现计数：**P0=0，P1=0，P2=3，P3=9**。
- claude 提出 4 项（独立复核全部命中真实代码）。
- cursor 提出 11 项（含 1 项我漏掉的真实破坏：dev-seed Makefile）。
- 跨智能体共识：2 项（`code_challenge_method` 未校验、`client_id` 碰撞）。

> 复选框是你的批准边界：勾选 = 同意修复。本评审只读，不改任何代码。

---

## P2 — 中

- [ ] **`server/Makefile:94` `dev-seed-client` 仍 INSERT 已删列，迁移后必失败**
  提出者: cursor · agreement: 1/2 · 复核: **确认**
  - 现状：`INSERT OR REPLACE INTO OAuthClient (...,party,...,allowed_scopes,pkce_required,...)`。迁移 0007/0008 删了这三列，`make dev-seed-client` 会报 `no such column`。
  - 影响：本地 dev 种子链路（/connect 联调）直接坏掉。属本 diff 的迁移引发的**附带破坏**（Makefile 本身未在本会话改动）。
  - 建议：更新该 INSERT 去掉三列；并补 `code_challenge`（种子客户端现在也必须走 PKCE 才能联调 /connect）。

- [ ] **缺 confidential「有 secret、无 `code_verifier`」负向测试（守不住 p6-3 的核心修补）**
  提出者: cursor · agreement: 1/2 · 复核: **确认**
  - 现状：`TestToken_AuthCodeConfidential` 仅测「带 verifier 时错 secret」，**没有**一条「confidential 省略 verifier → `ErrInvalidGrant`」的用例——而这正是本次修复的旧漏洞路径。
  - 风险：未来重构可能无声回归（confidential 再次跳过 PKCE），且无测试拦截。
  - 建议：新增用例：`eligibleCode` 拿 code 后仅以 `client_secret` 兑换（省 `CodeVerifier`），断言 `ErrInvalidGrant`。

- [ ] **`code_challenge_method` / challenge 格式未校验**
  提出者: claude, cursor · agreement: **2/2** · 复核: **确认**
  - 现状：`oauth.go:119` 只要求 `code_challenge` 非空；无 `code_challenge_method` 处理、无长度/字符校验。token 侧硬编码 S256（`pkceS256`）。
  - 风险：发 `plain`-method 或畸形 challenge 的客户端不会在 authorize 被拒，而是拿到一个**永远无法兑换**的 code（token 时才报不透明的 `invalid_grant`）；与 RFC 7636 字面不完全一致。无已知利用链（S256 实质强制）。
  - 建议：authorize 若接受 method 参数则只允许 `S256`、否则返回 `invalid_request`；可选校验 challenge 为 43 位 base64url。

## P3 — 低

- [ ] **`client_id` 生成后直接 Upsert，无碰撞检测**
  提出者: claude, cursor · agreement: **2/2** · 复核: **确认**
  - `handlers_admin_resources.go:310-325`：`"op-client-"+RandomToken(9)`（72-bit）后走 `INSERT … ON CONFLICT DO UPDATE`。极低概率碰撞会**静默覆盖**已有客户端。实际概率可忽略，但 register 语义上不应 clobber。
  - 建议：register 用 insert-only（冲突即失败/重试），或保留现状并注释为有意。

- [ ] **退役端点 `POST /keys/issuer/{kid}/retire` 无 HTTP 层测试**
  提出者: cursor · agreement: 1/2 · 复核: **确认**（仅 `keys.TestRetire` 服务级，handler 的 404/409 映射 + audit 无集成测试）
  - 建议：rotate 后对 rotating kid→200、active kid→409、未知 kid→404。

- [ ] **public 客户端 secret 再生 409 分支未测**
  提出者: cursor · agreement: 1/2 · 复核: **确认**（测试仅覆盖 confidential 成功 + 未知 404）
  - 建议：seed 一个 public client，step-up 调 `/secret`，断言 409。

- [ ] **authorize 无 `code_challenge` 的负向测试缺失**
  提出者: cursor · agreement: 1/2 · 复核: **确认**（强制逻辑在 `oauth.go:119`，但无单测守护）
  - 建议：`TestAuthorize_RequiresPKCE`，base 请求不带 challenge → `ErrInvalidRequest`。

- [ ] **`handlers_oauth.go:20` 文档注释仍写 `code_challenge?`（可选）**
  提出者: cursor · agreement: 1/2 · 复核: **确认** — 与强制 PKCE 不符，改为必填。

- [ ] **`keys_test.go:127` 注释写 `p5-1`，应为 `p5-2`**
  提出者: cursor · agreement: 1/2 · 复核: **确认** — 手动退役是 p5-2（p5-1 是延后的自动 worker）。

- [ ] **`cache:"no-store"` 应用于全部 admin GET，而非仅 JWKS**
  提出者: cursor · agreement: 1/2 · 复核: **确认（但有意为之）** — spec 只要求 JWKS 绕缓存；全局实现稍多余网络流量，功能满足 p3-1-fix2。`client.ts:31` 已注释为 intentional（admin SPA 恒取实时数据）。可保留或收窄到 `fetchJwks`。

- [ ] **`keys.Retire` 为 Get-then-SetStatus，非原子（TOCTOU）**
  提出者: claude · agreement: 1/2 · 复核: **确认** — 并发 rotate/retire 理论可竞态；owner+step-up 单操作场景风险极低。建议改状态守卫 UPDATE（`… WHERE kid=? AND status='rotating'`）。

- [ ] **`CopyButton` 在 `navigator.clipboard` 缺失时仍 toast「Copied」**
  提出者: claude · agreement: 1/2 · 复核: **确认** — 非安全上下文/旧浏览器下 `?.` 静默失败但仍提示成功。建议仅在 `writeText` resolve 后提示。

---

## 规格符合性 (Spec Compliance)

两位评审一致判定**全部满足**，证据交叉一致：

| 项 | 结论 | 证据 |
|---|---|---|
| p3-1-fix2 admin GET no-store | **满足** | `client.ts:31` 共享 `request()` 加 `cache:"no-store"`；verifier 端 `handlers_verifier.go:29` `max-age=60` 未改 |
| p3-1-fix3 JWKS status | **满足** | `admin.ts` `Jwk.status` + KeysPage Status 列 `active (signing)` |
| p3-1-fix4 Generate/Rotate 合并 | **满足** | `KeysPage` 单按钮按 `hasActiveKey` 切换 |
| p5-2 手动退役 | **满足** | `keys.Retire` 守卫 + 路由 owner+step-up + 404/409 + audit + `TestRetire`（HTTP 层测试缺，见 P3） |
| p6-1 系统生成 client_id | **满足** | `op-client-`+RandomToken(9)，请求 id 被忽略，name 必填（Upsert 碰撞见 P3） |
| p6-2 删 party + allowed_scopes | **满足** | 端到端 + 迁移 0007；二者确为死配置 |
| p6-3 强制 PKCE + 删 pkce_required | **满足** | authorize 恒要 challenge；`authenticateClient` 全员验 PKCE + confidential 叠加 secret（缺口已闭合）；迁移 0008（method 校验见 P2） |
| p6-4 copy id + regenerate secret | **满足** | `POST /oauth-clients/{id}/secret` owner+step-up + 404/409 + audit + 一次性 plaintext；secret 仍哈希存储；前端 Copy ID + 仅 confidential 显示 Regenerate |
| TC-3 页面消费契约 | **满足** | Keys/Clients 页与 admin 契约一致；列表脱敏 `ClientSecretHash`（未改） |

**Scope drift**：仅 cursor 指出的 `Makefile dev-seed-client`（见 P2#1），为会话外残留被迁移波及。其余无越界。

## 误报 / 已排除

- 无。两位评审的发现经独立复核均命中真实代码；无降级或排除项。

## 残留风险（运维须知，非缺陷）

- **破坏性行为变更**：confidential 客户端兑换现在必须**同时**带 `client_secret` 和有效 `code_verifier`；既有未做 PKCE 的集成方将开始收到 `invalid_grant`。升级前需知会接入方。库中遗留的无 challenge 未消费 code 升级后也无法兑换（预期）。
- **迁移 0007/0008** `DROP COLUMN`：modernc SQLite 3.45+/pgx 均合法，被删列无索引；store+e2e 测试实跑通过。
