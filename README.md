# Skating

[中文说明](README.zh-CN.md)

A lightweight CI/CD automation tool — Jenkins-like power, single binary simplicity. No database, no daemons, no config drift. Built for vibe coding.

## Features

- **Zero-dependency deploy** — Single binary, no database, all data in local YAML files
- **Three-layer architecture** — Pipeline DSL + Shell/Lua executor + Yaegi plugin system
- **Docker sandbox** — Isolated builds inside containers for consistent environments
- **Cross-platform** — Windows / Linux / macOS

## vs Drone

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

## Quick Start

### Installation

```bash
# Build from source (requires Go 1.21+)
git clone https://github.com/hpgood/skating.git
cd skating
go build -ldflags="-X main.version=0.2.0" -o skating ./cmd/skating/

# Verify
./skating version
```
```
skating 0.2.0
  commit : abc123
  built  : 2024-01-01
```
# Or use the install scripts
# Linux/macOS:
bash scripts/install.sh

# Windows (PowerShell):
.\scripts\install.ps1
```

### Initialize a Project

```bash
cd /path/to/your/project
skating init          # Generates .skating.yaml template
skating run myproject # Run the build
```

## Commands

| Command | Description |
|---------|-------------|
| `skating init` | Initialize a project with `.skating.yaml` and `scripts/` |
| `skating run <project>` | Execute the pipeline build |
| `skating config <project>` | View project configuration |
| `skating logs <project>` | View build logs (`--last N`, `--id N`) |
| `skating clean <project>` | Clear build logs (keeps BuildID and registry) |
| `skating version` | Print version (commit, build date) |
| `skating skill` | Output SKILL guide for AI agents |
| `skating ls` | List all registered projects and their status |

All commands support `--lang zh-CN` for Chinese output.

### `skating run` options

The `run` command supports two flags for selective / parameterized builds:

| Flag | Format | Description |
|---|---|---|
| `--step`  | `<stage>` or `<stage>/<step>` | Run only the named stage (or single step). Other stages / same-stage siblings are reported as `skipped` in the summary but never execute. |
| `--env`   | `KEY=VAL` (repeatable) | Inject a custom environment variable into every step of this run. User values **override** the built-in `SKA_BUILD_*` variables. |

#### Selective execution with `--step`

```bash
# Run only the 'build' stage (sequential steps inside run end-to-end, even on failure)
skating run myproj --step build

# Run only one step inside a stage; other stages & sibling steps are skipped
skating run myproj --step build/compile

# Unknown stage → clear error listing known stages
$ skating run myproj --step nonexistent
ERROR: stage "nonexistent" in --step "nonexistent" not found. Known stages: [build test deploy]
```

Behavior notes:
- Stages not selected are reported with `○ skipped` in the build summary — you always see what was *not* run.
- Within a filtered stage (`--step build`), sequential steps continue past failures so you get a full picture of the stage.
- Step-level filters (`--step build/compile`) preserve the original "stop on first failure" semantic.
- Multi-stage failure ordering: if the selected stage fails, subsequent stages are still marked `skipped` (not silently dropped) so the summary is complete.

#### Custom env vars with `--env`

```bash
# Inject a single var
skating run myproj --env DEPLOY_ENV=staging

# Multiple values; later wins on duplicates
skating run myproj --env DEPLOY_ENV=staging --env REGION=us-west-2

# Override a built-in SKA_BUILD_* var (e.g. for replay/debug)
skating run myproj --env SKA_BUILD_ID=999

# Both flags together: deploy only the 'release' step with a custom target
skating run myproj --step deploy/release --env DEPLOY_TARGET=canary
```

Malformed `--env` values (missing `=`, empty KEY) produce a clear error before any step runs.

## Three-Layer Architecture

```
┌─────────────────────────────┐
│  Pipeline DSL (YAML)        │  ← Stage / Parallel / When orchestration
├─────────────────────────────┤
│  Executor (Shell + Lua)     │  ← Command execution with Lua sandbox
├─────────────────────────────┤
│  Plugins (Yaegi)            │  ← Go plugins: notifications, reviewers, etc.
└─────────────────────────────┘
```

### Pipeline Configuration

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
      parallel: true            # Run steps concurrently
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
          type: lua             # Lua script execution
          script: |
            log("Build: " .. getenv("SKA_BUILD_ID"))
            sh("docker push myapp:" .. getenv("SKA_BUILD_ID"))
        - name: notify
          type: shell
          when: branch == "main"  # Conditional execution
          script: |
            echo "Deployed to production"
```

## Environment Variables

Every build automatically injects these environment variables (UTC, derived from build start time):

| Variable | Type | Example | Description |
|---|---|---|---|
| `SKA_BUILD_ID`        | integer (string) | `1`           | Auto-incrementing build number (from 1) |
| `SKA_BUILD_TIMESTAMP` | integer (string) | `1787139682`  | Build start time as **Unix seconds (UTC)** — easy to sort / diff / arithmetic |
| `SKA_BUILD_DATE`      | string           | `2026-08-19T11:41:22Z` | Build start time as **RFC3339 (UTC)** — human-readable, for reports/logs |

Shell scripts:
```bash
echo "Building version ${SKA_BUILD_ID} at ${SKA_BUILD_DATE}"
echo "Local time: $(date -d @${SKA_BUILD_TIMESTAMP})"
```

Lua scripts (sandbox has no `os` module — use `getenv`):
```lua
log("Build: " .. getenv("SKA_BUILD_ID"))
sh("docker push myapp:" .. getenv("SKA_BUILD_ID"))
local ts = getenv("SKA_BUILD_TIMESTAMP")
log("Unix seconds: " .. ts)
```

## Data Storage

All data is stored under `~/.skating/`:

```
~/.skating/
├── projects.yaml              # Project registry
└── logs/<project>/<ID>.log    # Build logs
```

## Examples

See the [examples/](./examples/) directory:

| Example | Description |
|---------|-------------|
| [go-project](./examples/go-project/) | Go CI: build → test → lint |
| [node-project](./examples/node-project/) | Node.js CI: install → build → test |
| [mixed-shell-lua](./examples/mixed-shell-lua/) | Shell + Lua hybrid pipeline |
| [parallel-stages](./examples/parallel-stages/) | Parallel stage execution |
| [conditional-build](./examples/conditional-build/) | When condition examples |

## License

MIT