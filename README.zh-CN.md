# Skating

一个轻量级 CI/CD 自动化构建工具 —— 媲美 Jenkins 的能力，单二进制文件的极简部署。无需数据库、无需守护进程、零配置漂移。为 vibe coding 而生。

## 特性

- **一键安装** — 单二进制文件，无需数据库，数据存储于本地 YAML 文件
- **三层架构** — Pipeline DSL 编排 + Shell/Lua/SSH/SCP/Sync 执行引擎 + Yaegi 插件扩展
- **Docker 沙箱** — 在容器内隔离构建，环境一致
- **远程部署** — 原生 SSH 执行器 + 基于 SFTP 的 scp/sync 文件传输
- **跨平台** — Windows / Linux / macOS 全支持

## Skating vs Drone

| | Skating | Drone |
|---|---------|-------|
| **部署** | 单二进制，`go install` 即用 | 需要 Server + Runner + 数据库 |
| **数据库** | 无，本地 YAML 文件 | PostgreSQL / MySQL / SQLite |
| **配置** | 单文件 `.skating.yaml`，在项目内 | `.drone.yml` + Web UI 配置 |
| **执行层** | Shell + Lua (Gopher-Lua 沙箱) + SSH/SCP/Sync | 纯 Shell / 插件 |
| **插件** | Yaegi Go 解释器，原生性能 | Docker 容器化插件 |
| **并行** | Stage 级别原生 goroutine | 多 Runner / 多 Pipeline |
| **缓存** | 无外部依赖 | 需 Redis 或共享卷 |
| **安装成本** | < 30 秒 | 需 Docker Compose / K8s 部署 |

## 快速开始

### 安装

```bash
# 通过 go install 安装（需要 Go 1.25+）
go install github.com/hpgood/skating/cmd/skating@latest

# 或从源码编译
git clone https://github.com/hpgood/skating.git
cd skating
mkdir build
go build -ldflags="-X main.version=0.2.0" -o build/skating ./cmd/skating/

# 或用 skating 自举编译（读取本仓库的 .skating.yaml 流水线）
skating run

# 验证
./build/skating version
```
```
skating 0.2.0
  commit : abc123
  built  : 2024-01-01
```

# 或使用安装脚本
# Linux/macOS:
bash scripts/install.sh

# Windows (PowerShell):
.\scripts\install.ps1
```

### 初始化项目

```bash
cd /path/to/your/project
skating init          # 生成 .skating.yaml 配置模板
skating run           # 执行构建（项目名可选 —— 默认当前目录）
```

## 命令

| 命令 | 说明 |
|------|------|
| `skating init` | 当前目录初始化项目，生成 `.skating.yaml`、`scripts/` |
| `skating run <项目>` | 按 Pipeline 编排执行构建（项目名可选 —— 默认当前目录） |
| `skating update` | 将当前目录 `.skating.yaml` 的 name/image 同步到全局项目注册 |
| `skating config [项目]` | 查看项目配置（项目名可选 —— 默认当前目录） |
| `skating logs [项目]` | 查看构建日志（支持 `--last N`、`--id N`；项目名可选 —— 默认当前目录） |
| `skating clean [项目]` | 清空构建日志（保留 BuildID 和项目注册信息；项目名可选 —— 默认当前目录） |
| `skating version` | 输出版本号（含 commit 和构建日期） |
| `skating skill` | 输出 SKILL 指南（供 AI 智能体使用） |
| `skating ls` | 列出所有已注册项目及构建状态 |

所有命令默认输出英文，可通过 `--lang zh-CN` 切换为中文。示例：`skating run myproject --lang zh-CN`

### `skating run` 选项

项目名是可选参数：不带参数的 `skating run` 会执行当前目录的 `.skating.yaml`（adhoc 模式）并自动注册项目。此外 `run` 命令支持两个 flag 用于选择性子集 / 参数化构建：

| Flag | 格式 | 说明 |
|---|---|---|
| `--step` | `<stage>` 或 `<stage>/<step>` | 只跑指定 stage（或单个 step）。其他 stage 和同 stage 内其他 step 在 summary 里以 `skipped` 显示，但不会执行。 |
| `--env`  | `KEY=VAL`（可重复） | 注入自定义环境变量到本次运行的每个 step。用户传入值**优先级高于**内置 `SKA_BUILD_*` 变量。 |

#### 用 `--step` 选择性执行

```bash
# 只跑 build 整个 stage（sequential step 失败仍跑完整个 stage）
skating run myproj --step build

# 只跑 build stage 里的 compile 单个 step
skating run myproj --step build/compile

# 未知 stage → 报错并给出 stage 名单
$ skating run myproj --step nonexistent
ERROR: --step "nonexistent" 中 stage "nonexistent" 不存在。已知 stage: [build test deploy]
```

行为说明：
- 没选中的 stage 在 summary 里以 `○ skipped` 显示 —— 用户始终看得到哪些 stage 没跑到。
- `--step build`（整 stage）模式下，sequential step 失败**继续**跑后续 step，让用户看到完整 stage 全貌。
- `--step build/compile`（单个 step）保留原"遇错即停"语义。
- 多 stage 顺序：选中的 stage 失败时，后续 stage 仍记为 `skipped`（不会悄悄漏掉），summary 完整。

#### 用 `--env` 注入自定义环境变量

```bash
# 注入单个变量
skating run myproj --env DEPLOY_ENV=staging

# 多个值；同名变量后者覆盖前者
skating run myproj --env DEPLOY_ENV=staging --env REGION=us-west-2

# 覆盖内置 SKA_BUILD_*（如用于重放/调试）
skating run myproj --env SKA_BUILD_ID=999

# 两个 flag 组合：只跑 release step 并指定自定义目标
skating run myproj --step deploy/release --env DEPLOY_TARGET=canary
```

`--env` 格式错误（缺 `=`、KEY 为空）会在任何 step 执行前明确报错。

#### `skating update`

将当前目录 `.skating.yaml` 的 name / image 同步到全局项目注册：

```bash
cd /path/to/your/project
skating update
```

行为：
- **未注册** → 注册新项目（YAML 未设置 name 时用目录名）
- **同名项目已存在** → 更新其 `image` 和 `path`
- **同路径但 YAML 里改了名** → 重命名项目记录

## 三层架构

```
┌─────────────────────────────┐
│  Pipeline DSL (YAML)        │  ← Stage / Parallel / When 编排
├─────────────────────────────┤
│  执行引擎                    │  ← Shell + Lua + SSH/SCP/Sync
├─────────────────────────────┤
│  插件扩展 (Yaegi)            │  ← Go 插件动态加载，通知/审查等
└─────────────────────────────┘
```

### Pipeline 配置示例

```yaml
# .skating.yaml
name: myproject
image: golang:1.21

pipeline:
  stages:
    - name: build
      steps:
        - name: compile
          type: shell
          script: go build ./...

    - name: test
      parallel: true            # 并行执行
      steps:
        - name: unit-test
          type: shell
          script: go test ./...
        - name: lint
          type: shell
          script: golangci-lint run

    - name: deploy
      steps:
        - name: release
          type: lua             # Lua 脚本执行
          script: |
            log("Build: " .. getenv("SKA_BUILD_ID"))
            sh("docker push myapp:" .. getenv("SKA_BUILD_ID"))
        - name: notify
          type: shell
          when: branch == "main"  # 条件执行
          script: |
            echo "生产环境部署完成"
```

## 环境变量

每次构建自动注入以下环境变量（基于构建开始时间，UTC 时区）：

| 变量 | 类型 | 示例 | 说明 |
|---|---|---|---|
| `SKA_BUILD_ID`        | 整数（字符串） | `1`           | 自增构建编号（从 1 起） |
| `SKA_BUILD_TIMESTAMP` | 整数（字符串） | `1787139682`  | 构建开始时间的 **Unix 秒（UTC）**——便于排序 / 算 diff / 数值比较 |
| `SKA_BUILD_DATE`      | 字符串         | `2026-08-19T11:41:22Z` | 构建开始时间的 **RFC3339（UTC）**——人类可读，用于报告 / 日志 |
| `SKA_GIT_COMMIT_ID`   | 字符串         | `9a2c1f0d...` | 项目当前的 git commit id（**完整 hash**）；项目无 `.git` 时为空 |
| `SKA_OS`              | 字符串         | `linux`       | 运行时操作系统类型（`runtime.GOOS`）：`linux`、`windows`、`darwin`... |

Shell 脚本用法：
```bash
echo "Building version ${SKA_BUILD_ID} at ${SKA_BUILD_DATE}"
echo "Local time: $(date -d @${SKA_BUILD_TIMESTAMP})"
```

Lua 脚本用法（沙箱没有 `os` 模块，需用 `getenv`）：
```lua
log("Build: " .. getenv("SKA_BUILD_ID"))
sh("docker push myapp:" .. getenv("SKA_BUILD_ID"))
local ts = getenv("SKA_BUILD_TIMESTAMP")
log("Unix seconds: " .. ts)
```

### Step 级环境变量（`env`）

每个 step 可以定义自己的环境变量，按平台分组。`default` 组注入所有平台；其他组（`windows`、`linux`、`darwin`...）仅当组名与当前操作系统（GOOS）一致时注入，平台专属值**优先覆盖** `default` 里的同名变量：

```yaml
steps:
  - name: compile
    type: shell
    env:
      default:
        version: "0.2.1"
      windows:
        GOARCH: "amd64"
        GOOS: "windows"
        Ext: ".exe"
      linux:
        GOARCH: "amd64"
        GOOS: "linux"
        Ext: ""
    script: |
      go build -ldflags="-X main.version=${version}" -o "build/skating${Ext}" ./cmd/skating
```

### Stage 级环境变量（`env`）

stage 也可以定义 `env`，由**该 stage 内所有 step 共享**（平台分组规则与 step env 相同）。同名变量优先级：构建 env（`SKA_*`、`--env`）< stage env < step env：

```yaml
stages:
  - name: build
    env:                 # 该 stage 内所有 step 共享
      default:
        version: "0.2.1"
      windows:
        Ext: ".exe"
      linux:
        Ext: ""
    steps:
      - name: compile
        type: shell
        script: |
          go build -ldflags="-X main.version=${version}" -o "build/skating${Ext}" ./cmd/skating
```

stage env 变量对脚本（`${VAR}`）和 `when` 条件均可见。

### 用 `SKA_SHELL` 指定脚本执行方式

`shell` step 默认使用的 shell：

| 平台 | 默认 shell | 实际执行命令 |
|---|---|---|
| Windows | `powershell` | `powershell -ExecutionPolicy Bypass -File <script>` |
| Linux / macOS | `bash` | `bash -x <script>` |

通过 `SKA_SHELL` 环境变量（写在 step 的 `env` 里，或命令行 `--env` 传入）可以为 step 的 `script` 指定其他 shell。值按空格分割：第一个是 shell 可执行文件，其余作为参数，脚本路径会自动追加：

```yaml
env:
  default:
    SKA_SHELL: "pwsh -NoLogo"
# 实际执行: pwsh -NoLogo <script>
```

## 远程执行与文件传输（SSH / SCP / Sync）

Step 可以在远程服务器上执行命令，或在本地与远端之间传输文件。
三种类型复用同一组连接字段（`host` / `port` / `user` / `auth` / `secret`），
底层由原生 Go SSH 实现（`golang.org/x/crypto/ssh`）驱动——无需本地安装
ssh.exe、sshpass 或 scp。

Secret 按名字引用（用 `skating secret set <name>` 存储，支持密码或 PEM
私钥）；secret 值永远不会出现在 yaml 和日志中。

```yaml
steps:
  # 1) 在远程服务器上执行命令
  - name: remote-build
    type: ssh
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass          # password（默认）或 "key" 认证
    workdir: /opt/app
    script: |
      ./deploy.sh ${SKA_GIT_BRANCH}

  # 2) scp：本地→远端复制文件/目录（push，默认方向），或远端→本地（pull）
  - name: upload-artifact
    type: scp
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass
    local: dist/app-${SKA_GIT_BRANCH}.tar.gz
    remote: /opt/app/app.tar.gz

  # 3) sync：本地目录的 rsync 式增量镜像上传
  - name: sync-dist
    type: sync
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass
    local: dist
    remote: /opt/app/dist
    delete: true                 # 默认：删除本地已不存在的远端文件
```

行为说明：
- `scp` 支持双向：`direction: push`（本地→远端，默认）和 `direction: pull`
  （远端→本地）。文件与目录均递归复制，并保留 mtime——后续 `sync`
  可据此跳过未变化文件
- `sync` 只上传有变化的文件（按 size + mtime 秒级精度比较），且默认镜像
  本地目录：删除远端已不存在的文件。首次部署时远端目录可以不存在
- `local` / `remote` 支持 `${VAR}` 变量展开（取自 step env 视图：
  `SKA_*` 系统变量、step env、跨 step 变量池）。`host`、`user` 与 `workdir`
  （ssh）同样支持 `${VAR}`（如 `host: 10.0.0.${SEG}`）——未定义变量保留原样，
  拼错变量名会在连接时暴露原始值，而非静默连错主机
- 构建取消/超时会自动关闭 SSH 连接

## 数据存储

所有数据存储在 `~/.skating/`：

```
~/.skating/
├── projects.yaml              # 项目注册表
└── logs/<项目名>/<ID>.log     # 构建日志
```

## 示例

参见 [examples/](./examples/) 目录：

| 示例 | 说明 |
|------|------|
| [go-project](./examples/go-project/) | Go 标准 CI：build → test → lint |
| [node-project](./examples/node-project/) | Node.js CI：install → build → test |
| [mixed-shell-lua](./examples/mixed-shell-lua/) | Shell + Lua 混合流水线 |
| [parallel-stages](./examples/parallel-stages/) | 并行 Stage 执行 |
| [conditional-build](./examples/conditional-build/) | When 条件判断 |

### 用 examples 试用 `--step` 和 `--env`

每个 example 的 `.skating.yaml` 都有清晰的 stage/step 命名，挑一个动手试：

```bash
# 混合 shell + Lua：3 个 stage，最适合演示 --step 粒度
skating run mixed-shell-lua-example --step shell-stage             # 只跑 stage 1
skating run mixed-shell-lua-example --step lua-stage/lua-build-with-logic  # 只跑单个 Lua step
skating run mixed-shell-lua-example --step shell-verify            # 只跑验证 stage

# 覆盖 SKA_BUILD_* 用于重放/调试
skating run go-project --env SKA_BUILD_ID=999

# 组合使用：只跑 release step 并指定自定义目标
skating run node-project --step deploy/release --env DEPLOY_TARGET=canary
```

完整演练参见 [`examples/mixed-shell-lua/README.md`](./examples/mixed-shell-lua/README.md)。

## License

MIT