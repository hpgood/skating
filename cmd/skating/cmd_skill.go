package main

import (
	"fmt"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/spf13/cobra"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: i18n.T("输出 SKILL 使用说明（供 AI 智能体使用）", "Output SKILL guide for AI agents"),
	Long:  i18n.T("输出结构化的 SKILL 文档，便于 AI 智能体集成。通过 --lang zh-CN 可切换中文。", "Output structured SKILL documentation for AI agent integration. Use --lang zh-CN for Chinese."),
	Run:   runSkill,
}

func runSkill(cmd *cobra.Command, args []string) {
	if i18n.IsZhCN() {
		fmt.Print(skillDocZhCN)
	} else {
		fmt.Print(skillDocEn)
	}
}

const skillDocEn = `# SKATING SKILL

## Overview
Skating is a lightweight CI/CD automation tool. It uses a YAML-based pipeline DSL
with Shell and Lua executors, runs builds in Docker containers, and stores all data
as local YAML files (no database required).

## Commands

### skating init
Initialize a skating project in the current directory.
- Creates .skating.yaml (pipeline config template)
- Creates scripts/ directory with example shell/lua scripts
- Registers the project in global ~/.skating/projects.yaml
- If .skating.yaml already exists, shows a message; use --force to overwrite
- The project name is auto-derived from the current directory name
- Image defaults to "golang:1.21"

### skating config <project>
Display the .skating.yaml configuration for a registered project.

### skating ls
List all registered projects with their name, path, docker image, last build status, and last build time.
Status values: "success", "failure", "none" (never built).

### skating run <project>
Execute the full pipeline for a project.
Workflow:
1. Load project from ~/.skating/projects.yaml
2. Read .skating.yaml from project path
3. Increment and inject SKA_BUILD_ID environment variable
4. Parse pipeline stages/steps from .skating.yaml
5. Execute stages sequentially; within a stage with parallel=true, run steps concurrently
6. For each step: evaluate When condition (if any), then dispatch to shell or lua executor
7. Stream output to terminal and buffer for log persistence
8. Save log to ~/.skating/logs/<project>/<buildID>.log
9. Update project status and timestamp in projects.yaml
10. Run all registered plugins (builtin ConsoleNotifier + user Yaegi plugins)
11. Print build summary with per-step status, icon, and duration

### skating logs <project>
View build logs.
- Default: show the most recent log in full
- --id N: show log for build ID N in full
- --last N: show summary of last N builds (build ID, status only, not full log)

### skating clean <project>
Clear build log files for a project. BuildID counter and project registry are preserved.
--all flag clears logs for all projects.

### skating skill
Output this SKILL document. Use --lang zh-CN for Chinese.

## Pipeline Configuration (.skating.yaml)

### Full Schema
` + "```yaml" + `
name: project-name          # Project identifier
image: docker-image:tag     # Docker image for isolated builds (optional)
pipeline:
  stages:
    - name: stage-name
      parallel: false       # If true, run all steps in this stage concurrently
      steps:
        - name: step-name
          type: shell|lua   # Executor type
          script: |         # Inline script content
            echo "hello"
          source: path/script.sh  # External script path (alternative to script)
          when: "expression"      # Condition to evaluate (see When Conditions)
` + "```" + `

### Step Fields
- name: (required) Unique step name within the stage
- type: (required) "shell" or "lua"
- script: Inline script content. For shell: bash commands. For lua: Lua code.
- source: Path to external script file. Mutually exclusive with script (use one).
- when: Conditional expression. Step is skipped if false. Empty/absent means always run.

### Stage Fields
- name: (required) Stage name
- parallel: (optional, default false) Run all steps concurrently
- steps: (required) Ordered list of Step objects

### Step Execution Rules
- Stages run sequentially; a failing stage stops the entire pipeline
- Within a serial stage, steps run in order; a failing step stops the stage
- Within a parallel stage, all steps start concurrently; first failure is reported after all complete

## Shell Executor
- On Linux/macOS: scripts run via /bin/bash
- On Windows: scripts run via PowerShell
- Inline scripts are written to a temp file, executed, then the temp file is deleted
- External scripts (source) run directly from the project directory
- All environment variables from env map are injected into the command
- stdout and stderr are captured and returned

## Lua Executor (Gopher-Lua)
Runs Lua scripts in-process using the gopher-lua VM. The following APIs are exposed to Lua:

### sh(command) -> (output, error)
Execute a shell command. Returns stdout on success, or nil + error message on failure.
` + "```lua" + `
local output, err = sh("go build ./...")
if err then error("build failed: " .. err) end
` + "```" + `

### log(message)
Write a message to the build log output.
` + "```lua" + `
log("Starting build step...")
` + "```" + `

### set_env(key, value)
Set an environment variable in the build context.
` + "```lua" + `
set_env("CUSTOM_VAR", "value")
` + "```" + `

### upload_artifact(path) [NOT YET IMPLEMENTED]
Placeholder for artifact upload functionality.

### Lua Sandbox
- The os and io modules are disabled in the Lua VM
- Only explicitly registered functions (sh, log, set_env, upload_artifact) are available
- Attempting to call unregistered functions will result in a Lua error

## When Conditions
Condition expressions support the following operators: ==, !=, >, <, >=, <=

Variable resolution:
- $VAR_NAME: resolved from environment variables (e.g., $SKA_BUILD_ID)
- bare_identifier: resolved from context variables (e.g., branch)

If both sides can be parsed as numbers, numeric comparison is used; otherwise string comparison.

Examples:
- SKA_BUILD_ID == 5          (only on 5th build)
- SKA_BUILD_ID > 1           (after first build)
- branch == "main"           (only on main branch)
- "" (empty string)          (always run, no condition)

## Environment Variables
- SKA_BUILD_ID: Auto-injected, increments from 1 per project. Available in all steps.
- User-defined via set_env() in Lua scripts.

## Data Storage
All data under ~/.skating/:

~/.skating/projects.yaml     Schema:
` + "```yaml" + `
projects:
  - name: myproject
    path: /absolute/path/to/project
    image: golang:1.21
    buildid: 5
    laststatus: success
    lastbuild: "2024-01-15T10:30:00Z"
    createdat: "2024-01-01T08:00:00Z"
` + "```" + `

~/.skating/logs/<project>/<N>.log   Plain text build logs, one file per build ID.

## Plugins (Yaegi)
- Builtin ConsoleNotifier prints build result summary after each build
- User plugins: Go files in plugins/ directory, loaded at startup via Yaegi interpreter
- Plugin interface: Name(), Version(), Init(), Run(ctx) methods

## Examples
See examples/ directory for complete project templates:
- go-project:       Go CI pipeline (build, test, lint)
- node-project:     Node.js CI (install, build, test)
- mixed-shell-lua:  Demonstrates both shell and lua step types
- parallel-stages:  Demonstrates parallel step execution
- conditional-build: Demonstrates When conditions

## Quick Start for Agents
To create a CI pipeline for a user's project:
1. cd into the project directory
2. Run: skating init
3. Edit .skating.yaml to define stages/steps appropriate for the project
4. Run: skating run <project-name>
5. Check results with: skating logs <project-name>
`

const skillDocZhCN = `# SKATING SKILL（中文）

## 概述
Skating 是一个轻量级 CI/CD 自动化工具。使用基于 YAML 的流水线 DSL，
配合 Shell 和 Lua 执行器，在 Docker 容器中运行构建，所有数据以本地 YAML 文件存储（无需数据库）。

## 命令

### skating init
在当前目录初始化一个 skating 项目。
- 创建 .skating.yaml（流水线配置模板）
- 创建 scripts/ 目录及示例 shell/lua 脚本
- 将项目注册到全局 ~/.skating/projects.yaml
- 如果 .skating.yaml 已存在，显示提示信息；使用 --force 强制覆盖
- 项目名自动取当前目录名
- 默认镜像为 "golang:1.21"

### skating config <项目名>
显示已注册项目的 .skating.yaml 配置内容。

### skating ls
列出所有已注册项目的名称、路径、Docker 镜像、最近构建状态和最近构建时间。
状态值："success"（成功）、"failure"（失败）、"none"（从未构建）。

### skating run <项目名>
执行项目的完整流水线。
工作流：
1. 从 ~/.skating/projects.yaml 加载项目信息
2. 从项目路径读取 .skating.yaml
3. 递增并注入 SKA_BUILD_ID 环境变量
4. 解析 .skating.yaml 中的 stages/steps
5. 按顺序执行各 Stage；若 Stage 设置 parallel=true，则并发执行其中的 Steps
6. 对每个 Step：先评估 When 条件（如有），然后分发到 shell 或 lua 执行器
7. 输出流式输出到终端，同时缓冲用于日志持久化
8. 将日志保存到 ~/.skating/logs/<项目名>/<buildID>.log
9. 更新 projects.yaml 中的项目状态和时间戳
10. 运行所有已注册插件（内置 ConsoleNotifier + 用户 Yaegi 插件）
11. 打印构建摘要，包含每个 Step 的状态、图标和耗时

### skating logs <项目名>
查看构建日志。
- 默认：显示最近一次构建的完整日志
- --id N：显示构建 ID 为 N 的完整日志
- --last N：显示最近 N 次构建的摘要（仅构建ID、状态，不含完整日志）

### skating clean <项目名>
清空项目的构建日志文件。BuildID 计数器和项目注册信息保持不变。
--all 标志清空所有项目的日志。

### skating skill
输出本文档。默认英文，使用 --lang zh-CN 切换中文。

## 流水线配置 (.skating.yaml)

### 完整 Schema
` + "```yaml" + `
name: project-name          # 项目标识
image: docker-image:tag     # Docker 镜像（可选）
pipeline:
  stages:
    - name: stage-name
      parallel: false       # 若为 true，该 Stage 内的 Steps 并发执行
      steps:
        - name: step-name
          type: shell|lua   # 执行器类型
          script: |         # 内联脚本内容
            echo "hello"
          source: path/script.sh  # 外部脚本路径（与 script 二选一）
          when: "表达式"          # 条件表达式（见 When 条件）
` + "```" + `

### Step 字段说明
- name：（必填）Stage 内唯一的 Step 名称
- type：（必填）"shell" 或 "lua"
- script：内联脚本内容。shell 类型为 bash 命令，lua 类型为 Lua 代码
- source：外部脚本文件路径。与 script 互斥（只能使用一个）
- when：条件表达式。若为 false 则跳过该 Step。为空或未设置表示始终执行

### Stage 字段说明
- name：（必填）Stage 名称
- parallel：（可选，默认 false）是否并发执行该 Stage 内的所有 Steps
- steps：（必填）Step 对象的有序列表

### 执行规则
- Stage 按顺序执行；失败的 Stage 会终止整个流水线
- 串行 Stage 中，Steps 按顺序执行；失败的 Step 会终止该 Stage
- 并行 Stage 中，所有 Steps 同时启动；所有完成后报告首个失败

## Shell 执行器
- Linux/macOS：通过 /bin/bash 执行脚本
- Windows：通过 PowerShell 执行脚本
- 内联脚本写入临时文件，执行后删除
- 外部脚本（source）直接从项目目录运行
- 所有环境变量注入到命令中
- stdout 和 stderr 均被捕获返回

## Lua 执行器 (Gopher-Lua)
使用 gopher-lua 虚拟机在进程内运行 Lua 脚本。向 Lua 暴露以下 API：

### sh(命令) -> (输出, 错误)
执行 Shell 命令。成功返回 stdout，失败返回 nil + 错误信息。
` + "```lua" + `
local output, err = sh("go build ./...")
if err then error("构建失败: " .. err) end
` + "```" + `

### log(消息)
向构建日志输出一条消息。
` + "```lua" + `
log("开始构建步骤...")
` + "```" + `

### set_env(键, 值)
在构建上下文中设置环境变量。
` + "```lua" + `
set_env("CUSTOM_VAR", "value")
` + "```" + `

### upload_artifact(路径) [尚未实现]
上传构建产物的占位 API。

### Lua 安全沙箱
- Lua 虚拟机中禁用了 os 和 io 模块
- 仅允许显式注册的函数（sh、log、set_env、upload_artifact）
- 调用未注册函数将导致 Lua 错误

## When 条件
条件表达式支持以下运算符：==、!=、>、<、>=、<=

变量解析：
- $变量名：从环境变量解析（如 $SKA_BUILD_ID）
- 裸标识符：从上下文变量解析（如 branch）

若两边均可解析为数值，则使用数值比较；否则使用字符串比较。

示例：
- SKA_BUILD_ID == 5          （仅第 5 次构建）
- SKA_BUILD_ID > 1           （第 2 次及之后）
- branch == "main"           （仅 main 分支）
- ""（空字符串）               （始终执行，无条件）

## 环境变量
- SKA_BUILD_ID：自动注入，每个项目从 1 递增。所有 Step 中可用。
- 通过 Lua 的 set_env() 可自定义变量。

## 数据存储
所有数据存储在 ~/.skating/ 下：

~/.skating/projects.yaml     Schema：
` + "```yaml" + `
projects:
  - name: myproject
    path: /absolute/path/to/project
    image: golang:1.21
    buildid: 5
    laststatus: success
    lastbuild: "2024-01-15T10:30:00Z"
    createdat: "2024-01-01T08:00:00Z"
` + "```" + `

~/.skating/logs/<项目名>/<N>.log   纯文本构建日志，每个构建 ID 一个文件。

## 插件系统 (Yaegi)
- 内置 ConsoleNotifier 在每次构建后打印结果摘要
- 用户插件：放在 plugins/ 目录下的 Go 文件，启动时通过 Yaegi 解释器加载
- 插件接口：Name()、Version()、Init()、Run(ctx) 方法

## 示例
参见 examples/ 目录的完整项目模板：
- go-project：       Go CI 流水线（build、test、lint）
- node-project：     Node.js CI（install、build、test）
- mixed-shell-lua：  展示 shell 和 lua 混合使用
- parallel-stages：  展示并行 Step 执行
- conditional-build：展示 When 条件使用

## AI 智能体快速开始
为用户的工程创建 CI 流水线：
1. 进入项目目录
2. 运行：skating init
3. 编辑 .skating.yaml 定义适合项目的 stages/steps
4. 运行：skating run <项目名>
5. 查看结果：skating logs <项目名>
`