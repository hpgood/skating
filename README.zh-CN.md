# Skating

一个轻量级 CI/CD 自动化构建工具 —— 媲美 Jenkins 的能力，单二进制文件的极简部署。无需数据库、无需守护进程、零配置漂移。为 vibe coding 而生。

## 特性

- **一键安装** — 单二进制文件，无需数据库，数据存储于本地 YAML 文件
- **三层架构** — Pipeline DSL 编排 + Shell/Lua/SSH/SCP/Sync/Sign 执行引擎 + Yaegi 插件扩展
- **Docker 沙箱** — 在容器内隔离构建，环境一致
- **远程部署** — 原生 SSH 执行器 + 基于 SFTP 的 scp/sync 文件传输
- **产物签名** — 内置 RSA/ECDSA/Ed25519 detached 签名与验签，密钥来自 skating secret
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
| `skating groups [项目]` | 列出 `pipeline.groups` 中的 group、stage 及说明（项目名可选 —— 默认当前目录） |
| `skating stages [项目]` | 按声明顺序列出 stage 及说明（项目名可选 —— 默认当前目录） |
| `skating update` | 将当前目录 `.skating.yaml` 的 name/image 同步到全局项目注册 |
| `skating config [项目]` | 查看项目配置（项目名可选 —— 默认当前目录） |
| `skating logs [项目]` | 查看构建日志（支持 `--last N`、`--id N`；项目名可选 —— 默认当前目录） |
| `skating clean [项目]` | 清空构建日志（保留 BuildID 和项目注册信息；项目名可选 —— 默认当前目录） |
| `skating stats` | 查看正在运行的任务及实时进度；支持 `--once`、`--all`、`--prune` |
| `skating stop [项目] <id>` | 停止正在运行的构建，或删除 stale（僵尸）记录 |
| `skating version` | 输出版本号（含 commit 和构建日期） |
| `skating skill` | 输出 SKILL 指南（供 AI 智能体使用） |
| `skating ls` | 列出所有已注册项目及构建状态 |

所有命令默认输出英文，可通过 `--lang zh-CN` 切换为中文。示例：`skating run myproject --lang zh-CN`

### `skating run` 选项

项目名是可选参数：不带参数的 `skating run` 会执行当前目录的 `.skating.yaml`（adhoc 模式）并自动注册项目。位置参数也支持 `<project>+<group>`（当前目录用 `+<group>`）来选择 group —— 等价于 `-g`/`--group`。此外 `run` 命令支持以下 flag 用于选择性子集 / 参数化构建：

```bash
skating run [项目名[+group]] [flags]
```

| Flag | 格式 | 说明 |
|---|---|---|
| `--step`、`-s` | `<stage>` 或 `<stage>/<step>` | 只跑指定 stage（或单个 step）。其他 stage 和同 stage 内其他 step 在 summary 里以 `skipped` 显示，但不会执行。 |
| `--group`、`-g` | `<name>` | 只跑指定 group 内的 stage（在 `.skating.yaml` 的 `pipeline.groups` 中定义）。与 `--step` 互斥。 |
| `--env`、`-e`  | `KEY=VAL`（可重复） | 注入自定义环境变量到本次运行的每个 step。用户传入值**优先级最高**：覆盖同名内置 `SKA_*`、stage env 与 step env。 |
| `--timeout` | `10m` / `600s` / `600` | 覆盖本次运行的超时时间（Go duration 或纯秒数）。优先级高于 `.skating.yaml` 的 `timeout` 字段。 |

#### 运行超时（`--timeout`）

整个流水线有一个运行超时，默认 **10m**，可在 `.skating.yaml` 中设置：

```yaml
name: myproj
timeout: 10m        # Go duration（"10m"/"600s"）或纯秒数（"600"）
pipeline:
  # ...
```

`--timeout` 可覆盖单次运行的值（格式相同）：

```bash
skating run myproj --timeout 5m
skating run myproj --timeout 600
```

超过超时时间后，正在运行的子进程会被终止，构建状态记为 `timeout`。

#### 用 `--step` 选择性执行

```bash
# 只跑 build 整个 stage（sequential step 失败仍跑完整个 stage）
skating run myproj --step build

# 只跑 build stage 里的 compile 单个 step
skating run myproj --step build/compile

# -s 是 --step 的短选项（等价于上一行）
skating run myproj -s build/compile

# 未知 stage → 报错并给出 stage 名单
$ skating run myproj --step nonexistent
ERROR: --step "nonexistent" 中 stage "nonexistent" 不存在。已知 stage: [build test deploy]
```

行为说明：
- 没选中的 stage 在 summary 里以 `○ skipped` 显示 —— 用户始终看得到哪些 stage 没跑到。
- `--step build`（整 stage）模式下，sequential step 失败**继续**跑后续 step，让用户看到完整 stage 全貌。
- `--step build/compile`（单个 step）保留原"遇错即停"语义。
- 多 stage 顺序：选中的 stage 失败时，后续 stage 仍记为 `skipped`（不会悄悄漏掉），summary 完整。

#### 用 `--group` 选择性执行

在 `pipeline.groups` 中定义命名的 stage 集合，用 `--group <name>`（短选项 `-g`）
或位置参数简写 `<project>+<group>` / `+<group>` 运行其中一个：

```yaml
name: myproj
pipeline:
  groups:
    all:
      stages: [init, build, test]
      description: "完整流水线：初始化、构建、测试"
    only_build: [build]     # 列表简写；description 为空
    test:       [test]
  stages:
    - name: init
      # ...
    - name: build
      # ...
    - name: test
      # ...
```

两种写法可在同一个 `groups:` 下混用。纯列表等价于 `{stages: [...]}`
（description 为空）；mapping 形式必须写 `stages`，可选单行 `description`，
仅用于展示，不影响执行逻辑。

```bash
# 运行 'all' 组内的全部 stage
skating run myproj --group all

# 只跑 'build' stage；其余 stage 记为 skipped
skating run myproj --group only_build

# -g 是 --group 的短选项（等价于上一行）
skating run myproj -g only_build

# 位置参数简写：<project>+<group>，等价于 --group
skating run myproj+only_build

# ad-hoc：跑当前目录项目（'+' 前无项目名）的 group
skating run +only_build

# 未知 group → 报错并给出 group 名单
$ skating run myproj --group nope
ERROR: --group "nope" 不存在。已知 group: [all only_build test]
```

列出 group 及其说明（项目名可选 —— 默认当前目录，且不要求项目已注册）：

```bash
$ skating groups myproj
GROUP       STAGES            DESCRIPTION
all         init, build, test 完整流水线：初始化、构建、测试
only_build  build             -
test        test              -
```

行为说明：
- 组是一组 stage 的*集合*：执行顺序始终按 `pipeline.stages` 的声明顺序，而非组内书写顺序。
- 遇首个失败 stage 即停止；其后（选中的）剩余 stage 均记为 `skipped`，整体构建仍失败。
- 组内条目只能是 stage 名 —— 单个 step 请用 `--step <stage>/<step>`。
- 组名与 stage 名处于不同命名空间，因此 `groups.test` 可指向名为 `test` 的 stage。
- `description` 按单行使用；`run` 忽略该字段，只有 `skating groups` 会展示
  （空 description 显示为 `-`）。
- 位置 `+<group>` 与 `-g`/`--group` 互斥（同时出现报错），也与 `--step` 互斥；`--env` 可与任一组合。
- 用 `+` 而非 `@` 作分隔符：行首 `@` 在 PowerShell 下被当作未定义变量的 splat 而静默丢弃，
  会退化成跑全量流水线。`+group` 在 bash/zsh/cmd/PowerShell 下都无需引号。
- 项目名本身含 `+` 暂不支持（会被当作 group 分隔符切分）。
- 加载配置时校验**所有**组：即使某个组本次未被使用，引用未知 stage 也会在运行前硬报错。

#### 列出 stage

列出 `pipeline.stages` 中声明的 stage 及其说明。与 `skating groups` 不同，
输出遵循声明顺序（也就是执行顺序）：

```bash
$ skating stages myproj
STAGE   DESCRIPTION
init    -
build   编译各平台二进制
test    运行测试
```

项目名可选 —— 不传时读取当前目录的 `.skating.yaml`，且不要求项目已注册。
`description` 仅用于展示（`run` 会忽略），空 description 显示为 `-`。

#### 用 `--env` 注入自定义环境变量

```bash
# 注入单个变量
skating run myproj --env DEPLOY_ENV=staging

# -e 是 --env 的短选项（等价于上一行）
skating run myproj -e DEPLOY_ENV=staging

# 多个值；同名变量后者覆盖前者
skating run myproj --env DEPLOY_ENV=staging --env REGION=us-west-2

# 覆盖内置 SKA_BUILD_*（如用于重放/调试）
skating run myproj --env SKA_BUILD_ID=999

# 两个 flag 组合：只跑 release step 并指定自定义目标
skating run myproj --step deploy/release --env DEPLOY_TARGET=canary
```

`--env` 的环境变量优先级最高：覆盖同名内置 `SKA_*`、stage env 与 step env，
`when` 条件与 `env` 值插值同样能看到用户值。

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

#### `skating stats`

显示正在运行的任务及实时进度（每 2s 刷新；加 `--once` 只输出一次后退出）。
`Concurrency: N/M` 只统计真正占用槽位的任务。

```bash
skating stats --once
skating stats --all      # 附带最近的历史终态记录
skating stats --prune    # 清理 stale（僵尸）记录
```

当任务的进程未正常结束（如 `kill -9`、OOM、断电、容器被杀）时心跳停止，
超过 15s 后该记录变为 **stale**（僵尸）。stale 记录：

- **不占用并发槽位**，因此不会阻塞新构建；
- 会一直显示在列表中，直到被清理，或 7 天后自动删除。

用 `--prune` 立即删除它们；`--prune` 不会影响正在运行或已结束的记录。

被中途杀掉的构建**没有日志文件**，`projects.yaml` 里的状态也不会更新 —— 日志只在
运行结束时写入。

#### `skating stop [项目] <id>`

停止本机正在运行的构建，或清理 stale（僵尸）记录。项目名可选，默认取当前目录。

```bash
skating stats --once          # 先确认 PROJECT 与 BUILD#
skating stop 102              # 项目取当前目录
skating stop training 102     # 显式指定项目
```

行为：
- **running** 且心跳新鲜 → 终止进程（Unix 先 `SIGTERM` 再 `SIGKILL`；Windows 用
  `taskkill /T /F`），记录标记为 `stopped`；
- **stale** / 进程已退出 → 删除僵尸记录；
- **已结束**（success/failure/timeout/stopped）→ 无需处理。

## 三层架构

```
┌─────────────────────────────┐
│  Pipeline DSL (YAML)        │  ← Stage / Parallel / When 编排
├─────────────────────────────┤
│  执行引擎                    │  ← Shell + Lua + SSH/SCP/Sync + Sign/Verify
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

### Stage 说明（`description`）

stage 可以带一个可选的单行 `description`，由 `skating stages` 展示：

```yaml
stages:
  - name: build
    description: "编译各平台二进制"
```

该字段仅用于展示，不影响执行、分组校验或环境变量解析。空 description 显示为 `-`。

### Stage 条件（`when`）

stage 可以带一个可选的 `when` 条件，语法与 step 级 `when` 相同。条件为假时整个
stage 被跳过：stage 内每个 step 记为 `skipped`，原因为 `stage condition not met`，
构建整体仍为成功。

```yaml
stages:
  - name: deploy
    when: false                 # 整段禁用
    steps:
      - name: release
        type: shell
        script: ./deploy.sh

  - name: publish
    when: branch == "main"      # 基于 git 上下文 / 环境变量的表达式
    steps:
      - name: push
        type: shell
        script: docker push myapp
```

- 取值：缺省或空串（始终执行）、布尔字面量 `true` / `false`（大小写不敏感），
  或使用 `==`、`!=`、`>`、`<`、`>=`、`<=` 的比较表达式。
- 条件可访问构建 env、stage env、跨 step 变量池以及 git 上下文
  （`branch`、`commit`、`git_dirty`）。
- 条件在 step 过滤之前求值，因此 `--step <stage>/<step>` 无法绕过 `when: false`。
- 使用 `--group` 时，被跳过的 stage 不计为失败，后续被选中的 stage 仍会执行。
- 表达式非法视为错误：stage 内每个 step 记为 `failed`。

step 级 `when` 同样支持布尔字面量，`when: false` 可禁用单个 step 且不使构建失败。

### Stage 级环境变量（`env`）

stage 也可以定义 `env`，由**该 stage 内所有 step 共享**（平台分组规则与 step env 相同）。同名变量优先级：构建 env（`SKA_*`）< stage env < step env < `--env`（最高）：

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

### `env` 值支持变量拼接

stage/step `env` 的值可以用 `${VAR}` 或 `$VAR` 引用其他变量并与固定文本拼接。
引用在 step 运行前由 skating 展开，取值来自已解析的环境（构建 env 如 `SKA_*`、
`--env`、跨 step 变量池、stage env），因此一个值可以由其他变量拼出来：

```yaml
env:
  default:
    version: "0.2.${SKA_BUILD_ID}"   # build #7 时为 "0.2.7"
```

展开是迭代进行的，因此值可以引用同一个 `env` 块中的其他键（支持引用链）。
未定义的变量展开为空串。注意与 `script` 的区别：`script` 里的 `${version}` 由
shell 在运行时展开，而 `env` 值里的 `${...}` 由 skating 在 step 运行前展开。

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
    port: "${SERVER_PORT}"       # 可选；默认 22；支持 ${VAR}
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
  `SKA_*` 系统变量、step env、跨 step 变量池）。`host`、`port`、`user` 与
  `workdir`（ssh）同样支持 `${VAR}`（如 `host: 10.0.0.${SEG}`）——未定义变量
  保留原样，拼错变量名会在连接时暴露原始值，而非静默连错主机。`port` 可写
  数字或字符串（`port: 22` / `port: "${SERVER_PORT}"`），非法值会在连接前报错
- SSH 脚本通过远端 `bash -x -s` 执行，每条命令都会以 `+ ...` 形式输出到实时
  日志，便于排查远端执行过程
- 构建取消/超时会自动关闭 SSH 连接

## 数字签名（Sign / Verify）

`type: sign` 为单个文件生成 detached 签名，`type: verify` 负责验签。两者均为
内置能力（无需外部 `gpg`/`openssl` 可执行文件），只依赖 `golang.org/x/crypto`
与标准库。

私钥用 `skating secret set <name> --from-file <keyfile>` 保存，yaml 中只引用
名字；密钥内容不会出现在 yaml 或日志中。

```yaml
steps:
  - name: sign-artifact
    type: sign
    secret: release-signing-key      # 必填；私钥名
    file: dist/app.tar.gz            # 必填；支持 ${VAR}
    output: dist/app.tar.gz.sig      # 可选；默认 <file>.sig
    algorithm: rsa-sha256            # 可选；缺省按私钥类型自动识别

  - name: verify-artifact
    type: verify
    file: dist/app.tar.gz            # 必填
    signature: dist/app.tar.gz.sig   # 可选；默认 <file>.sig
    public_key: keys/release.pub     # public_key 与 secret 二选一
    # secret: release-pub            # 或：公钥 PEM 存在 skating secret 中
    algorithm: rsa-sha256            # 可选；缺省按公钥类型自动识别
```

算法（未显式指定 `algorithm` 时按密钥类型自动识别）：

| 密钥类型 | 算法 | 签名编码 |
|---|---|---|
| RSA | `rsa-sha256`（PKCS#1 v1.5 + SHA-256） | raw bytes |
| ECDSA | `ecdsa-sha256`（SHA-256） | ASN.1 DER |
| Ed25519 | `ed25519` | raw bytes |

行为说明：
- 密钥格式：私钥支持 x509 PEM（PKCS#8 / PKCS#1 / SEC1）与 OpenSSH 私钥；
  公钥支持 PKIX / PKCS#1 PEM 或 OpenSSH `.pub`（authorized_keys）
- `file` / `output` / `signature` / `public_key` 相对项目目录解析（与 shell
  步骤一致），支持 `${VAR}` 展开；未定义变量保留原样
- `.sig` 为单行标准 base64（带 padding）并以 `\n` 结尾。验签时忽略首尾空白，
  同时兼容去 padding 的 base64（如 `openssl base64 -A` 的输出）
- 已存在的 `.sig` 直接覆盖；`output` 的父目录不存在时自动创建
- `verify` 的 `public_key` 与 `secret` 必须且只能填一个；验签不匹配会使
  step（及构建）失败，解析/格式错误会说明期望的密钥类型
- 不支持带口令的加密私钥；请先解密（如
  `openssl pkey -in key.pem -out plain.pem`）
- Ed25519 需要把整个文件读入内存；超大产物建议用 RSA/ECDSA（流式计算 SHA-256）
- 签名针对文件原始字节，Windows/Linux 跨平台对同一文件字节一致

用 OpenSSL 交叉验证（以 RSA 密钥为例）：
```bash
# 用公钥校验 skating 生成的 .sig
openssl dgst -sha256 -verify keys/release.pem \
  -signature dist/app.tar.gz.sig dist/app.tar.gz
```

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
| [env-vars](./examples/env-vars/) | 环境变量的来源、作用域与优先级 |

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

短选项：`-s` = `--step`、`-g` = `--group`、`-e` = `--env`（以及位置参数
`<project>+<group>` / `+<group>` 简写）用法完全一致：

```bash
skating run mixed-shell-lua-example -s shell-stage
skating run go-project -e SKA_BUILD_ID=999
skating run myproj -g only_build
skating run myproj+only_build
```

完整演练参见 [`examples/mixed-shell-lua/README.md`](./examples/mixed-shell-lua/README.md)。

## License

MIT