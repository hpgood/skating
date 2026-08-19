# Skating

一个轻量级 CI/CD 自动化构建工具 —— 媲美 Jenkins 的能力，单二进制文件的极简部署。无需数据库、无需守护进程、零配置漂移。为 vibe coding 而生。

## 特性

- **一键安装** — 单二进制文件，无需数据库，数据存储于本地 YAML 文件
- **三层架构** — Pipeline DSL 编排 + Shell/Lua 执行引擎 + Yaegi 插件扩展
- **Docker 沙箱** — 在容器内隔离构建，环境一致
- **跨平台** — Windows / Linux / macOS 全支持

## Skating vs Drone

| | Skating | Drone |
|---|---------|-------|
| **部署** | 单二进制，`go install` 即用 | 需要 Server + Runner + 数据库 |
| **数据库** | 无，本地 YAML 文件 | PostgreSQL / MySQL / SQLite |
| **配置** | 单文件 `.skating.yaml`，在项目内 | `.drone.yml` + Web UI 配置 |
| **执行层** | Shell + Lua (Gopher-Lua 沙箱) | 纯 Shell / 插件 |
| **插件** | Yaegi Go 解释器，原生性能 | Docker 容器化插件 |
| **并行** | Stage 级别原生 goroutine | 多 Runner / 多 Pipeline |
| **缓存** | 无外部依赖 | 需 Redis 或共享卷 |
| **安装成本** | < 30 秒 | 需 Docker Compose / K8s 部署 |

## 快速开始

### 安装

```bash
# 从源码编译（需要 Go 1.21+）
git clone https://github.com/hpgood/skating.git
cd skating
go build -ldflags="-X main.version=0.2.0" -o skating ./cmd/skating/

# 验证
./skating version
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
skating run myproject # 执行构建
```

## 命令

| 命令 | 说明 |
|------|------|
| `skating init` | 当前目录初始化项目，生成 `.skating.yaml`、`scripts/` |
| `skating run <项目>` | 按 Pipeline 编排执行构建 |
| `skating config <项目>` | 查看项目配置 |
| `skating logs <项目>` | 查看构建日志（支持 `--last N`、`--id N`） |
| `skating clean <项目>` | 清空构建日志（保留 BuildID 和项目注册信息） |
| `skating version` | 输出版本号（含 commit 和构建日期） |
| `skating skill` | 输出 SKILL 指南（供 AI 智能体使用） |
| `skating ls` | 列出所有已注册项目及构建状态 |

所有命令默认输出英文，可通过 `--lang zh-CN` 切换为中文。示例：`skating run myproject --lang zh-CN`

### `skating run` 选项

`run` 命令支持两个 flag 用于选择性子集 / 参数化构建：

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

## 三层架构

```
┌─────────────────────────────┐
│  Pipeline DSL (YAML)        │  ← Stage / Parallel / When 编排
├─────────────────────────────┤
│  执行引擎 (Shell + Lua)      │  ← 实际构建命令执行，Lua 安全沙箱
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

## License

MIT