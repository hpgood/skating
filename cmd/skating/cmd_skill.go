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
with Shell and Lua executors, and stores all data as local YAML files (no database required).
When a docker image is configured and docker CLI is available on the host, shell steps
run inside a one-shot "docker run --rm" container (workdir mounted, env injected).
Otherwise shell steps fall back to running directly on the host shell.

## Commands

### skating init
Initialize a skating project in the current directory.
- Creates .skating.yaml with a sample build stage. The template includes step env
  platform groups (default / windows / linux) and SKA_SHELL examples
- Creates scripts/ directory with example shell/lua scripts
- Registers the project in global ~/.skating/projects.yaml
- If .skating.yaml already exists, returns an error; use --force to overwrite
- The project name is auto-derived from the current directory name
- The template sets image: "" (steps run on the host shell); set a docker image
  in .skating.yaml to build inside a container instead

### skating run [project-name]
Execute the full pipeline. The project name is optional:
- With a name: run the registered project
- Without a name (ad-hoc mode): use the .skating.yaml in the current directory;
  the project is auto-registered if not yet known
Options:
- --step <stage> or <stage>/<step>: run only the named stage or step.
  Stages/steps not selected are recorded as skipped. In --step <stage> mode a
  sequential stage continues past a failing step so the whole stage reports.
- --env KEY=VAL: inject custom env into steps. Repeatable; later values win;
  on conflict user values override skating system env (SKA_*)
Workflow:
1. Load project (registry or ad-hoc) and read .skating.yaml from the project path
2. Increment and inject SKA_BUILD_ID (plus SKA_BUILD_TIMESTAMP / SKA_BUILD_DATE)
3. Execute stages sequentially; within a stage with parallel=true, run steps concurrently
4. For each step: evaluate the When condition (if any), merge step-level env
   (platform-aware), then dispatch to the shell or lua executor
5. Stream output to the terminal line-by-line and buffer it for log persistence
6. Save log to ~/.skating/logs/<project>/<buildID>.log (plain text, ANSI stripped)
7. Update project status and timestamp in projects.yaml
8. Run all registered plugins (builtin ConsoleNotifier + user Yaegi plugins)
9. Print build summary with per-step status, icon, and duration

### skating config [project]
Display the .skating.yaml configuration for a project. The name is optional —
it defaults to the project in the current directory (name from .skating.yaml,
falling back to the directory name). The same rule applies to logs and clean.

### skating logs [project]
View build logs.
- Default: show the most recent log in full
- --id N: show log for build ID N in full
- --last N: show summary of last N builds (build ID, status only, not full log)

### skating clean [project]
Clear build log files for a project (defaults to the current directory project).
BuildID counter and project registry are preserved. --all clears logs for all projects.

### skating update
Sync name/image from the current directory .skating.yaml to the global registry:
- Project not registered yet → register it
- Same name already registered → update image and path
- Same path registered under an old name (yaml name changed) → rename the project

### skating ls
List all registered projects with their name, path, docker image, last build status, and last build time.
Status values: "success", "failure", "none" (never built).

### skating version
Print version information (commit, build date).

### skating skill
Output this SKILL document. Use --lang zh-CN for Chinese.

## Pipeline Configuration (.skating.yaml)

### Full Schema
` + "```yaml" + `
name: project-name          # Project identifier
image: docker-image:tag     # Docker image for isolated builds (optional; empty = host shell)
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
          env:                    # Step-level env, grouped by platform (optional)
            default:              # Injected on every platform
              version: "0.1.0"
            windows:              # Injected only when GOOS == windows
              Ext: ".exe"
            linux:                # Injected only when GOOS == linux
              Ext: ""
` + "```" + `

### Step Fields
- name: (required) Unique step name within the stage
- type: (required) "shell" or "lua"
- script: Inline script content. For shell: bash commands. For lua: Lua code.
- source: Path to external script file. Mutually exclusive with script (use one).
- when: Conditional expression. Step is skipped if false. Empty/absent means always run.
- env: (optional) Step-level environment variables grouped by platform. Group key
  "default" applies on every platform; other keys (windows/linux/darwin...) apply
  when the key matches the runtime GOOS. Platform groups override same-name
  variables from default.

### Stage Fields
- name: (required) Stage name
- parallel: (optional, default false) Run all steps concurrently
- steps: (required) Ordered list of Step objects

### Step Execution Rules
- Stages run sequentially; a failing stage stops the entire pipeline
- Within a serial stage, steps run in order; a failing step stops the stage
- Within a parallel stage, all steps start concurrently; first failure is reported after all complete
- With --step filtering, stages/steps not selected are recorded as skipped (visible
  in the summary); in --step <stage> mode a sequential stage continues past failing
  steps so the whole stage reports, while the overall build stays failed

## Shell Executor
- Default shell: /bin/bash on Linux/macOS, PowerShell on Windows
- SKA_SHELL env var overrides the executing shell. Format: "<shell> [args...]" —
  the first token is the shell, remaining tokens become shell flags, and the
  script path is appended automatically. Example: SKA_SHELL: "pwsh -NoLogo".
  bash/sh get "-x"; powershell gets "-ExecutionPolicy Bypass -File"
- Inline scripts are written to a temp file, executed, then the temp file is deleted
- External scripts (source) run directly from the project directory
- Environment: SKA_* system vars + step env (platform-resolved) are injected into
  the command environment
- stdout/stderr are streamed line-by-line to the live log and captured; log files
  and plugins receive plain text (ANSI colors stripped)

### PowerShell notes (Windows)
- The output stream is forced to UTF-8, and inline scripts get a UTF-8 BOM so
  non-ASCII text is decoded correctly on Windows PowerShell 5.1
- Scripts run with Set-PSDebug -Trace 1, so DEBUG: trace lines appear in the log
- Injected env vars are also exposed as PowerShell variables ($VAR) in the script
- Native command failures propagate: a trailing "exit $LASTEXITCODE" line makes
  the step fail when the last native command (go build, etc.) failed — matching
  GitHub Actions pwsh behavior. Pure cmdlet scripts and scripts with an explicit
  exit are unaffected.

### Docker execution
- Shell steps run via "docker run --rm <image> bash -x <script>" by default;
  SKA_SHELL overrides the shell inside the container using the same rules

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

### upload_artifact(path) -> (dest_path, error)
Copy a local file to ~/.skating/artifacts/<docker-image-or-host>/<basename>.
Returns the destination absolute path on success, or empty string + error message on failure.
` + "`" + `path` + "`" + ` may be an absolute path or relative to the current step's working directory.

### Lua Sandbox
- The os and io modules are disabled in the Lua VM
- Only explicitly registered functions (sh, log, set_env, upload_artifact) are available
- Attempting to call unregistered functions will result in a Lua error

## When Conditions
Condition expressions support the following operators: ==, !=, >, <, >=, <=

Variable resolution:
- $VAR_NAME: resolved from environment variables (e.g., $SKA_BUILD_ID)
- bare_identifier: resolved from env first, then auto-detected git context
  (branch, commit, git_dirty), otherwise treated as a literal string

Auto-detected git context (available to bare identifiers):
- branch:   current git branch name (git rev-parse --abbrev-ref HEAD)
- commit:   short commit hash (git rev-parse --short HEAD)
- git_dirty: "true" if working tree has uncommitted changes, "false" otherwise

If both sides can be parsed as numbers, numeric comparison is used; otherwise string comparison.

Examples:
- SKA_BUILD_ID == 5          (only on 5th build)
- SKA_BUILD_ID > 1           (after first build)
- branch == "main"           (only on main branch)
- git_dirty == "false"       (only on clean tree)
- "" (empty string)          (always run, no condition)

## Environment Variables
- SKA_BUILD_ID: Auto-injected, increments from 1 per project. Available in all steps.
- SKA_BUILD_TIMESTAMP: Auto-injected, unix seconds (UTC) of the build start time.
- SKA_BUILD_DATE: Auto-injected, RFC3339 (UTC) human-readable build date.
- SKA_SHELL: Optional, set via step env, overrides the executing shell (see Shell Executor).
- Step env: defined per step in .skating.yaml (default + platform groups).
- --env KEY=VAL (skating run): user-provided; overrides SKA_* system values on conflict.
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
- Plugin interface methods: Name(), Version(), Init() (optional), Run(ctx)

### Plugin File Format
A plugin file MUST declare "package plug" and export a variable named "Plugin" whose
type implements the plugin interface. Yaegi introspects this single symbol.
` + "```go" + `
// File: plugins/notify_slack.go
package plug

type SlackPlugin struct{}

func (p *SlackPlugin) Name() string    { return "slack" }
func (p *SlackPlugin) Version() string { return "1.0.0" }
func (p *SlackPlugin) Init() error     { return nil }

// ctx fields: ProjectName, BuildID, Status ("success"/"failure"), Duration, Output (full log)
func (p *SlackPlugin) Run(projectName string, buildID int, status string, duration string, output string) error {
    // post to webhook...
    return nil
}

// Yaegi looks up this exact symbol.
var Plugin = &SlackPlugin{}
` + "```" + `

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
4. Run: skating run (project name optional; without it the current directory is used)
5. Check results with: skating logs
`

const skillDocZhCN = `# SKATING SKILL（中文）

## 概述
Skating 是一个轻量级 CI/CD 自动化工具。使用基于 YAML 的流水线 DSL，
配合 Shell 和 Lua 执行器，所有数据以本地 YAML 文件存储（无需数据库）。
当 .skating.yaml 配置了 docker 镜像且主机上存在 docker CLI 时，shell 步骤
会在一次性 "docker run --rm" 容器内运行（挂载工作目录、注入环境变量）；
否则回退到直接在 host shell 执行。

## 命令

### skating init
在当前目录初始化一个 skating 项目。
- 创建 .skating.yaml，内含示例 build stage。模板带 step env 平台分组
  （default / windows / linux）以及 SKA_SHELL 示例
- 创建 scripts/ 目录及示例 shell/lua 脚本
- 将项目注册到全局 ~/.skating/projects.yaml
- 如果 .skating.yaml 已存在，返回错误；使用 --force 强制覆盖
- 项目名自动取当前目录名
- 模板 image 为 ""（步骤直接在 host shell 执行）；在 .skating.yaml 设置
  docker 镜像可改为容器内构建

### skating run [项目名]
执行项目的完整流水线。项目名可选：
- 传名称：运行已注册项目
- 不传（adhoc 模式）：使用当前目录的 .skating.yaml；未注册时自动注册
选项：
- --step <stage> 或 <stage>/<step>：只运行指定 stage 或 step。未选中的
  stage/step 会记为 skipped。--step <stage> 模式下，串行 stage 中某 step
  失败后仍继续跑完剩余 step，便于看到整个 stage 的完整结果
- --env KEY=VAL：向 step 注入自定义环境变量。可重复传入，后者覆盖前者；
  与系统 SKA_* 变量冲突时用户值优先
工作流：
1. 加载项目（注册表或 adhoc），从项目路径读取 .skating.yaml
2. 递增并注入 SKA_BUILD_ID（同时注入 SKA_BUILD_TIMESTAMP / SKA_BUILD_DATE）
3. 按顺序执行各 Stage；若 Stage 设置 parallel=true，则并发执行其中的 Steps
4. 对每个 Step：先评估 When 条件（如有），再合并 step 级 env（按平台解析），
   最后分发到 shell 或 lua 执行器
5. 输出按行实时流式转发到终端，同时缓冲用于日志持久化
6. 将日志保存到 ~/.skating/logs/<项目名>/<buildID>.log（纯文本，已剥离 ANSI）
7. 更新 projects.yaml 中的项目状态和时间戳
8. 运行所有已注册插件（内置 ConsoleNotifier + 用户 Yaegi 插件）
9. 打印构建摘要，包含每个 Step 的状态、图标和耗时

### skating config [项目名]
显示项目的 .skating.yaml 配置内容。项目名可选——默认取当前目录对应的项目
（读 .skating.yaml 的 name，缺省用目录名）。logs 和 clean 同规则。

### skating logs [项目名]
查看构建日志。
- 默认：显示最近一次构建的完整日志
- --id N：显示构建 ID 为 N 的完整日志
- --last N：显示最近 N 次构建的摘要（仅构建ID、状态，不含完整日志）

### skating clean [项目名]
清空项目的构建日志文件（默认当前目录对应的项目）。BuildID 计数器和项目注册信息保持不变。
--all 清空所有项目的日志。

### skating update
将当前目录 .skating.yaml 的 name/image 同步到全局注册表：
- 项目未注册 → 注册新项目
- 同名项目已注册 → 更新 image 与 path
- 同路径记录存在但名字不匹配（yaml 里改过名）→ 重命名项目

### skating ls
列出所有已注册项目的名称、路径、Docker 镜像、最近构建状态和最近构建时间。
状态值："success"（成功）、"failure"（失败）、"none"（从未构建）。

### skating version
输出版本信息（commit、构建日期）。

### skating skill
输出本文档。默认英文，使用 --lang zh-CN 切换中文。

## 流水线配置 (.skating.yaml)

### 完整 Schema
` + "```yaml" + `
name: project-name          # 项目标识
image: docker-image:tag     # Docker 镜像（可选；为空则直接在 host shell 执行）
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
          env:                    # step 级环境变量，按平台分组（可选）
            default:              # 所有平台都注入
              version: "0.1.0"
            windows:              # 仅 GOOS == windows 时注入
              Ext: ".exe"
            linux:                # 仅 GOOS == linux 时注入
              Ext: ""
` + "```" + `

### Step 字段说明
- name：（必填）Stage 内唯一的 Step 名称
- type：（必填）"shell" 或 "lua"
- script：内联脚本内容。shell 类型为 bash 命令，lua 类型为 Lua 代码
- source：外部脚本文件路径。与 script 互斥（只能使用一个）
- when：条件表达式。若为 false 则跳过该 Step。为空或未设置表示始终执行
- env：（可选）step 级环境变量，按平台分组。分组键 "default" 对所有平台生效；
  其余键（windows/linux/darwin...）仅在键名等于当前 GOOS 时生效；
  平台组覆盖 default 中同名变量

### Stage 字段说明
- name：（必填）Stage 名称
- parallel：（可选，默认 false）是否并发执行该 Stage 内的所有 Steps
- steps：（必填）Step 对象的有序列表

### 执行规则
- Stage 按顺序执行；失败的 Stage 会终止整个流水线
- 串行 Stage 中，Steps 按顺序执行；失败的 Step 会终止该 Stage
- 并行 Stage 中，所有 Steps 同时启动；所有完成后报告首个失败
- 使用 --step 过滤时，未选中的 stage/step 记为 skipped（在摘要中可见）；
  --step <stage> 模式下串行 stage 失败后继续执行剩余 step 以报告完整结果，
  但整体构建状态仍为失败

## Shell 执行器
- 默认 shell：Linux/macOS 为 /bin/bash，Windows 为 PowerShell
- 环境变量 SKA_SHELL 可覆盖执行 shell。格式 "<shell> [参数...]"——首个 token
  为 shell，其余 token 作为 shell 参数，脚本路径自动追加。
  示例：SKA_SHELL: "pwsh -NoLogo"。bash/sh 自动加 "-x"；
  powershell 自动加 "-ExecutionPolicy Bypass -File"
- 内联脚本写入临时文件，执行后删除
- 外部脚本（source）直接从项目目录运行
- 环境变量：SKA_* 系统变量 + step env（按平台解析）注入命令环境
- stdout/stderr 按行实时转发到实时日志并捕获；日志文件与插件收到的是
  纯文本（已剥离 ANSI 颜色）

### PowerShell 说明（Windows）
- 输出流强制 UTF-8；内联脚本带 UTF-8 BOM，避免 Windows PowerShell 5.1
  把脚本内中文按 ANSI 解码乱码
- 以 Set-PSDebug -Trace 1 运行，日志中会出现 DEBUG: 跟踪行
- 注入的环境变量同时以 PowerShell 变量形式暴露（$VAR）
- native 命令失败会传播：脚本末尾追加 exit $LASTEXITCODE 行，最后一条
  native 命令（go build 等）失败时该 step 判为失败——与 GitHub Actions
  pwsh 行为一致。纯 cmdlet 脚本与显式 exit 的脚本不受影响

### Docker 执行
- shell 步骤默认以 "docker run --rm <image> bash -x <script>" 执行；
  SKA_SHELL 以同样规则覆盖容器内 shell

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

### upload_artifact(路径) -> (目标路径, 错误)
将本地文件复制到 ~/.skating/artifacts/<docker-镜像或-host>/<文件名>。
成功时返回目标绝对路径，失败时返回空字符串 + 错误信息。
` + "`" + `路径` + "`" + ` 可以是绝对路径，也可以是相对当前步骤工作目录的相对路径。

### Lua 安全沙箱
- Lua 虚拟机中禁用了 os 和 io 模块
- 仅允许显式注册的函数（sh、log、set_env、upload_artifact）
- 调用未注册函数将导致 Lua 错误

## When 条件
条件表达式支持以下运算符：==、!=、>、<、>=、<=

变量解析：
- $变量名：从环境变量解析（如 $SKA_BUILD_ID）
- 裸标识符：先从 env 解析，其次从自动检测的 git 上下文解析
  （branch、commit、git_dirty），否则按字面字符串处理

自动注入的 git 上下文变量：
- branch：    当前 git 分支名（git rev-parse --abbrev-ref HEAD）
- commit：    短提交哈希（git rev-parse --short HEAD）
- git_dirty：工作区有未提交修改则为 "true"，否则为 "false"

若两边均可解析为数值，则使用数值比较；否则使用字符串比较。

示例：
- SKA_BUILD_ID == 5          （仅第 5 次构建）
- SKA_BUILD_ID > 1           （第 2 次及之后）
- branch == "main"           （仅 main 分支）
- git_dirty == "false"       （仅工作区干净时）
- ""（空字符串）               （始终执行，无条件）

## 环境变量
- SKA_BUILD_ID：自动注入，每个项目从 1 递增。所有 Step 中可用。
- SKA_BUILD_TIMESTAMP：自动注入，构建开始时刻的 unix 秒（UTC）。
- SKA_BUILD_DATE：自动注入，RFC3339（UTC）格式的构建日期。
- SKA_SHELL：可选，通过 step env 设置，覆盖执行 shell（见 Shell 执行器）。
- step env：在 .skating.yaml 中按 step 定义（default + 平台分组）。
- --env KEY=VAL（skating run）：用户传入；与 SKA_* 系统变量冲突时用户值优先。
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
- 插件接口方法：Name()、Version()、Init()（可选）、Run(ctx)

### 插件文件格式
插件文件必须声明 "package plug" 并导出一个名为 "Plugin" 的变量，其类型实现插件接口。
Yaegi 只查找这个固定符号。
` + "```go" + `
// File: plugins/notify_slack.go
package plug

type SlackPlugin struct{}

func (p *SlackPlugin) Name() string    { return "slack" }
func (p *SlackPlugin) Version() string { return "1.0.0" }
func (p *SlackPlugin) Init() error     { return nil }

// 上下文字段：ProjectName、BuildID、Status ("success"/"failure")、Duration、Output (完整日志)
func (p *SlackPlugin) Run(projectName string, buildID int, status string, duration string, output string) error {
    // 发送到 webhook...
    return nil
}

// Yaegi 查找这个固定符号
var Plugin = &SlackPlugin{}
` + "```" + `

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
4. 运行：skating run（项目名可选；不传即使用当前目录）
5. 查看结果：skating logs
`
