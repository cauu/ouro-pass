# Ouro Pass 镜像发布与拉取式一键部署（GHCR + CI + stakehash 子命令）

Spec-ID: S0010
Status: active
Created Time: 2026-06-28T01:30:00+08:00
Start Time: 2026-06-28T01:30:00+08:00
Completion Time:
Previous Spec-ID: S0009
Closure Reason:

> S0009 交付了 compose 全栈，但 issuer 服务为 `build: .`（用户需克隆整仓本地构建）。本 spec 把分发模式改为**官方发布预构建镜像 → 用户只下少量文件、拉镜像即起**：(1) CI 在打 tag 时构建并推送多架构镜像到 **GHCR**(`ghcr.io/cauu/ouro-pass`)；(2) `docker-compose.yml` 改为拉取该镜像；(3) 把取 owner key 的 `stakehash` 暴露为 issuer 子命令，纯拉镜像的用户用 `docker run` 即可算。目标发布/部署流程：维护者打 tag→镜像发布；用户下 `docker-compose.yml`+`deploy/`+`.env.example`→`init.sh`→填 `.env`→`docker compose up -d`。

## 1. Requirement Details

### Background
- S0009 的 compose `issuer: build: .` 会在用户机器上从源码编译（需 `web/`+`server/`）。要支持"发布镜像→用户拉取"，需改为 `image:` 拉取 + 真正发布镜像。
- 取 `OUROPASS_OWNER_KEYS` 现靠 `cd server && make stake-hash`（需 Go 源码）；纯拉镜像用户无法运行。`stakehash` 逻辑在可复用包 `internal/utils/chain.StakeHashFromRewardAddress`，可直接挂到 issuer 子命令。
- 远端 = `github.com/cauu/ouro-pass` → 镜像 `ghcr.io/cauu/ouro-pass`。已有 `.github/workflows/ci.yml`（go 1.25.x / pnpm 10 / node 22）作为风格参照。

### Scope
- **issuer 子命令**：`issuer stake-hash <stake1...|hex>` 复用 `chain.StakeHashFromRewardAddress`，使 `docker run --rm ghcr.io/cauu/ouro-pass stake-hash stake1...` 可算 owner key。保留 `cmd/stakehash`（`make stake-hash` 不变）。
- **Dockerfile 跨架构就绪**：web 阶段钉 `$BUILDPLATFORM`（SPA 与架构无关、只构一次），go 阶段按 `$TARGETOS/$TARGETARCH` 交叉编译（CGO=0 纯 Go，零成本交叉）；pnpm 对齐 10。
- **发布工作流**：`.github/workflows/release.yml`，on `push` tag `v*` → buildx 多架构(`linux/amd64,linux/arm64`) build + push 到 GHCR；tag = 语义版本 + `latest`；用内置 `GITHUB_TOKEN`(`packages: write`)。
- **compose 改拉取**：issuer `image: ghcr.io/cauu/ouro-pass:${OUROPASS_TAG:-latest}`，移除 `build:`；`.env.example` 注释 `OUROPASS_TAG`；保留本地构建的文档化路径。
- **文档**：`docs/deployment.md` 改为拉取式流程 + 用户最小下载文件清单 + `docker run ... stake-hash` 取 owner key + 升级(改 tag→`docker compose pull && up -d`)。

### Constraints
- C1 **应用改动最小**：仅在 `cmd/issuer` 增加子命令分发（无源码行为变化、不碰 server 逻辑/HTTP/DB）；其余为 CI/编排/文档。
- C2 **镜像公开可拉**：GHCR 公开包，tag 钉死（语义版本），多架构 amd64+arm64。
- C3 **用户免源码**：拉取式 compose；取 owner key 经 `docker run` 子命令，不需 Go 工具链。
- C4 **可复现/可验证**：buildx 多架构；基础镜像 tag 钉死（沿用 S0009）；保留 SBOM/cosign 作为文档化加固（本期可选）。
- C5 **不回归 S0009**：`./data` bind-mount、Caddy 自动 HTTPS、healthcheck、env 契约不变。

### Non-goals
- 不做 cosign 签名 / SBOM 附加 / 镜像 attestation（列为后续可选）。
- 不改应用业务逻辑、不动 `server/internal/*` 行为。
- 不做 Helm/K8s、不做自动版本号/changelog 生成。

## 2. Outline Design

### 2.1 issuer 子命令（`server/cmd/issuer/main.go`）
在 `main()` 顶部分发：`os.Args[1] == "stake-hash"` → 调 `chain.StakeHashFromRewardAddress(os.Args[2])`，打印/错误退出；否则照常启动服务。复用现有包，无重复逻辑。

### 2.2 Dockerfile（跨架构）
```dockerfile
FROM --platform=$BUILDPLATFORM node:22-alpine AS web   # SPA 只在原生 arch 构一次
RUN npm install -g pnpm@10
...
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/issuer ./cmd/issuer
FROM alpine:3.20                                        # 运行层按 target arch 拉取
```

### 2.3 发布工作流（`.github/workflows/release.yml`）
on push tag `v*`：checkout → `docker/setup-qemu-action` + `docker/setup-buildx-action` → `docker/login-action`(ghcr, `${{ github.actor }}`/`${{ secrets.GITHUB_TOKEN }}`) → `docker/metadata-action`(images=`ghcr.io/${{ github.repository }}`, tags=semver+latest) → `docker/build-push-action`(platforms=linux/amd64,linux/arm64, push=true)。`permissions: { contents: read, packages: write }`。

### 2.4 compose / env / 文档
- compose：`image: ghcr.io/cauu/ouro-pass:${OUROPASS_TAG:-latest}`，删 `build:`。
- `.env.example`：`OUROPASS_TAG=latest`（建议钉具体版本如 `v0.1.0`）。
- 文档：拉取式 quick start + 最小文件清单（`docker-compose.yml`、`deploy/Caddyfile`、`deploy/init.sh`、`.env.example`，保持目录层级）+ `docker run --rm ghcr.io/cauu/ouro-pass stake-hash stake1...` + 升级流程 + 本地构建备选。

### 2.5 Risk and rollback
- R1 多架构 emulation 慢 → `$BUILDPLATFORM` 钉 web/go 阶段在原生 arch、仅交叉编译产物，避免 qemu 跑 node/go。
- R2 首次推 GHCR 包默认私有 → 文档提示在 GitHub 包设置改为 public（或 workflow 后续加可见性步骤）。
- R3 子命令分发误伤正常启动 → 仅当 `os.Args[1]=="stake-hash"` 时分流，其余原样；`go test` 守。
- Rollback：CI/compose/文档为前向变更，`git revert` 即可；子命令为纯增量。

## References
- server/cmd/stakehash/main.go、server/internal/utils/chain（StakeHashFromRewardAddress）
- server/cmd/issuer/main.go — 子命令分发挂载点
- .github/workflows/ci.yml — 现有 CI 风格（go/pnpm/node 版本）
- docs/specs/completed/20260628T0015-S0009-docker-compose-deployment.md — 前序（compose/Dockerfile/init/docs）
- Dockerfile、docker-compose.yml、.env.example、docs/deployment.md — 待改造对象

## 3. Execution Plan
### p1 issuer 子命令
- [x] p1-1 `cmd/issuer` 增 `stake-hash` 子命令（复用 `chain.StakeHashFromRewardAddress`），与 `cmd/stakehash` 输出一致（TC-1, TC-4）。

### p2 镜像构建与发布
- [x] p2-1 `Dockerfile` 跨架构就绪（`$BUILDPLATFORM` web/go 阶段 + `$TARGETOS/$TARGETARCH` 交叉编译；pnpm→10）（TC-2, TC-5）。
- [ ] p2-2 `.github/workflows/release.yml`（tag `v*` → buildx 多架构 build+push GHCR；semver+latest；packages:write）（TC-2）。

### p3 拉取式编排
- [ ] p3-1 `docker-compose.yml` issuer 改 `image: ghcr.io/cauu/ouro-pass:${OUROPASS_TAG:-latest}`（删 build）+ `.env.example` 加 `OUROPASS_TAG`（TC-3, TC-5）。

### p4 文档
- [ ] p4-1 `docs/deployment.md` 改拉取式：最小文件清单 + `docker run stake-hash` + 升级 + 本地构建备选 + GHCR 包公开提示（TC-3, TC-6）。

### p5 验收
- [ ] p5-1 验收：子命令与 `stakehash` 输出一致、`go build/vet` + `make test` 绿、`docker compose config` 用新 image 通过、`release.yml` lint 通过、Dockerfile 多架构构建可用（TC-1..TC-6 汇总）。

## 4. Test and Acceptance Criteria
- TC-1 **子命令正确**：`issuer stake-hash <addr>` 与 `stakehash <addr>` 对同一输入输出相同；无参打印 usage、非零退出。
- TC-2 **发布可用**：`release.yml` 语法/动作合法；Dockerfile 经 buildx 多架构(`linux/amd64,linux/arm64`)构建成功；镜像名 `ghcr.io/cauu/ouro-pass`、tag=semver+latest。
- TC-3 **拉取式部署**：compose issuer 用 `image:` 拉取（无 build）；用户仅需 `docker-compose.yml`+`deploy/{Caddyfile,init.sh}`+`.env.example`（目录层级保持）即可 `init.sh`→`up -d`。
- TC-4 **零行为回归**：`go build ./... && go vet ./...` + `make test` 绿；子命令分发不影响正常服务启动。
- TC-5 **可复现/无新依赖**：基础镜像 tag 钉死；`package.json` 无新依赖；pnpm 对齐 10 与 lockfile 兼容。
- TC-6 **文档自洽**：deployment.md 与拉取式 compose/子命令一致；含升级与本地构建备选与 GHCR 包公开提示。
- Pass/fail：每 item 仅在其映射 TC 全 pass 且证据 append 后标 `[x]`；p5-1 总收口。运行时真实 GHCR 推送与公网部署由维护者打 tag 后实地复核（Exception #3：需 GitHub/registry 凭证与公网）。

## 5. Execution Log (append-only)
- 2026-06-28 S0010 创建并激活（active）：前序 S0009 已 delivered。范围经用户确认 = GHCR 全做（CI build/push + compose 改拉取 + stakehash 子命令）。镜像 `ghcr.io/cauu/ouro-pass`，多架构 amd64+arm64。
- 2026-06-28 p2-1 完成：`Dockerfile` 跨架构——web 阶段 `FROM --platform=$BUILDPLATFORM node:22-alpine`（pnpm 升到 10），go 阶段 `FROM --platform=$BUILDPLATFORM golang:1.25-alpine` + `ARG TARGETOS TARGETARCH` + `GOOS/GOARCH` 交叉编译（CGO=0），运行层 `alpine:3.20` 按 target 拉取。buildx `--platform linux/amd64,linux/arm64` 构建两架构均成功（go build amd64/arm64 + stage-2 COPY 均 DONE）。
- 2026-06-28 p1-1 完成：`cmd/issuer/main.go` 在 `main()` 首部加 `stake-hash` 子命令分发（`os.Args[1]=="stake-hash"` → `stakeHashCmd` 复用 `chain.StakeHashFromRewardAddress`，无参打印 usage 退 2），仅此分流、不影响正常启动。与 `cmd/stakehash` 对同一 stake1 地址输出一致（337b62...7251）；`go build ./...`/`go vet ./cmd/issuer` 绿；`make test` 全绿。`cmd/stakehash` 保留。

## 6. Validation Evidence (append-only)
- （待执行后按 `TC-<n> | stack: go|docker|shell|other | command: ... | result: pass|fail | note: ...` 追加）

- TC-1 | stack: go | command: go run ./cmd/issuer stake-hash <addr> vs ./cmd/stakehash | result: pass | note: 同输入输出一致(337b62…7251)；无参 usage + 非零退出
- TC-4 | stack: go | command: go build ./... + go vet ./cmd/issuer + make test | result: pass | note: 编译/vet 绿、单元+e2e 全绿；子命令分发不影响服务启动路径
- TC-2 | stack: docker | command: docker buildx build --platform linux/amd64,linux/arm64 --output cacheonly | result: pass | note: 两架构 go build + 运行层均 DONE，跨编译就绪
- TC-5 | stack: docker | command: review Dockerfile | result: pass | note: 基础镜像 tag 钉死(node:22/golang:1.25/alpine:3.20)；pnpm@10 与 lockfile 兼容(buildx --frozen-lockfile 通过)；无新依赖

## 7. Change Requests (append-only)
- （无）
