# 评审汇总 — S0001 Ouro Pass Issuer 后端

参与评审 (Reviewers): **claude (subagent)**、**cursor (auto)**；codex 本次跳过（用户告知不可用）。主 Claude 已对照真实代码独立复核。
评审范围 (Scope): 场景 1（spec 全量变更）· 整个 `server/` @ HEAD `60b46f8`（`19da9ae..HEAD -- server/`，75 个非测试文件 ~6062 LOC）· 规格标准: `docs/specs/20260623T0041-S0001-poolops-issuer-backend.md`
总体结论 (Overall): **REQUEST_CHANGES**（两个评审者一致）
问题计数: P0 **1** · P1 **6** · P2 **8** · P3 **9**
交叉确认: 两个 agent 共同提出 **4** 项 / 总 24 项；其余为单 agent 提出但经 Claude 对照代码复核。

> 勾选 `[x]` = 该问题“批准修复”——这是人工审批边界。本 skill 只评审、只报告、**不改任何代码**。
> `提出者` = 提出该问题的 agent；`agreement` = 几个评审者命中；`复核` = Claude 对照真实代码的二次确认。
> 多 agent 共同提出的问题排在各等级最前，置信度最高。
> 架构正面评价：functional-core 规则引擎纯函数、crypto 包隔离、**全量参数化 SQL（无注入）**、fail-closed 装配、**COSE/CIP-8 信任根实现正确（无验签绕过）**、AES-GCM 字段加密正确、JWKS 不泄私钥。这些经双方+复核确认。

---

## P0 — 严重

- [ ] **[store/repo_authnonce.go:69, repo_authorizationcode.go:55, repo_activationcode.go:58]** 一次性 Consume 缺“比较并交换(CAS)”→ PostgreSQL 上可双花/重放 — `提出者: claude(P0), cursor(P2)` · `agreement: 2/2` · `复核: 确认`
  - 问题: 三处一次性原语（nonce / 授权码 / 激活码）都是 `SELECT…WHERE x=?` → Go 内判 `consumed_at` → `UPDATE…SET consumed_at=? WHERE x=?`，**UPDATE 没有 `AND consumed_at IS NULL` 守卫**。虽包在 `WithTx` 内，但 `store.go:84 BeginTx(ctx,nil)` 用引擎默认隔离级：PG 是 READ COMMITTED，普通 `SELECT` 不加行锁 → 两个并发兑换都读到“未消费”、都过 Go 检查、都成功 UPDATE 并提交。
  - 为何测试没发现: SQLite 仅因 `store.go:62 SetMaxOpenConns(1)` 把写串行化而侥幸安全；测试栈是 SQLite-only，PG 的竞态被完全掩盖。
  - 风险: 击穿整条信任链的重放保护——签名钱包 nonce 可被消费两次、授权码可换两套 token、激活码可建两个 Telegram 订阅会话。违反 TC-3/TC-6/TC-9“恰好一次消费”在**生产驱动**上的保证。
  - 严重度裁定: cursor 给 P2，claude 给 P0；**Claude 复核维持 P0**——它在文档声明的生产驱动(PG)上破坏安全不变量。
  - 建议修复: 让“写”成为闸门：`UPDATE…SET consumed_at=? WHERE x=? AND consumed_at IS NULL`，`RowsAffected()==0` → `domain.ErrConsumed`；保留前置 `SELECT` 仅用于区分 NotFound/Expired/Purpose。方言无关，彻底消除窗口。**同一缺陷类也出现在 refresh 轮换（见 P1-1），应统一用 CAS 模式修。**

---

## P1 — 高

- [ ] **[core/oauth/token.go:106-117, 233-239]** Refresh 轮换非原子 + 无 CAS → 并发可双发 token、且崩溃会让会话变砖 — `提出者: claude(P1), cursor(P1×2)` · `agreement: 2/2` · `复核: 确认`
  - 问题: `tokenRefresh` 读到 grant=active → `SetStatus(...,GrantRotated)`（**无条件 UPDATE**）→ `mint`（`issuedTokens.Create`/`grants.Create` 各自传 `nil` tx，非同一事务）。
  - 风险(并发，cursor 强调): 同一 refresh token 两个并发请求都过 `active` 检查、都无条件置 rotated、都 mint → 一个 refresh 换出**两套有效 token**，且因双方都是“从 active 轮换”而非“重放 rotated”，**盗用检测 `RevokeChain` 不触发**。
  - 风险(原子，claude 强调): `SetStatus(rotated)` 后、新 grant 落库前崩溃 → 旧 grant 已 rotated、无后继 → 用户必须重新登录（可恢复）。
  - 建议修复: 单事务内 `UPDATE RefreshGrant SET status='rotated' WHERE id=? AND status='active'`，校验 `RowsAffected()==1`，只有赢家 `mint`、输家返回 `invalid_grant`；rotate 与 mint 的多次写穿同一 `*sql.Tx`，全部成功后再提交。

- [ ] **[utils/chain/koios.go:51, node_lsq.go:71]** 链适配器把 `stake_credential_hash` 当作 bech32 stake address 发查询 → 非 mock 模式下无法运行 — `提出者: cursor(P0)` · `agreement: 1/2` · `复核: 确认（生产阻断；降为 P1）`
  - 问题: 身份存的是 `hex(blake2b224(stake_vkey))`，但 Koios 把它作 `_stake_addresses` 发（koios.go 注释自己也承认“real queries 需 bech32 stake address”），node_lsq 把它作 `cardano-cli … stake-address-info --address` 发。两者都没有 hash→stake address 的映射。
  - 风险: `OUROPASS_CHAIN_KIND=koios|node_lsq` 时实时快照恒为空 → `evaluate` 拒绝所有人。issuer **只能在 `mock` 模式工作**。
  - 严重度裁定: cursor 给 P0；**Claude 复核降 P1**——它 fail-closed（拒绝而非误放，无安全/数据损失），且 spec R4/链适配器明确是“mock 优先、真链集成单独打 tag 延后”。但它是**硬性上线阻断**：任何真链部署前必修。
  - 建议修复: 在 challenge 时由 stake vkey 计算 bech32 stake address（含 network header + credential），或建 credential-hash→address 映射缓存，向适配器传 stake address；补真 Koios/node fixture 的集成测试。附带：`node_lsq.go:104 --testnet-magic` 缺 magic 值，真链下也会失败。

- [ ] **[cmd/issuer/main.go (worker 装配段)]** Push 调度 worker 从未在 main 启动 → TC-10 运行时为死路 — `提出者: cursor(P1)` · `agreement: 1/2` · `复核: 确认`
  - 问题: `main` 起了 Telegram + reconciliation 两个 goroutine，但**没有实例化 `push.Scheduler`、也没有轮询 `PushJob`**。admin `POST /api/admin/push/jobs` 只是插入 `scheduled` 行。
  - 风险: TC-10 行为只存在于单测；运营者建的推送任务在真实运行时**永远不会投递**。
  - 建议修复: 加一个 push worker 循环（轮询 `status=scheduled` 的 PushJob → `Scheduler.Run` → 尊重 ctx 关停），`channel_type=telegram` 时复用 Telegram `Sender` transport。

- [ ] **[worker/reconciliation/reconciliation.go:57-86, 101-108]** 单个 credential 出错即中止整个 epoch，且 `lastEpoch` 不前进 → 降级/吊销可能永不生效 — `提出者: claude(P1)` · `agreement: 1/2` · `复核: 确认`
  - 问题: `Reconcile` 对任一 session 的 `Eligibility`/store 错误就 `return`（59/63/75/82），一个 credential 的瞬时链查询失败即中止**其余所有** active session 的处理；`Run` 仅在整趟成功后才推进 `lastEpoch`（105），故某 credential 持续出错会**无限期**阻塞该 epoch 的所有降级/过期。
  - 风险: 违反 C7/TC-11（epoch 边界降级/过期）。这是“撤权可用性”漏洞——失权事件静默地永不触发；攻击者让自己的 credential 在链源报错即可卡住全池 reconciliation。
  - 建议修复: 单 session 故障隔离：对个别错误 `log + continue`、计入 `Result` 失败数，无论如何推进 `lastEpoch`（或仅下趟重试失败子集）。

- [ ] **[core/oauth/token.go:81-94, 210-217]** Public client 刷新不校验设备 PoP；无效 `device_pubkey` 静默签出无绑定 token — `提出者: cursor(P1×2)` · `agreement: 1/2` · `复核: 确认`
  - 问题: refresh 路径只对 confidential 校验 `client_secret`；public client 既不验 DPoP 也不验 `device_pubkey`，`BoundDevicePubkey` 被动前传。另在 `mint`，`hex.DecodeString(p.devicePubkey)` 出错时走 `err==nil` 分支之外 → `boundDevice` 为空、**无 `cnf.jkt` 声明**，token 退化为纯 bearer。
  - 风险: C6 的 public-client PoP 在首次签发后形同虚设；被盗 refresh token 在过期/被检测前是完全 bearer。
  - 建议修复: public client 刷新时要求 `device_pubkey`（或 DPoP 证明）与 `grant.BoundDevicePubkey`/`cnf.jkt` 指纹匹配，不符 → `invalid_grant`；mint 时 public client 的 device key 解码失败应 `invalid_request` 而非静默放行。（注：DPoP 完整验证 spec 按 D7 延后，但“静默不绑定”超出了“延后”——至少应显式失败。）

- [ ] **[httpapi/handlers_admin.go:138-143]** Admin IP 取自原始可伪造的 `X-Forwarded-For` → 审计日志投毒 + 限流绕过 — `提出者: claude(P1)` · `agreement: 1/2` · `复核: 确认`
  - 问题: `clientIPFromReq` 在有头时**原样返回整个 `X-Forwarded-For`**，无可信代理处理。该值喂给 admin 登录审计（`Verify(...,clientIPFromReq(r))`，:59）和每次敏感操作的审计 IP（`handlers_admin_resources.go:48`）。而公共面限流只按 `RemoteAddr`（`middleware.go:120`），两层口径不一致。
  - 风险: 客户端可任设 `X-Forwarded-For` 来 (a) 伪造写入 `AdminUser`/`AuditLog` 的 IP（反取证/嫁祸）、(b) 轮换该值绕过任何未来的 per-IP admin 登录限流。无前置代理时 `RemoteAddr` 才是唯一可信源。
  - 建议修复: 引入单一可信 IP 解析器供中间件与 admin handler 共用：有可信代理则按配置的可信跳数取 XFF 最右非可信跳，否则用 `net.SplitHostPort(r.RemoteAddr)`；绝不信任原始整头。

- [ ] **[httpapi/middleware/middleware.go:131-190]** 幂等缓存：无界增长 + 缓存失败响应 + key 未按方法/路径限定（且仅进程内） — `提出者: claude(P1), cursor(P2)` · `agreement: 2/2` · `复核: 确认`
  - 问题: (1) `put`(184) 无条件写入，仅在“同 key 再次命中且超 TTL”时惰性删（`get`,175），**无后台清扫**（IP 限流器有清扫、它没有）→ 写一次再不请求的 key 永不释放 → map 无界增长（内存耗尽 DoS）。(2) `put` 对任意状态码（含 4xx/5xx）都缓存（167）→ 客户端 `Idempotency-Key:X` 撞上瞬时 500 后 10 分钟内一直被回放同一个 500、该 key 永远无法成功重试。(3) key 是裸头值，`/api/oauth/token` 与 `/api/activation/create` 共用（router.go:58,61）→ 同 key 跨端点回放错误响应体。另：进程内 map 在多实例部署下无幂等效果。
  - 风险: 未鉴权（这两个端点在鉴权前）的内存耗尽 DoS + 中毒错误回放 + 跨端点串响应。
  - 建议修复: 加后台 TTL 清扫（或 LRU 上限）；只缓存 2xx；key 加 `Method+Path` 前缀（`r.Method+" "+r.URL.Path+"\x00"+key`）；多实例需求则改 DB 持久化幂等记录。

---

## P2 — 中

- [ ] **[core/rules/engine.go:33, 113-118 + oauth.go InputFromSnapshot]** `min_active_epochs`/委托年龄规则在生产路径恒不生效（fail-open 子规则） — `提出者: cursor(P1), claude(P3)` · `agreement: 2/2` · `复核: 确认（裁定 P2）`
  - 问题: `InputFromSnapshot` 把 `EpochsDelegated` 恒置 -1，无适配器填充委托年龄；`satisfies` 在 `EpochsDelegated<0` 时**跳过** `MinActiveEpochs` 检查 → 配了 `min_active_epochs` 的规则对该判据**静默无效**（委托时长不足者不会被该判据拒绝）。
  - 风险: 运营者配置的资格门槛弱于预期（该子判据 fail-open）；C7 grace/min-epoch 逻辑成死代码。其余判据（min stake、pool）仍生效。
  - 严重度裁定: cursor P1 / claude P3 → **Claude 裁定 P2**（安全相关的功能 fail-open，但非整体放行）。
  - 建议修复: 给 `chain.Snapshot` 加 `EpochsDelegated`（db_sync 由 epoch_stake 历史算）并在 `InputFromSnapshot` 映射；或显式文档化“grace 仅靠 reconciliation 滞后实现”并移除死分支 + 处理 `RequiredStatus`（同样 parsed-never-used）。

- [ ] **[httpapi/router.go:56-58, 61]** 签发面（authorize/token/activation）无 IP 限流 — `提出者: cursor(P2)` · `agreement: 1/2` · `复核: 确认（注：符合 spec 平面映射）`
  - 问题: `/connect`、`/api/connect/authorize`、`/api/oauth/token`、`/api/activation/create` 未挂 `publicLimit`，仅两个挂了 `idem`；只有 `/api/auth/challenge` 与 verifier 组限流。
  - 风险: 这些端点未鉴权却做 COSE 验签 + **外部链调用(evaluate)**，无限流 → 暴力猜码/穷举 + 对 Koios/节点的放大型资源耗尽。
  - 复核注: spec §2.3 平面映射本就只给签发面规定了 `idempotencyKey`、未规定限流，故**与文档一致**；但端点性质值得补限流。
  - 建议修复: 给签发面加 `publicLimit`（或更紧桶），token 端点考虑 per-`client_id` 限流。

- [ ] **[core/oauth/introspect.go:24-54 + handlers_verifier_introspect.go:55,64]** introspect 未鉴权，且可凭“裸 jti”查询 → token 状态 oracle — `提出者: claude(P2)` · `agreement: 1/2` · `复核: 确认（已细化）`
  - 问题: introspect 在 verifier 组仅限流、无调用方鉴权；`parseTokenBody` 接受裸 `jti`，`Introspect` 在 `token==""且jti!=""` 时跳过 JWS 验签直接查 `issuedTokens.Get(jti)` → 返回 `active/exp/membership_status`。
  - 风险: 任意未鉴权方可用**已知 jti** 探测其 live 状态（是否吊销/有效会员），RFC 7662 建议该端点应鉴权。`sub/tier` 泄露程度低（持 token 者本可解 JWS 自得），真正残留是裸 jti 状态 oracle——仅靠 jti 为随机不可猜来缓解。
  - 建议修复: 若 introspect 面向 confidential 资源服务器，加 client 鉴权（Basic/bearer）；或显式文档化开放 introspect 决策并对未鉴权方最小化响应（去掉 `sub/tier`，并禁掉裸 jti 路径）。

- [ ] **[store/repo_refreshgrant.go:97-105]** `RevokeChain` 不检查 `rows.Err()` → 盗用响应吊销可能静默截断 — `提出者: claude(P2)` · `agreement: 1/2` · `复核: 确认`
  - 问题: 沿 `rotated_from` 的 BFS 在 `for rows.Next()` 后只 `rows.Close()`，**从不检查 `rows.Err()`**；迭代中途的驱动/读错误会让循环像正常结束一样退出，链上后代 grant 漏吊销。
  - 风险: 刷新盗用响应路径(§9.4)欠吊销——被盗的后代 grant 可能在链吊销中存活。概率低但安全相关。
  - 建议修复: `for rows.Next()` 后加 `if err := rows.Err(); err != nil { rows.Close(); return err }` 再继续 BFS。

- [ ] **[httpapi/handlers_admin_resources.go:39-42, 325]** `requireStepUp` 解引用可能为 nil 的 admin；step-up 策略不一致 — `提出者: claude(P2)` · `agreement: 1/2` · `复核: 确认`
  - 问题: `requireStepUp` 取 `adminFromCtx` 后直接读 `u.OwnerKeyHash`，**无 nil 检查**（虽通常被 `requireSession` 前置保护，但任何顺序变更会 panic→被 Recoverer 吞成 500）。另 step-up **只在密钥轮换**强制；同等破坏力的 `adminRevokeMember`（级联吊销 token/grant/sub）、`adminRegisterClient`（签发 client secret）、`adminCancelSub` 都不需要 step-up。
  - 风险: 潜在 500/panic；同等爆炸半径操作的 step-up 策略不一致（operator 会话仅凭 cookie 即可吊销任意会员/取消任意订阅）。
  - 建议修复: 加 `if u==nil { return admin.ErrUnauthorized }`；按爆炸半径统一 step-up 策略（至少 member-revoke 与 client 注册对齐密钥轮换）。

- [ ] **[worker/push/push.go (Append/limiter)]** Push worker 吞掉 DeliveryLog 错误，且每次重试都耗一个限流令牌 — `提出者: claude(P2)` · `agreement: 1/2` · `复核: 确认`
  - 问题: `_ = s.deliveries.Append(...)` 丢弃审计写错误而 job 仍标 `Done`；`limiter.Wait` 按每次**发送尝试**（含重试）调用，失败收件人会耗多个令牌。
  - 风险: 投递审计记录静默丢失（TC-10 要求 DeliveryLog）；失败时有效扇出速率被压低。
  - 建议修复: 记录/上报 Append 失败；限流按收件人计（每收件人一个令牌而非每重试）；收件人循环顶部查 `ctx.Err()` 以便及时关停。

- [ ] **[cmd/issuer/main.go (shutdown 段)]** 优雅关停不等待后台 worker — `提出者: claude(P2)` · `agreement: 1/2` · `复核: 确认`
  - 问题: `runNonceGC`、Telegram、reconciler 都是绑 `sigCtx` 的裸 goroutine；收到信号后 `run()` 调 `srv.Shutdown` 即返回，**不 join** 它们，进程可能在 `Consume`/`Upsert`/`SendMessage` 中途退出。
  - 风险: worker 可能在事务中途被杀（TC-1 期望干净 SIGTERM）；每写各自独立事务故损坏概率低，但在途工作被丢弃。
  - 建议修复: 用 `sync.WaitGroup` 跟踪 worker，`srv.Shutdown` 后 `wg.Wait()`（受 `ShutdownTimeout` 约束）。

- [ ] **[store/repo_oauthclient.go:73-95]** `OAuthClient.List` 跳过 `Rebind` 且吞解码错误 — `提出者: claude(P2)` · `agreement: 1/2` · `复核: 确认`
  - 问题: `List` 是唯一未包 `Rebind` 的查询（今天因无占位符而安全，但日后加 `WHERE ?` 过滤会在 PG 上断）；并用 `_` 丢弃 `decodeStrings`/`parseTS` 错误 → 列损坏时静默成空字段，与 `Get` 不一致。
  - 建议修复: SQL 包 `Rebind`；像 `Get` 一样上报解码错误。

- [ ] **[utils/crypto/crypto_test.go COSE 测试]** COSE 仅用合成向量，无真钱包 CIP-30 签名 — `提出者: cursor(P2)` · `agreement: 1/2` · `复核: 确认（R1/TC-3 缺口）`
  - 问题: spec TC-3/R1 要求真钱包 golden vector，但测试用与生产 verifier 同一套 `makeCOSESign1` 构造（自证）。
  - 风险: 钱包互操作 bug（CBOR 规范化、tag 处理、头放置）要到生产才暴露。
  - 建议修复: 加 `//go:build integration` 测试，纳入 Lace/Eternl 等真实抓包向量；保留合成测试做回归。

---

## P3 — 低

- [ ] **[utils/crypto/cose.go:114-134]** `checkAlg` 容忍缺失/不可解析的 `alg` — `提出者: claude(P3), cursor(P3)` · `agreement: 2/2` · `复核: 确认（不可利用）`。protected 头空/无 label-1 时不强制算法，仅靠 `ed25519.Verify` 兜底。**不可利用**（非 EdDSA 签名只会验签失败），但信任根原语建议：有 protected 头时强制 `alg=-8`，否则拒。
- [ ] **[core/oauth/token.go:91-92, 156-157]** client_secret 比较非常量时间 — `提出者: cursor(P2)` · `复核: 降 P3`。用 `!=` 比 SHA-256 哈希串；两侧均已哈希，时序泄露的是哈希前缀而非密钥，256-bit 随机密钥下不可逆 → 实际不可利用。建议仍用 `subtle.ConstantTimeCompare` 保持 crypto 一致性。
- [ ] **[utils/chain/node_lsq.go:47,59]** `RewardAccountBal` 用 `int64` 解析 — `提出者: cursor(P2)` · `复核: 降 P3`。违反 C4（>2^63-1 lovelace 溢出），但 rewards 不参与资格判定。建议同 Koios `total_balance` 用 string/`big.Int`。
- [ ] **[utils/jose/jose.go:69-84]** `SignActivationToken`/`ActivationClaims` 死代码 — `提出者: cursor(P2)` · `复核: 降 P3`。激活实际走 D8 短码（opaque code + DB 消费），非 JWT。建议删除未用签名路径，或对齐 TC-9/spec 文案到 D8 短码设计。
- [ ] **[utils/chain/chain.go, db_sync.go]** `db_sync` 启动可选但运行时恒 `ErrNotImplemented` — `提出者: cursor(P3)` · `复核: 确认`。`NewSource` 对 `kind=db_sync` 成功、首次 `Snapshot/Epoch` 才失败 → 误配置过了健康检查却全量签发失败。建议默认构建对 db_sync 启动即失败或明确报错。
- [ ] **[httpapi/handlers_admin.go:64-67]** admin 会话 cookie 恒 `Secure:true` — `提出者: cursor(P3)` · `复核: 确认`。本地 HTTP（无 TLS）admin 登录浏览器不存 cookie。建议按配置（`OUROPASS_TLS`/`X-Forwarded-Proto`）gate。
- [ ] **[config/config.go:97-112]** `validate()` 不要求 `OUROPASS_POOL_ID` — `提出者: claude(P3)` · `复核: 确认`。空 pool id → issuer 为 `ouropass:`、资格恒不匹配（fail-closed）。建议启动即拒空 PoolID。
- [ ] **[worker/telegram/transport_botapi.go:42-61 + telegram.go:186]** `GetUpdates` 未传 `allowed_updates` — `提出者: claude(P3)` · `复核: 确认`。非消息更新（edited/channel post/callback）带 `From.ID=0` 抵达并被回 `/help` 到 chat "0"。建议传 `allowed_updates=["message"]` 并跳过 `From.ID==0`/空文本。
- [ ] **[httpapi/handlers_oauth.go:99,111; handlers_activation.go:37; handlers_admin.go:134]** 多处 4xx 回显 `err.Error()` — `提出者: claude(P3)` · `复核: 确认`。今为 domain 校验串，但无保证不会冒出被包装的 DB/内部错误。建议已知哨兵映射固定文案、未知回退通用 `invalid request`（如 `serverError` 对 500 的做法）。
- [ ] **[core/oauth/oauth.go:138-141]** `Authorize` 不在授权码上存 `DevicePubkey` — `提出者: cursor(P3)` · `复核: 确认`。设备绑定仅发生在 token 兑换时。鉴于 PKCE 风险低；建议文档化“设备绑定仅 token-time”或在授权码持久化以便提前绑定。

---

## 规格符合性 (Spec Compliance)

| 规格项 | 结论 | 证据 / 备注 | 命中评审者 |
|--------|------|-------------|-----------|
| C1 Go+chi+net/http | ✅ 满足 | router.go:12-13；无 Gin/Echo/Fiber | 2/2 |
| C2 签发统一 OAuth | ✅ 满足 | token.go:46-55 双 grant；无 license 端点 | 2/2 |
| C3 私钥隔离 | ✅ 满足 | 仅存 issuer 签名密钥+bot token；owner key 仅验签不存 | 2/2 |
| C4 金额大数 | 🟡 部分 | engine.go 用 `big.Int`；但 node_lsq rewards 用 int64(P3) | 2/2 |
| C5 敏感字段加密 | 🟡 部分 | 签名私钥 AES-GCM；client_secret 为 SHA-256 哈希(单向，不可恢复)；bot token 仅 env | 2/2 |
| C6 短效+PoP | 🟡 部分 | EdDSA 无证书链✓、TTL✓、`cnf.jkt` 签发时设✓；**但 refresh 不验 PoP、无效 device key 静默不绑定(P1)、confidential holder-of-key 未实现** | 2/2 |
| C7 active snapshot+epoch 重算 | 🟡 部分 | 重算存在；**但 reconciler 可卡死(P1)、grace/min-epoch 死代码(P2)** | 2/2 |
| C8 无 Member 表+派生 sub | ✅ 满足 | hash.go:31-35 `base32(HMAC-SHA256(salt,sch))`；确定性、salt-gated | 2/2 |
| C9 无冷钥 license | ✅ 满足 | JWKS 仅公钥；owner allowlist(D9) | 2/2 |
| C10 rules 纯函数 | ✅ 满足 | engine.go 注入式、稳定排序、无 IO/时钟 | 2/2 |
| TC-1 启动/健康/关停 | 🟡 部分 | 四平面+健康200+HTTP 优雅关停；**worker 未 join(P2)** | 2/2 |
| TC-2 双栈 | 🟡 部分 | SQLite 测试过；**PG 未实测，且 Consume 竞态使两引擎行为分叉(P0)** | 2/2 |
| TC-3 CIP-30 验签 | 🟡 部分 | COSE 实现正确、篡改被拒；**无真钱包向量(P2)** | 2/2 |
| TC-4 JOSE/JWKS | ✅ 满足 | 独立 verifier 验签过；JWKS 无证书链 | 2/2 |
| TC-5 资格引擎 | 🟡 部分 | 纯函数✓、min_stake/priority✓；**grace/min-epoch 运行时死(P2)** | 2/2 |
| TC-6 授权码流 | 🟡 部分 | mock 链下通过；**真链适配器下会失败(P0/P1)** | 2/2 |
| TC-7 刷新+盗用 | 🟡 部分 | 轮换+RevokeChain 测过；**非原子+并发双发(P1)、RevokeChain 截断(P2)** | 2/2 |
| TC-8 密钥轮换/吊销 | ✅ 满足 | rotate JWKS overlap；revoke 反映于 introspect | 2/2 |
| TC-9 激活+Telegram | 🟡 部分 | 流程实现；**激活码 Consume 双花(P0)；设计是 D8 短码非 jti 台账** | 2/2 |
| TC-10 推送 | 🟡 部分 | Scheduler 单测过；**main 未起 push worker(P1)、DeliveryLog 吞错(P2)** | 2/2 |
| TC-11 Reconciliation | 🟡 部分 | 逻辑+测试存在；**整趟中止+epoch 不前进可致永不降级(P1)** | 2/2 |
| TC-12 Admin | 🟡 部分 | 登录/RBAC/审计✓；**step-up 仅密钥轮换(P2)、审计 IP 可伪造(P1)** | 2/2 |

**Scope drift（范围漂移）**: 无实质越界。`SignActivationToken`/activation JWT 类型未用、push worker 未接线、`db_sync` 桩、`RequiredStatus`/grace 分支死代码——均为 spec 预期但当前未接通的迭代项，非新增范围。产品名迁移到 Ouro Pass 符合 p11-2。Confidential holder-of-key 与完整 DPoP 按 C6/D7 延后。

---

## 分歧与少数意见
- **P0 Consume CAS 的严重度**: claude=P0 / cursor=P2。Claude 复核裁定 **P0**——破坏生产驱动上的安全不变量（重放保护），且与 refresh 轮换(P1)同根，应一并修。
- **链适配器 hash↔stake-address**: cursor=P0 / claude 未提。复核裁定 **P1**（fail-closed、集成延后，但硬性上线阻断）。
- **min_active_epochs 死代码**: cursor=P1 / claude=P3。复核裁定 **P2**（安全相关 fail-open 子判据）。
- 仅单 agent 提出但已复核确认的项：push worker 未接线、reconciler 卡死、XFF 伪造、introspect 裸 jti oracle、RevokeChain rows.Err、step-up 不一致、关停不 join、constant-time、cookie Secure、db_sync 桩等——均“确认”，非误报。

## 误报 / 已排除
- **无明确误报。** 复核未发现任一被提出的问题不成立。需注意几处“被正确实现、非缺陷”的点：COSE/CIP-8 验签信任根**实现正确无绕过**（payload 绑定 nonce、key 在 walletauth 层绑定、Sig_structure 手装 external_aad=[]）；**SQL 全参数化无注入**；AES-GCM 字段加密正确（32 字节 key、每次新随机 12 字节 nonce、Open 验 tag）；JWKS 不泄私钥；`Revoke` 用 `JTIUnverified` 是 RFC 7009 故意设计（持有 token 串即可吊销自身那一行，不提权）。

## 建议补充的测试（合并前高价值）
1. **PG 并发双兑换测试**：对 nonce/授权码/激活码并发 Consume，断言只成功一次（覆盖 P0）。
2. **PG 并发 refresh 测试**：同一 refresh token 并发，断言只签出一套且另一路 `invalid_grant`（覆盖 P1-1）。
3. **reconciler 单 session 报错测试**：一个 credential 链查询失败，断言其余 session 仍被处理且 epoch 推进（覆盖 P1-4）。
4. **真钱包 COSE golden vector**（Lace/Eternl，`//go:build integration`，覆盖 TC-3/R1）。
5. **真链适配器 fixture 测试**：hash→stake-address 映射 + Koios/node 响应解析（覆盖 P1-2）。
6. **push worker 端到端**：建 job → worker 轮询 → 投递 + DeliveryLog（覆盖 P1-3）。
