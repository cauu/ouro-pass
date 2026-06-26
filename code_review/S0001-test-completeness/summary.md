# 评审汇总 — S0001 测试完备性 (test completeness)

参与评审 (Reviewers): **claude (subagent)**、**cursor (auto)**；codex 跳过（不可用）。主 Claude 已对照真实测试/代码复核。
评审类型: 测试完备性审计（非代码变更 diff）—— 单元 / 集成(PG) / e2e 用例是否覆盖关键路径、错误分支、并发/安全否定用例。
覆盖率: **73.6%**（`-coverpkg=./...` 含 e2e；按函数明细见 `tmp/review/S0001-test-completeness/coverage-by-func-coverpkg.txt`）。
总体结论 (Overall): **NEEDS_MORE_TESTS**（两评审者一致）
问题计数: P0 **0** · P1 **4** · P2 **10** · P3 **3**
交叉确认: 两 agent 共同命中 **8** 项；其余单 agent 提出但经复核确认。

> 勾选 `[x]` = 批准补这条测试。本 skill 只评审、不写代码/测试。`提出者`/`agreement`/`复核` 同前。
> **结论先行**:安全内核(COSE 验签非绕过、一次性 CAS 双花、refresh 盗用链、RBAC+step-up、PG 并发恰好一次)已**对抗性地测扎实**,**无 P0**。缺口集中在 **push worker 轮询层、密钥吊销、admin 变更/读端点、几条 spec 文案声称但实际证据偏弱的 TC**。都是"静默回归窗口",不是"今天就坏"。

---

## P1 — 高（建议发布前补）

- [ ] **[worker/push/worker.go:31 `Run` + store/repo_pushjob.go:87 `ListScheduled`，均 0%]** push worker 轮询路径**零覆盖** — `提出者: claude(P1), cursor(P0)` · `agreement: 2/2` · `复核: 确认`
  - 未测:p12-4 加的运行时驱动 —— `ListScheduled`(`status='scheduled' AND (scheduled_at IS NULL OR scheduled_at<=now)`,oldest-first)→ `Scheduler.Run` 逐 job → 循环。**全部测试只调 `Scheduler.Run`**(`push_test.go:82/117/134/154`)、e2e Flow D 也直接调 Scheduler,**`Worker.Run`/`NewWorker` 无人调用**(已 grep 确认)。
  - 风险:`ListScheduled` 的 SQL 谓词/排序或轮询接线一旦回归(漏 `scheduled_at IS NULL` 分支、status 串错、列出却不投递、`ctx.Err()` 中途处理错),admin 建的 push job **永不投递且无任何测试失败**。TC-16 文案声称"push worker 拾取并投递"但无 Go 测试驱动 `Worker.Run`。
  - 严重度裁定:cursor=P0/claude=P1 → **复核 P1**(是功能/接线测试缺口,非"安全不变量零覆盖";Scheduler 业务逻辑本身已测)。
  - 建议补:`push_test.go` 种两条 due(`ScheduledAt=nil` + `过去`)+ 一条 `PushDone` + 一条 `未来`;`NewWorker(st,capturingSender,1ms,opts).Run` 跑 100ms ctx;断言只有两条 due → `PushDone`、其余不动。另加 `ListScheduled` 直接单测谓词+排序。

- [ ] **[core/keys/keys.go:141 `Revoke`，0%]** 签名密钥**紧急吊销**无测试 — `提出者: claude(P1), cursor(P1)` · `agreement: 2/2` · `复核: 确认`
  - 未测:`Revoke(kid)` 置 `revoked`+`revoked_at`。无测试调用,也无测试断言被吊销 key 从 `PublicJWKSKeys`(只发 active+rotating)消失、其签出 token 停止验签。已 grep 确认 keys 测试无 `.Revoke(`。
  - 风险:§3.5 密钥泄露应急路径。若状态过滤回归导致 revoked key 仍出现在 JWKS,被妥协密钥签的 token 仍验签通过,无任何测试捕获。
  - 建议补:`Rotate` 两次(active k2 + rotating k1)→ `Revoke(k1)`→ 断言 JWKS 仅剩 k2;`Revoke(k2)`→ 断言 `ActiveSigner` 报错(无 active)、JWKS 空。

- [ ] **[httpapi/handlers_admin_resources.go:139 `adminCancelSub`，0%]** admin **变更端点**(取消订阅)无测试 — `提出者: claude(P1), cursor(P1)` · `agreement: 2/2` · `复核: 确认`
  - 未测:`POST /subscriptions/{id}/cancel` → `SetStatus(SubCancelled)` + 审计。operator 门禁的会员可见状态变更,无测试覆盖其行为或 RBAC(viewer 应 403)。已 grep 确认 admin 测试无 cancel/subscriptions 路径。
  - 风险:状态写错 / 漏审计 / viewer 越权取消,均静默上线。
  - 建议补:operator env 种一个 active session → `POST .../cancel`→200、断言 DB `SubCancelled` + `subscription.cancel` 审计行;viewer→403。

- [ ] **[utils/crypto/cose.go `Verify` — TC-3]** **缺真实钱包 COSE golden vector** — `提出者: claude(P1), cursor(P2)` · `agreement: 2/2` · `复核: 确认（需外部抓样）`
  - 未测:所有 COSE 测试(`crypto_test.go`/`walletauth_test.go`/e2e)都用**与生产 verifier 同一套 `cbor.Marshal`** 现造 `Sig_structure` 再验 —— 是**自洽往返**,非互操作测试。`Sig_structure` 字段序/编码若与真 Nami/Eternl/Lace `signData` 不符,**两侧同错、测试全绿、生产拒绝所有真钱包**。
  - 风险:系统最高风险自实现原语(CIP-8,spec R1)。这是真实互操作风险,但补它需要一份**真实 CIP-30 抓样**(外部依赖)。
  - 建议补:冻结一份真实 `signData` COSE_Sign1 hex(key/payload/sig)做 byte 字面量,断言 `ParseCOSESign1().Verify(pub,payload)==nil` + 一个篡改 payload 变体断言 `ErrCOSEPayload`。**需你提供/抓一份真钱包签名样本。**

---

## P2 — 中（建议补，按价值排序）

- [ ] **[e2e_test.go — 缺 reconciliation 全链路]** e2e 实际 **5 条流程**、无 reconciliation — `提出者: cursor(P1), claude(TC-24 caveat)` · `agreement: 2/2` · `复核: 确认`。spec p13-2/TC-24 文案称"6 流程含 reconciliation",实测 5 个 `TestE2E_*`(已 grep:confidential/PKCE/revoke/activation/keyrotation),reconciliation 仅 `reconciliation_test.go` 单包测,未经 `NewRouter`/epoch 推进驱动。补:e2e 种 active gold session + mock epoch 递增使其不合格 → 经 reconciler `Run` → 断言 session expired。
- [ ] **[oauth/token.go tokenRefresh WithTx — TC-14 回滚]** rotate→mint **同事务失败回滚**未测 — `提出者: cursor(P0), claude(可接受)` · `agreement: 2/2` · `复核: 确认（裁定 P2）`。mint 失败时旧 grant 应保持 active(回滚);happy-path 原子性+CAS 已测,但 mint-error 回滚分支无直接测试。需 fault-injection 缝(失败 store wrapper)。cursor 给 P0、claude 认为可接受 → **复核 P2**(错误边界,非零覆盖功能)。
- [ ] **[refresh_test.go — TC-7 tier 降级]** refresh **仍合格但 tier 下降**(gold→silver)未断言 — `提出者: cursor(P1)` · `agreement: 1/2` · `复核: 确认`。仅测完全不合格(`refresh_test.go:81`),无 tier 降级断言(已 grep:refresh_test 无 tier/silver)。TC-7 文案含"降级"。补:发 token 时 gold,改 snapshot 仅满足 silver,refresh → 断言新 access token `tier=silver`。
- [ ] **[handlers_verifier_introspect.go:14 introspect — TC-21]** HTTP 层**裸 jti oracle 防护**未测 — `提出者: claude(P2), cursor(P1)` · `agreement: 2/2` · `复核: 确认`。handler 固定传 `""` 忽略 body `jti`(p12-9/D16),但只有 core 层 `introspect_test.go:53` 测裸 jti、无 HTTP 测试 POST `{"jti":...}`。补:种 active token,`POST /api/oauth/introspect {"jti":"<active jti>"}`→`active:false`(证明 handler 忽略);`{"token":...}`→`active:true`。
- [ ] **[middleware.go clientIP — TC-18 可信代理]** `X-Forwarded-For` 解析两分支无测 — `提出者: claude(P2), cursor(P1)` · `agreement: 2/2` · `复核: 确认`。默认应忽略 XFF 取 RemoteAddr、TrustedProxy 时取最右跳;无测试设 XFF。安全控制(审计 IP/限流键)。补:中间件 table test + admin 审计 IP 断言两分支。
- [ ] **[middleware.go 幂等 — TC-19 加固]** **非 2xx 不缓存 + Method+Path 命名空间** 未测 — `提出者: claude(P2), cursor(P2)` · `agreement: 2/2` · `复核: 确认`。`middleware_test.go:44` 只测 replay+同 key 隔离。补:首次 500/二次 200 同 key → 断言二次真执行(非回放 500);`/a` 与 `/b` 同 key → 不同 body。
- [ ] **[inttest — TC-2 PG 双栈]** PG 仅 `OAuthClient` 方言往返、**其余 repo 未在 PG 重跑** — `提出者: cursor(P1), claude(TC-2 note)` · `agreement: 2/2` · `复核: 确认`。jsonb/TEXT/时间/lovelace numeric 在 StakeSnapshotCache/MembershipRule 等未 PG 验证。补:抽共享 `TestAllReposRoundTrip(openStore)`,`-tags integration` 用 `pgStore` 也跑一遍(顺带覆盖 PG 上 0% 的 `ListScheduled`/`ListActive`)。
- [ ] **[router.go 签发面 429 — TC-21]** `publicLimit` 挂到 authorize/token/activation **的 429 无路由级测试** — `提出者: cursor(P1), claude(TC-21 note)` · `agreement: 2/2` · `复核: 确认`。只有独立中间件 429 测试(`middleware_test.go:30`),未证明实际挂到了签发平面。补:httptest 对 `/api/oauth/token` 同 IP 连发超 burst → 断言 429。
- [ ] **[push/push.go deliver — 限速/退避]** 测试 `RatePerSec:100000` **关掉了限流器** — `提出者: claude(P2), cursor(quality)` · `agreement: 2/2` · `复核: 确认`。p12-4"按收件人计令牌"与退避时序从未被断言;TC-10"限速"未证。补:低 RatePerSec+burst1+多收件人,断言节流/计时。
- [ ] **[cmd/issuer/main.go worker 生命周期 — TC-16]** `startWorker`/`WaitGroup` join **仅二进制 smoke、无自动化单测** — `提出者: cursor(P1), claude(TC-1/16 note)` · `agreement: 2/2` · `复核: 确认`。补:抽 `startWorkers(...)` 可注入 fake worker,cancel ctx 后断言 WaitGroup 超时内归零。
- [ ] **[handlers_admin_resources_test.go — step-up 错 key HTTP 层]** step-up 签名正确但 `owner_vkey` ≠ 会话 → 403,**仅服务层测**(`admin_test.go:123`) — `提出者: cursor(P2)` · `agreement: 1/2` · `复核: 确认`。补:operator 登录后用**另一 wallet** 的 step-up 签名 POST rotate → 403。
- [ ] **[activation_test.go — blacklist 否定]** `CreateActivation` 对 blacklisted sch 拒绝**未测** — `提出者: cursor(P2)` · `agreement: 1/2` · `复核: 确认`。blacklist 只在 authorize 面测(`oauth_test.go:136`)。补:`Blacklist.Add(sch)` 后 `CreateActivation`→`ErrNotEligible`。

> 另两条单 agent P2(`复核: 确认`,价值较低):**过期授权码 `Consume`→`ErrExpired`**(`repo_authorizationcode.go:29`,72.7%,nonce 有对称测试、authcode 无)· **`ParseCOSESign1` 畸形输入分支**(68.8%,非 4 元组/截断 CBOR→`ErrCOSEMalformed`)· **reconciliation 升级方向**(silver→gold,claude P2,只测了降级)。

---

## P3 — 低（可选 / 可接受）

- [ ] **[handlers_admin_resources.go 只读 list 端点 0%]** `adminListClients` 值得一测(它 strip `ClientSecretHash`,回归会泄露 secret hash → 断言 JSON 无该字段);`adminSubscriptions/ListRules/ListPushJobs/Audit` 低价值穿透,可不补 — `提出者: claude(P3), cursor(P3)` · `复核: 确认`。
- **`utils/crypto/random.go` `RandomID/RandomToken/HashToken`** — 原包 0%、coverpkg 100%(全链路间接执行)。补一个 HashToken 确定性/长度的小单测属 nice-to-have、非必须 — 两 agent 一致**可接受**。
- **集成-only 适配器 0%**(`telegram/transport_botapi.go`、`chain/db_sync.go`、`node_lsq.go:execCLI`、`koios.go` HTTP、`cmd/issuer` serve 循环)—— **设计性可接受**(外部进程/网络),两 agent + scope 一致豁免。

---

## 规格符合性 (TC 覆盖) — 两评审者合并

| TC | 结论 | 证据 / 缺口 |
|----|------|-------------|
| TC-1 启动/关停 | 🟡 部分 | `main_test.go` buildServices/runNonceGC；serve 循环+WaitGroup join 仅 smoke |
| TC-2 双栈 | 🟡 部分 | SQLite 全套；**PG 仅 OAuthClient 往返** |
| TC-3 CIP-30 验签 | 🟡 部分 | 合成 COSE 对抗性强；**无真钱包 golden vector** |
| TC-4 JOSE/JWKS | ✅ 满足 | `jose_test.go` 独立验签 + 无私钥泄露 |
| TC-5 资格引擎 | ✅ 满足 | `engine_test.go` priority/big.Int/grace/fail-closed/确定性 |
| TC-6 授权码流 | ✅ 满足 | `oauth_test.go` + `handlers_oauth_test.go` + e2e Flow A |
| TC-7 刷新/盗用 | 🟡 部分 | 盗用链✓、完全不合格✓;**tier 降级 refresh 未测** |
| TC-8 轮换/吊销 | 🟡 部分 | 轮换+overlap✓、revoke-introspect✓;**`keys.Revoke` 0%** |
| TC-9 激活+Telegram | ✅ 满足 | `telegram_test.go` + e2e Flow D |
| TC-10 推送 | 🟡 部分 | 过滤/退避/DeliveryLog✓;**限速关掉、Worker 未测** |
| TC-11 Reconciliation | ✅ 满足(单元) | `reconciliation_test.go` 降级/隔离/Run;**e2e 未覆盖** |
| TC-12 Admin | 🟡 部分 | RBAC/step-up/cascade/审计✓;**list/cancel 端点 0%、step-up 错 key 仅服务层** |
| TC-13 一次性 CAS | ✅ 满足 | `repo_tokens_test.go` + `inttest` 真并发 |
| TC-14 refresh CAS+原子 | 🟡 部分 | CAS✓、并发✓;**mint 失败回滚未直接测** |
| TC-15 reconciler 隔离 | ✅ 满足 | `reconciliation_test.go:107` |
| TC-16 worker 生命周期 | 🟡 部分 | telegram dispatch✓;**push `Worker.Run` 0%、join 仅 smoke** |
| TC-17 public PoP | ✅ 满足 | `token_test.go` + e2e Flow B |
| TC-18 可信 IP | ❌ 未满足 | **无 XFF 测试** |
| TC-19 幂等加固 | 🟡 部分 | replay✓;**非2xx不缓存/命名空间未测** |
| TC-20 链身份/fail-closed | ✅ 满足 | `engine_test.go` + `stakeaddr_test.go` |
| TC-21 verifier/admin 加固 | 🟡 部分 | 错误信封不泄露✓;**HTTP 裸 jti、签发面 429 未测** |
| TC-22 低危批 | 🟡 部分 | const-time/PoolID/db_sync/strict-alg✓;telegram skip 集成-only(可接受) |
| TC-23 单元补齐 | ✅ 满足 | `config_test.go` + `main_test.go` |
| TC-24 e2e | 🟡 部分 | **5 条非 6 条;缺 reconciliation;push 走 Scheduler 非 Worker** |
| TC-25 PG 集成 | ✅ 满足 | `inttest` 设计 sound、真 PG ×2 绿 |
| TC-26 测试编排 | ✅ 满足 | Makefile + CI(build-tag 隔离正确) |

---

## 分歧与裁定
- **push Worker.Run 0%**:cursor=P0 / claude=P1 → 裁定 **P1**(功能/接线缺口,Scheduler 已测)。
- **rotate→mint 回滚**:cursor=P0 / claude=可接受 → 裁定 **P2**(错误边界,happy+CAS 已测,补它需 fault-injection 缝)。
- **e2e flow 数**:claude 说"文件 6 条"系数错(实为 5 个 `TestE2E_*`),cursor 正确 —— 实测 5、缺 reconciliation。

## 误报 / 已排除
- **无误报**。两 agent 的"已良好覆盖"判断经复核一致:安全内核(对抗性 OAuth、COSE 非绕过、PG 并发恰好一次、RBAC+step-up、错误信封不泄露)确实扎实;PG `inttest` 的 `start.Wait()` 起跑屏障 + `won==1` 断言对 CAS 语义正确;无 tautology、无共享态 flaky(每测独立 SQLite 临时文件、PG 用 `uk()` 唯一键)。

## 建议补测优先级（最高价值前 5)
1. **push `Worker.Run` + `ListScheduled` 单测**(P1,p12-4 的运行时驱动,纯 SQLite 可测,价值最高)。
2. **`keys.Revoke` 单测**(P1,安全应急,易补)。
3. **`adminCancelSub` + admin list/audit HTTP 测**(P1/P3,沿用 `adminResourceEnv`,顺带 `adminListClients` 断言不泄 secret hash)。
4. **路由级:introspect 裸 jti(TC-21)+ 签发面 429 + TrustedProxy IP(TC-18)+ 幂等非2xx不缓存(TC-19)** —— 一组 httptest 否定用例,证据从"中间件单测"升到"路由层"。
5. **真钱包 COSE golden vector(TC-3)** —— 需你提供一份真实 CIP-30 `signData` 抓样;补上后系统最高风险原语才算真验。
