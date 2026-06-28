# Ouro Pass 一键 Docker Compose 部署（issuer + Postgres + Caddy 自动 HTTPS）

Spec-ID: S0009
Status: active
Created Time: 2026-06-28T00:15:00+08:00
Start Time: 2026-06-28T00:15:00+08:00
Completion Time:
Previous Spec-ID: S0008
Closure Reason:

> 目标：用户配置好 `.env` 后，`docker compose up -d` 一键拉起全栈（issuer 单二进制 = API + 内嵌 Admin SPA + worker；Postgres 数据库；Caddy 自动 HTTPS 反代）。对标自托管去中心化开源项目（Umbrel / Start9 / Gitea / nostr relay）的最佳实践：单一 compose + `.env` + profiles、宿主可见数据卷、可复现镜像、自动 TLS、secret 引导脚本。**不改任何应用代码**——纯交付编排/打包/文档。

## 1. Requirement Details

### Background
- `server/cmd/issuer` 是**单一 Go 二进制**：同时承载 HTTP API、go:embed 的 Admin SPA（`/admin`，由 `make web` 烤入）、后台 worker（reconciliation / push / telegram）。启动即自动迁移（`store.Migrate`，sqlite + postgres 迁移均已 embed），有 `/healthz`，监听 `OUROPASS_ADDR`（默认 `:8080`），SIGINT/SIGTERM 优雅关停。
- 配置全部走环境变量（`server/internal/config/config.go`，`OUROPASS_*`）；secret（DB DSN、FIELD_KEY、SERVER_SALT、链 API key、Telegram token）只从 env 来，从不落库/落镜像。
- issuer **无本地状态**：JWKS 签名密钥等都在 DB。唯一需要持久化的中间件是 **Postgres**；Caddy 需持久化 ACME 证书。

### Scope（经用户确认）
- **链上数据源默认 `koios`**（联邦式、可换、无 SaaS 锁定）；`.env` 可改 `OUROPASS_CHAIN_KIND`。
- **数据库 = 内置 Postgres 服务**，数据 **bind-mount 到宿主 `./data/postgres`**（可见、可备份）；提供 `external-db` profile 接外部库。
- **内置 Caddy 自动 HTTPS**（Let's Encrypt），证书 bind-mount 到 `./data/caddy`；issuer 置 `OUROPASS_TRUSTED_PROXY=true`、`OUROPASS_TLS=true`、`OUROPASS_ISSUER=https://${DOMAIN}`。
- 交付物：`Dockerfile`、`.dockerignore`、`docker-compose.yml`、`deploy/Caddyfile`、`.env.example`、`deploy/init.sh`（secret 引导）、`docs/deployment.md`。
- 主权路径：`cardano-node` 作为**文档化的可选 profile/升级路径**（`OUROPASS_CHAIN_KIND=node_lsq`+ 自有节点），本期不内置全节点服务（数百 GB / 数天同步），仅给指引。

### Constraints
- C1 **零应用代码改动**：不改 `server/`、`web/`；只新增部署/编排/文档文件。
- C2 **无 SaaS 强依赖**：默认 koios（可指公共或自托管）；blockfrost 仅作文档备选。
- C3 **数据宿主可见**：Postgres、Caddy 持久化目录 bind-mount 到仓库内 `./data/*`（不入库，加 `.gitignore`）。
- C4 **可复现 / 可验证**：基础镜像 tag 钉死；多阶段构建产出最小运行镜像；非 root 运行。
- C5 **secret 不入库不入镜像**：`.env` 被 gitignore；`init.sh` 生成强随机 FIELD_KEY/SERVER_SALT；dev 用的固定密钥严禁用于生产（README 警告）。
- C6 **一键**：`cp .env.example .env`(+`init.sh`) → 填 `DOMAIN`/owner keys → `docker compose up -d` 即可。

### Non-goals
- 不内置 Cardano 全节点 / db-sync（仅文档化升级路径）。
- 不做 Kubernetes/Helm、不做多副本/HA、不做托管编排（Swarm 等）。
- 不改 CI（`.github/workflows/ci.yml`）发布镜像到 registry（可后续另立 spec）。
- 不内置监控栈（Prometheus/Grafana）——文档提一句即可。

## 2. Outline Design

### 2.1 镜像（`Dockerfile`，多阶段）
```text
stage web   : node:22-alpine + pnpm  → web/ install + build → /web/dist
stage build : golang:1.25-alpine     → 拷 server/ + 上一阶段 dist 到 adminui/dist
                                       → CGO_ENABLED=0 go build ./cmd/issuer（纯 Go，含 modernc sqlite）
stage run   : alpine:3.20            → ca-certificates、非 root 用户、拷二进制
                                       → EXPOSE 8080、HEALTHCHECK wget /healthz、ENTRYPOINT issuer
```
> 选 alpine（非 distroless）运行层：体积仍小，但有 busybox（wget）可做容器级 healthcheck + 便于排错；distroless 作为文档化的加固选项。

### 2.2 编排（`docker-compose.yml`）
- `issuer`：`build: .`（+ `image: ouropass/issuer:${OUROPASS_TAG:-local}` 便于缓存/后续拉取）；`env_file: .env`；`depends_on: postgres: condition: service_healthy`；healthcheck；`restart: unless-stopped`；不对宿主暴露端口（仅 Caddy 暴露 80/443）。
- `postgres`：`postgres:16-alpine`；`POSTGRES_*` 来自 `.env`；卷 `./data/postgres:/var/lib/postgresql/data`；healthcheck `pg_isready`；`restart: unless-stopped`。`profiles: [default]`，并提供 `external-db` 拓扑（文档说明注释掉 postgres、改 DSN）。
- `caddy`：`caddy:2.8-alpine`；挂 `deploy/Caddyfile:/etc/caddy/Caddyfile:ro`、`./data/caddy:/data`、`./data/caddy/config:/config`；暴露 `80:80`、`443:443`；`depends_on: issuer healthy`；`restart: unless-stopped`。
- 网络：默认 bridge；issuer 仅在内网，Caddy reverse_proxy 到 `issuer:8080`。

### 2.3 反代（`deploy/Caddyfile`）
```caddyfile
{$DOMAIN} {
    encode zstd gzip
    reverse_proxy issuer:8080
}
```
Caddy 据 `DOMAIN` 自动签发/续期证书；`ACME_EMAIL`（可选）走全局选项。

### 2.4 配置与引导
- `.env.example`：`DOMAIN`、`ACME_EMAIL`、`OUROPASS_TAG`、全量 `OUROPASS_*`（ISSUER 由 DOMAIN 推导并注释、NETWORK、CHAIN_KIND=koios、KOIOS_BASE_URL（mainnet/preprod/preview 注释示例）、CHAIN_API_KEY 可选、FIELD_KEY/SERVER_SALT 占位、OWNER_KEYS、TELEGRAM_*、TRUSTED_PROXY=true、TLS=true、DB_DRIVER=postgres、DB_DSN 指向 postgres 服务）、`POSTGRES_USER/PASSWORD/DB`。
- `deploy/init.sh`：幂等生成 `.env`（若不存在则从 example 复制），填入 `openssl rand -hex 32`(FIELD_KEY)/`-hex 16`(SERVER_SALT)/`-hex 24`(POSTGRES_PASSWORD)，建 `./data/{postgres,caddy}`，打印「编辑 DOMAIN/OWNER_KEYS 后 docker compose up -d」。**不覆盖**已存在的非空 secret。
- `.gitignore` 追加 `/data/`。

### 2.5 Risk and rollback
- R1 Caddy 本地无公网域名无法签证书 → 文档说明 compose 面向公网部署；本地验证用 `make dev` 或 `DOMAIN` 指内网 + Caddy internal CA。
- R2 koios 公共实例限流/不可用 → 文档给自托管 koios 与切 blockfrost/自有节点的路径。
- R3 误用 dev 固定密钥 → init.sh 生成强随机 + README/`.env.example` 红字警告；FIELD_KEY 轮换会使既有 🔒 字段不可解（文档警示）。
- Rollback：纯新增文件；删除/`git revert` 即可，不影响应用。

## References
- server/internal/config/config.go — **环境变量契约（唯一依据）**
- server/Makefile — `make web`（SPA→embed）、dev 运行的 env 形态
- server/cmd/issuer/main.go — 启动/迁移/healthz/优雅关停
- server/internal/store/migrate.go — 启动自动迁移（sqlite+postgres embed）
- docs/specs/completed/20260627T2210-S0008-admin-ia-content-reorg.md — 前序（前端已内嵌进二进制）

## 3. Execution Plan
### p1 镜像与编排
- [x] p1-1 `Dockerfile`（多阶段 web→go→alpine，CGO=0、非 root、HEALTHCHECK）+ `.dockerignore`（TC-1, TC-4）。
- [x] p1-2 `docker-compose.yml`（issuer+postgres+caddy；`./data/*` bind-mount；健康门控；restart；external-db 注释说明）（TC-1, TC-2, TC-3）。
- [x] p1-3 `deploy/Caddyfile`（`{$DOMAIN}` 自动 HTTPS → issuer:8080）（TC-1, TC-2）。

### p2 配置与引导
- [ ] p2-1 `.env.example`（全量变量 + 注释，对照 config.go）+ `.gitignore` 加 `/data/`（TC-3, TC-5）。
- [ ] p2-2 `deploy/init.sh`（幂等 secret 引导 + 建 data 目录 + 指引）（TC-5, TC-6）。

### p3 文档与验收
- [ ] p3-1 `docs/deployment.md`（一键指南：前置/init/up/owner 首登/备份/链源切换/主权节点/排错）（TC-6）。
- [ ] p4-1 验收：`docker compose config` 通过、`docker build` 成功、env 自洽、字段对照 config.go（TC-1..TC-6 汇总）。

## 4. Test and Acceptance Criteria
- TC-1 **构建可用**：`docker build` 成功产出 issuer 镜像（多阶段含 SPA embed）；`docker compose config` 解析无误。
- TC-2 **拓扑正确**：compose 含 issuer/postgres/caddy；Caddy 暴露 80/443 且反代 issuer:8080；issuer 不对宿主暴露端口；depends_on 健康门控。
- TC-3 **数据宿主可见**：Postgres → `./data/postgres`、Caddy → `./data/caddy` 为 bind-mount；`.env`/`./data` 已 gitignore。
- TC-4 **镜像规范**：基础镜像 tag 钉死；运行层非 root；含 `/healthz` HEALTHCHECK。
- TC-5 **secret 安全**：`.env.example` 覆盖全部 `OUROPASS_*`（对照 config.go）+ POSTGRES_*；`init.sh` 生成强随机 FIELD_KEY(32B)/SERVER_SALT(16B)/PG 密码且幂等不覆盖；无 secret 入库/入镜像。
- TC-6 **一键文档**：`docs/deployment.md` 给出从零到 `docker compose up -d` 的完整步骤、owner 首登、`./data` 备份、链源切换与主权升级、排错。
- Pass/fail：每 item 仅在其映射 TC 全 pass 且证据 append 后标 `[x]`；p4-1 为总收口。

## 5. Execution Log (append-only)
- 2026-06-28 S0009 创建并激活（active）：前序 S0008 已 delivered。范围经用户确认 = 单 compose 一键部署，链源默认 koios、内置 Postgres（数据 bind-mount 到 `./data`）、内置 Caddy 自动 HTTPS；零应用代码改动。
- 2026-06-28 p1-2 完成：`docker-compose.yml` 三服务——`issuer`(build .; image ouropass/issuer:${OUROPASS_TAG}; env_file .env; environment 派生 ADDR/ISSUER=https://${DOMAIN}/TRUSTED_PROXY/TLS/DB_DRIVER/DB_DSN→postgres 服务; depends_on postgres service_healthy; 不对宿主暴露端口)、`postgres`(postgres:16-alpine; `./data/postgres` bind-mount; pg_isready healthcheck)、`caddy`(caddy:2.8-alpine; 80/443; 挂 Caddyfile + `./data/caddy{,/config}`; depends_on issuer service_healthy)。external-db 以注释指引(注释 postgres + 改 DSN)取代 profile（简化、避开 depends_on-on-profiled 边界）。均 restart: unless-stopped。
- 2026-06-28 p1-3 完成：`deploy/Caddyfile`——`{$DOMAIN}` 站点块 `reverse_proxy issuer:8080` + `encode zstd gzip`，Caddy 自动 ACME；可选 email 全局块注释示意。
- 2026-06-28 p1-1 完成：`Dockerfile` 三阶段——node:22-alpine(pnpm) 构建 SPA → golang:1.25-alpine `CGO_ENABLED=0 -trimpath -ldflags=-s -w` 构建 issuer（COPY SPA dist 到 adminui/dist 供 `//go:embed all:dist`）→ alpine:3.20 运行层（ca-certificates、非 root 用户 ouro、`HEALTHCHECK wget /healthz`）。`.dockerignore` 收窄上下文（仅 web/server 源，排除 node_modules/dist/data/.git 等）。`docker build` 成功，镜像 31.8MB；smoke run：/healthz=200、/admin/ 返回真实内嵌 SPA、容器内 `id`=uid 100(ouro) 非 root、HEALTHCHECK present。

## 6. Validation Evidence (append-only)
- （待执行后按 `TC-<n> | stack: docker|shell|other | command: ... | result: pass|fail | note: ...` 追加）

- TC-1 | stack: docker | command: docker build -t ouropass/issuer:local . | result: pass | note: p1-1 三阶段构建成功，镜像 31.8MB（含 SPA embed）
- TC-1 | stack: docker | command: docker run + curl /healthz /admin/ | result: pass | note: /healthz=200；/admin/ 返回真实内嵌 SPA（非 placeholder）
- TC-4 | stack: docker | command: docker exec id + inspect Healthcheck | result: pass | note: 运行用户 uid 100(ouro) 非 root；HEALTHCHECK 已配置；基础镜像 tag 钉死(node:22/golang:1.25/alpine:3.20)
- TC-1 | stack: docker | command: docker compose config | result: pass | note: p1-2/p1-3 解析无误；插值正确(OUROPASS_ISSUER=https://${DOMAIN}、DB_DSN→postgres:5432、images 钉死)
- TC-2 | stack: docker | command: docker compose config（核对） | result: pass | note: caddy 暴露 80/443 + 反代 issuer:8080；issuer 无 published 端口；postgres/issuer depends_on condition: service_healthy
- TC-3 | stack: docker | command: docker compose config（卷核对） | result: pass | note: ./data/postgres→/var/lib/postgresql/data、./data/caddy→/data bind-mount；Caddyfile ro 挂载

## 7. Change Requests (append-only)
- （无）
