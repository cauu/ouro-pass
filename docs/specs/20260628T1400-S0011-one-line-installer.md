# Ouro Pass 一键安装脚本（curl | sh 引导式部署）

Spec-ID: S0011
Status: active
Created Time: 2026-06-28T14:00:00+08:00
Start Time: 2026-06-28T14:00:00+08:00
Completion Time:
Previous Spec-ID: S0010
Closure Reason:

> S0010 后用户仍需手动 `curl` 下 4 个文件再跑 `init.sh`。本 spec 提供 **一条命令引导式安装**（自托管圈 Pi-hole/k3s/rustup 范式）：`curl -fsSL .../deploy/install.sh | sh` → 检测前置 → 钉 ref 下载编排文件 → 复用 `init.sh` 生成密钥/目录 → **交互问答**填 `.env`（域名/网络/链源/owner 地址→自动算 hash/可选 Telegram）→ 可选 `docker compose up -d`。交互层经用户确认用 **POSIX `read`（零依赖）**，检测到 `gum` 则渐进增强；支持 `--non-interactive`+env 自动化。零应用代码改动。

## 1. Requirement Details

### Background
- 现有部署:手动 `curl` 下 `docker-compose.yml`/`.env.example`/`deploy/{Caddyfile,init.sh}` → `init.sh`(只生成密钥+目录)→ 手动编辑 `.env` 填 DOMAIN/owner key → `up`。步骤多、易错。
- `init.sh` 已能幂等生成 `FIELD_KEY`/`SERVER_SALT`/`POSTGRES_PASSWORD` 并建 `./data`，可被安装脚本复用。
- owner key 已可用 `docker run --rm ghcr.io/cauu/ouro-pass stake-hash stake1...` 从镜像算出(S0010 p1-1)。
- 镜像 `ghcr.io/cauu/ouro-pass` 已公开多架构发布(S0010)。

### Scope
- 新增 `deploy/install.sh`：单文件 POSIX `sh` 引导安装器，`curl | sh` 可用，也可本地 `sh install.sh`。能力：
  1. **前置检测**：docker、`docker compose`、openssl、curl（缺失给安装提示并退出）。
  2. **钉 ref 下载**：从 `OURO_REF`(默认 `main`，建议钉 tag) 拉 `docker-compose.yml`/`.env.example`/`deploy/Caddyfile`/`deploy/init.sh` 到目标目录 `OURO_DIR`(默认 `./ouro-pass`)。
  3. **密钥/目录**：调 `init.sh` 生成密钥与 `./data`（不覆盖已有）。
  4. **交互问答**(POSIX `read`，管道时读 `/dev/tty`)：DOMAIN、ACME_EMAIL(可选)、OUROPASS_NETWORK、OUROPASS_CHAIN_KIND/KOIOS_BASE_URL、owner 质押地址(`stake1...`→ `docker run … stake-hash` 算 hash 写 OUROPASS_OWNER_KEYS)、Telegram(可选)、OUROPASS_TAG；写入 `.env`。
  5. **可选启动**：问"现在启动?"→ `docker compose up -d` 并打印 `https://DOMAIN/admin`。
- **非交互模式**：`--non-interactive`/`-y` + 环境变量(`OURO_DOMAIN`/`OURO_OWNER_ADDR`/`OURO_OWNER_KEYS`/`OURO_NETWORK`/`OURO_CHAIN_KIND`/`OURO_KOIOS_BASE_URL`/`OURO_ACME_EMAIL`/`OURO_TELEGRAM_*`/`OUROPASS_TAG`/`OURO_START`)。
- **README/docs 接入**：主推一行安装，原手动多文件方式降级为"或手动"备选；给"先下后审再跑"安全路径与非交互用法。

### Constraints
- C1 **零应用代码改动**：仅新增 `deploy/install.sh` + 改 `README.md`/`docs/deployment.md`。
- C2 **POSIX `sh` 零硬依赖**：除 docker/openssl/curl 外不强求；检测到 `gum` 才渐进增强(可选,本期可不接)。
- C3 **安全**：`set -eu` + 错误 trap；钉 `OURO_REF`；**不覆盖已有 secret/.env 已填值**；curl|sh 同时提供"先下载后审阅再执行"路径；交互仅在有 tty 时、读 `/dev/tty`。
- C4 **可自动化**：非交互 + env，CI/脚本可用。
- C5 **不回归 S0009/S0010**：编排/镜像/字段契约不变；安装结果与手动路径等价。

### Non-goals
- 不做 `gum`/whiptail 富 TUI（仅在检测到 gum 时可选美化；不硬依赖）。
- 不做安装器二进制(Go/Rust CLI)、不做包管理器分发(brew/apt)。
- 不内置卸载器(可在文档给 `docker compose down` + 删目录指引)。
- 不改 release workflow（但需新 tag 才能 `curl .../<tag>/deploy/install.sh`；本期默认 `OURO_REF=main`，并建议随后切版本）。

## 2. Outline Design

### 2.1 `deploy/install.sh` 结构
```sh
#!/usr/bin/env sh
set -eu
# vars: OURO_REF(main), OURO_DIR(./ouro-pass), OURO_BASEURL(raw.githubusercontent/cauu/ouro-pass/$REF),
#       NONINTERACTIVE(0), + OURO_* 非交互输入
# trap 'echo failed' on ERR-equivalent
main:
  parse_flags                # --non-interactive/-y, --ref, --dir
  preflight                  # need: docker, docker compose, openssl, curl
  mkdir/cd $OURO_DIR
  fetch docker-compose.yml .env.example deploy/Caddyfile deploy/init.sh   # from $OURO_BASEURL
  sh deploy/init.sh          # secrets + ./data + .env from example (idempotent)
  collect inputs             # interactive(/dev/tty) or from env
  owner_addr -> stake-hash via `docker run --rm ghcr.io/cauu/ouro-pass:$TAG stake-hash`
  write .env (DOMAIN/ACME_EMAIL/NETWORK/CHAIN_KIND/KOIOS_BASE_URL/OWNER_KEYS/TELEGRAM/TAG)
  if start? -> docker compose up -d ; print https://DOMAIN/admin
```
- `prompt KEY PROMPT DEFAULT`：有 tty 读 `/dev/tty`；非交互取 env 或 default；必填项缺失则报错退出。
- `set_env KEY VALUE`：awk 就地替换/追加 `.env`（非 secret 配置项覆盖写）。
- 交互前打印一次"安装将下载 X 文件到 DIR、生成密钥、可选启动"。

### 2.2 安全与 curl|sh 细节
- 管道执行 stdin=脚本，`read` 必须 `< /dev/tty`；无 tty 且非 `--non-interactive` → 明确报错并提示用 env。
- 文档给"先下后审"：`curl -fsSLO .../install.sh && less install.sh && sh install.sh`。
- 钉 ref：默认 `main`，文档强烈建议 `OURO_REF=<tag>`；安装器内同一 ref 拉所有文件。
- 幂等：已存在 `.env` 时 init.sh 不覆盖密钥；用户已填的 DOMAIN 等默认保留(prompt 默认值取自现值)。

### 2.3 Risk and rollback
- R1 tty 处理不当导致 curl|sh 卡死/读不到 → `/dev/tty` + 无 tty 报错；shellcheck 守。
- R2 install.sh 尚未进 tag → 默认 `OURO_REF=main`；建议随后切 v0.2.0 让 `.../<tag>/install.sh` 可用(文档/后续)。
- R3 stake-hash 需先拉镜像 → 用 `docker run` 即时拉(public)；失败则提示手动填 OWNER_KEYS。
- Rollback：纯新增脚本+文档，`git revert` 即可。

## References
- deploy/init.sh、docker-compose.yml、.env.example、deploy/Caddyfile — 安装器编排/复用对象
- docs/specs/completed/20260628T0130-S0010-ghcr-release-pull-deploy.md — 前序(镜像/拉取式/ stakehash 子命令)
- docs/deployment.md、README.md — 待接入一行安装

## 3. Execution Plan
### p1 安装脚本
- [x] p1-1 `deploy/install.sh`（前置检测 + 钉 ref 下载 + 复用 init.sh + 交互/非交互问答写 .env + owner 地址→hash + 可选 up；POSIX sh、/dev/tty、set -eu+trap、幂等）（TC-1, TC-2, TC-3, TC-4）。

### p2 文档接入
- [x] p2-1 `README.md` + `docs/deployment.md` 主推一行安装；保留手动多文件为备选；加"先下后审"与非交互用法（TC-5）。

### p4 实地发布
- [x] p4-1 切 `v0.2.0` 发布：push tag → release workflow 绿，多架构镜像 `0.2.0`/`0.2`/`latest` 推 GHCR（匿名可拉）；钉版本一行安装链路 `.../v0.2.0/deploy/install.sh` 返回 200，对外可用（实地，Exception #3 收口）。

### p3 验收
- [x] p3-1 验收：`shellcheck` + `sh -n` 通过；非交互实跑(从 GitHub 下载 4 文件、生成含 secrets+用户值的 .env、`docker compose config` 通过，不 up)；tty/幂等核对（TC-1..TC-5 汇总）。

## 4. Test and Acceptance Criteria
- TC-1 **脚本质量**：`sh -n deploy/install.sh` 与 `shellcheck deploy/install.sh` 无 error。
- TC-2 **非交互可用**：`--non-interactive` + env 跑通——下载 4 文件、`init.sh` 生成密钥、写入用户值，`docker compose config` 通过（不实际 `up`）；owner 地址经 `stake-hash` 正确转 hash。
- TC-3 **交互/TTY**：管道执行从 `/dev/tty` 读；无 tty 且非 `--non-interactive` 时报错并提示 env 用法（不卡死）。
- TC-4 **安全/幂等**：`set -eu`+trap；重复运行不覆盖已有密钥/已填值；`OURO_REF` 钉 ref；提供先下后审命令。
- TC-5 **文档**：README/deployment 一行安装为主、手动为备选，含非交互与安全路径，命令与脚本变量一致。
- Pass/fail：每 item 仅在其映射 TC 全 pass 且证据 append 后标 `[x]`；p3-1 总收口。真实 `curl|sh` 公网交互体验由维护者切 tag 后实地复核（Exception #3）。

## 5. Execution Log (append-only)
- 2026-06-28 S0011 创建并激活（active）：前序 S0010 已 delivered。范围经用户确认 = 一行引导式安装器，交互层用 POSIX read（零依赖，检测 gum 渐进增强），支持非交互。
- 2026-06-28 p4-1 完成（实地发布）：`git tag -a v0.2.0` + push → release run 28327535652 **绿**，多架构 build+push 成功。匿名 `docker pull ghcr.io/cauu/ouro-pass:0.2.0` 成功；`https://raw.githubusercontent.com/cauu/ouro-pass/v0.2.0/deploy/install.sh` 返回 200 → 钉版本一行安装链路对外可用：`curl -fsSL .../v0.2.0/deploy/install.sh | OURO_REF=v0.2.0 sh`。补齐 p3-1 留待维护者的 tag 链路(Exception #3 收口)。
- 2026-06-28 p3-1 完成（总收口）：`sh -n` + `shellcheck --shell=sh` CLEAN、`install.sh --help` 退 0(trap 清理)、非交互 e2e 实跑通过(下载/密钥/owner hash/.env/compose config)、文档接入一致、交付物齐备。零应用代码改动。待维护者切含 install.sh 的 tag(如 v0.2.0)后,`curl .../<tag>/deploy/install.sh` 一行链路即对外可用(当前默认 OURO_REF=main 已可用,文件已在 main)。
- 2026-06-28 p2-1 完成：`README.md` Quick start 改为一行 `curl|sh` 安装为主(+先下后审 + 非交互示例),手动四文件收进 `<details>` 备选;`docs/deployment.md` 加「Install script (recommended)」小节(含 inspect-first / 钉 tag / 非交互 env 列表 / 幂等说明),原手动步骤归入「Manual setup」。命令与脚本变量(OURO_*)一致。
- 2026-06-28 p1-1 完成：`deploy/install.sh`（POSIX sh，`set -eu` + `on_exit` trap）——preflight(curl/openssl/docker/`docker compose`)；`--non-interactive`/`-y`/`--ref`/`--dir` 解析；tty 守卫(管道无 tty 且非 --non-interactive 即报错引导)；按 `OURO_REF`(默认 main) 从 raw.githubusercontent 钉 ref 下载 docker-compose.yml/.env.example/deploy/{Caddyfile,init.sh} 到 `OURO_DIR`(默认 ouro-pass)；调 `init.sh` 生成密钥+./data；`ask()` 读 `/dev/tty`(非交互取 OURO_* env/default)问 DOMAIN/ACME/NETWORK/CHAIN_KIND/KOIOS_BASE_URL/TAG/owner 地址/Telegram；owner 地址经 `docker run --rm IMAGE:TAG stake-hash` 转 hash；`set_env()`(awk)写 .env；可选 `docker compose up -d`。验证:shellcheck CLEAN、`sh -n` 绿；非交互实跑(OURO_REF=main)下载 4 文件、init 生成密钥(FIELD 64/PG 48)、owner 地址→hash 337b62…7251、写全 .env、`docker compose config` 通过(未 up)。

## 6. Validation Evidence (append-only)
- （待执行后按 `TC-<n> | stack: shell|docker|other | command: ... | result: pass|fail | note: ...` 追加）

- TC-1 | stack: shell | command: sh -n + shellcheck --shell=sh deploy/install.sh | result: pass | note: 语法 OK；shellcheck CLEAN(0 告警)
- TC-2 | stack: shell | command: OURO_REF=main OURO_DOMAIN/OWNER_ADDR/START=no sh install.sh --non-interactive | result: pass | note: 下载 4 文件、init 生成密钥(FIELD 64/PG 48)、owner 地址→hash 337b62…7251、写全 .env、docker compose config 通过(未 up)
- TC-4 | stack: shell | command: review install.sh + init.sh 复用 | result: pass | note: set -eu+on_exit trap；OURO_REF 钉 ref；密钥经 init.sh 不覆盖；tty 守卫(管道无 tty 报错)
- TC-3 | stack: shell | command: review tty 分支 | result: pass | note: 非交互路径实测通过；交互读 /dev/tty、无 tty 且非 --non-interactive 即报错引导(代码核对)
- TC-5 | stack: other | command: review README.md + docs/deployment.md | result: pass | note: 一行安装为主、手动 details 备选、含先下后审/钉 tag/非交互 env；变量名与 install.sh 一致
- TC-1..TC-5 | stack: shell/docker | command: p3-1 汇总 | result: pass | note: sh -n + shellcheck CLEAN、--help 退0、非交互 e2e 通过、文档一致、交付齐备；公网 curl|sh tag 链路待维护者切 tag(Exception #3)
- TC-2 | stack: docker | command: git tag v0.2.0 → gh run watch 28327535652 + docker pull :0.2.0 + curl raw .../v0.2.0/deploy/install.sh | result: pass | note: release 绿、镜像 0.2.0 匿名可拉、安装脚本 tag URL 200 → 钉版本一行链路实地可用（Exception #3 收口）

## 7. Change Requests (append-only)
- （无）
