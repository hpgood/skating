# Skating

一个轻量级 CI/CD 自动化构建工具 —— 媲美 Jenkins 的能力，单二进制文件的极简部署。无需数据库、无需守护进程、零配置漂移。为 vibe coding 而生。

## 特性

- **一键安装** — 单二进制文件，无需数据库，数据存储于本地 YAML 文件
- **三层架构** — Pipeline DSL 编排 + Shell/Lua 执行引擎 + Yaegi 插件扩展
- **Docker 沙箱** — 在容器内隔离构建，环境一致
- **跨平台** — Windows / Linux / macOS 全支持

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
            log("Build: " .. os.getenv("SKA_BUILD_ID"))
            sh("docker push myapp:" .. os.getenv("SKA_BUILD_ID"))
        - name: notify
          type: shell
          when: branch == "main"  # 条件执行
          script: |
            echo "生产环境部署完成"
```

## 环境变量

每次构建自动注入 `SKA_BUILD_ID`（从 1 开始递增），脚本中可直接使用：

```bash
echo "Building version ${SKA_BUILD_ID}"
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