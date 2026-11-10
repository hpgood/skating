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
with Shell, Lua, SSH and file-transfer (scp/sync) executors, and stores all data
as local YAML files (no database required).
When a shell step configures a docker image (step-level "image") and docker CLI is
available on the host, that step runs inside a one-shot "docker run --rm" container
(workdir mounted, env injected). Otherwise the step falls back to running directly
on the host shell. Different steps may use different images. SSH steps run bash
scripts on a remote server over a native SSH connection. scp/sync steps transfer
files between local and remote over SFTP (scp semantics / rsync-like sync).

## Commands

### skating init
Initialize a skating project in the current directory.
- Creates .skating.yaml with a sample build stage. The template includes step env
  platform groups (default / windows / linux) and SKA_SHELL examples
- Creates scripts/ directory with example shell/lua scripts
- Registers the project in global ~/.skating/projects.yaml
- If .skating.yaml already exists, returns an error; use --force to overwrite
- The project name is auto-derived from the current directory name
- Steps run on the host shell by default; set a step-level docker image
  in .skating.yaml to run that step inside a container instead

### skating run [project-name[+group]]
Execute the full pipeline. The positional argument is optional:
- With a name: run the registered project
- Without a name (ad-hoc mode): use the .skating.yaml in the current directory;
  the project is auto-registered if not yet known
- <project>+<group>: run only that group of stages in the named project
- +<group> (ad-hoc): run only that group using the .skating.yaml in the current
  directory; the part before '+' is the project name (empty = ad-hoc)
Options:
- --step <stage> or <stage>/<step> (short: -s): run only the named stage or step.
  Stages/steps not selected are recorded as skipped. In --step <stage> mode a
  sequential stage continues past a failing step so the whole stage reports.
- --group <name> (short: -g): run only the stages in the named group defined
  under pipeline.groups. Mutually exclusive with --step. The positional forms
  <project>+<group> and +<group> (ad-hoc current directory) are equivalent to
  --group; they are mutually exclusive with -g/--group and with --step. The
  separator is '+' because a leading '@' is silently swallowed by PowerShell.
  Project names containing '+' are not supported.
- --env KEY=VAL (short: -e): inject custom env into steps. Repeatable; later
  values win; on conflict user values have the highest priority and override
  SKA_* system vars plus same-name stage/step env
- --timeout <dur>: override the run timeout for this invocation. Accepts a Go
  duration ("10m"/"600s") or plain seconds ("600"). Takes precedence over the
  top-level timeout field in .skating.yaml; when unset, the config value is used,
  falling back to 10m. On timeout, running subprocesses are terminated and the
  build is reported as timeout.
Workflow:
1. Load project (registry or ad-hoc) and read .skating.yaml from the project path
2. Increment and inject SKA_BUILD_ID (plus SKA_BUILD_TIMESTAMP / SKA_BUILD_DATE,
   SKA_GIT_COMMIT_ID, SKA_OS); SKA_GIT_BRANCH is refreshed before each step runs
3. Execute stages sequentially; within a stage with parallel=true, run steps concurrently
4. For each stage: evaluate the stage-level When condition (if any) after stage env
   is merged; false skips the whole stage (all steps recorded as skipped, build
   still succeeds) before any step filter or goroutine starts
5. For each step: evaluate the When condition (if any), merge stage-level env
   then step-level env (both platform-aware; step overrides stage on conflicts),
   then dispatch to the shell, lua, ssh, scp or sync executor
6. Stream output to the terminal line-by-line and buffer it for log persistence
7. Save log to ~/.skating/logs/<project>/<buildID>.log (plain text, ANSI stripped)
8. Update project status and timestamp in projects.yaml
9. Run all registered plugins (builtin ConsoleNotifier + user Yaegi plugins)
10. Print build summary with per-step status, icon, and duration

### skating config [project]
Display the .skating.yaml configuration for a project. The name is optional —
it defaults to the project in the current directory (name from .skating.yaml,
falling back to the directory name). The same rule applies to logs and clean.

### skating groups [project]
List the groups defined under pipeline.groups with their stages and descriptions.
- The project name is optional: without it the .skating.yaml in the current
  directory is used, and the project does not need to be registered
- Read-only: loads and validates the config, then prints a GROUP / STAGES /
  DESCRIPTION table sorted by group name
- An empty description is shown as "-"; stage lists are joined with ", "

### skating stages [project]
List the stages declared under pipeline.stages with their descriptions.
- The project name is optional: without it the .skating.yaml in the current
  directory is used, and the project does not need to be registered
- Read-only: loads and validates the config, then prints a STAGE / DESCRIPTION
  table in declaration order (which is also the execution order)
- An empty description is shown as "-"; description is display-only and is
  ignored by run

### skating logs [project]
View build logs.
- Default: show the most recent log in full
- --id N: show log for build ID N in full
- --last N: show summary of last N builds (build ID, status only, not full log)

### skating clean [project]
Clear build log files for a project (defaults to the current directory project).
BuildID counter and project registry are preserved. --all clears logs for all projects.

### skating stats
Show in-progress tasks with live progress. Keeps refreshing every 2s; add --once to
render a single snapshot and exit.
- --all: also list recent finished records
- --prune: delete "stale" (zombie) records. A record becomes stale when its
  heartbeat stops for >15s, which happens when a skating process dies without
  finishing (kill -9, OOM, power loss, killed container). Stale records do NOT
  occupy a concurrency slot — they are excluded from the max_tasks gate, so new
  builds are not blocked — but they keep appearing in the list until pruned or
  auto-removed (7 days). The "Concurrency: N/M" headline counts only live running
  tasks, not stale ones.

### skating stop [project-name] <build-id>
Stop a running build on this host, or clean a stale (zombie) record whose process is
already gone. Project name is optional (defaults to the current directory).
- running with a fresh heartbeat -> terminate the process (SIGTERM then SIGKILL on
  Unix; taskkill /T /F on Windows) and mark the record stopped
- heartbeat timed out (stale / process exited) -> delete the zombie record
- already finished (success/failure/timeout/stopped) -> nothing to do
Use skating stats to find the PROJECT and BUILD# first.

### skating secret set/list/delete
Manage global secrets (ssh passwords / private keys) stored in
~/.skating/secrets.json (file mode 0600). Secrets are referenced by name from
pipeline yaml — the yaml never contains the secret value itself.
- set <name>: store a value. Sources (mutually exclusive): --value <v>,
  --from-file <path>, or omit both for a hidden terminal prompt (no echo,
  nothing in shell history). For private keys use --from-file.
- list: show names and timestamps only (never values)
- delete <name>: remove a secret

### skating update
Sync the project name from the current directory .skating.yaml to the global registry:
- Project not registered yet → register it
- Same name already registered → update path
- Same path registered under an old name (yaml name changed) → rename the project

### skating ls
List all registered projects with their name, path, last build status, and last build time.
Status values: "success", "failure", "none" (never built).

### skating version
Print version information (commit, build date).

### skating skill
Output this SKILL document. Use --lang zh-CN for Chinese.

## Pipeline Configuration (.skating.yaml)

### Full Schema
` + "```yaml" + `
name: project-name          # Project identifier
timeout: 10m                # Run timeout (Go duration or seconds; default 10m,
                            # overridable with skating run --timeout)
pipeline:
  groups:                   # Named stage sets for skating run --group/-g <name>
    all:                    # Mapping form: stages (required) + description (optional)
      stages: [init, build, test]
      description: "Full pipeline"
    only_build: [build]     # List shorthand still works (description is empty)
  stages:
    - name: stage-name
      description: "Compile all platform binaries"  # Display-only (skating stages)
      when: "branch == main"  # Optional stage condition; false skips the stage
      parallel: false       # If true, run all steps in this stage concurrently
      env:                  # Stage-level env, shared by all steps in the stage
        default:            # (optional; same grouping rules as step env:
          version: "0.2.${SKA_BUILD_ID}"  # ${VAR} interpolated; --env wins over all)
        windows:
          Ext: ".exe"
      steps:
        - name: step-name
          type: shell|lua|ssh|scp|sync  # Executor type
          image: docker-image:tag  # Optional step-level docker image (shell steps
                                   # only); empty = run on host shell. Steps may
                                   # use different images
          docker_options:          # Extra args appended to "docker run" (optional)
            - --network host
          workdir: /work           # Container-side working directory (optional,
                                   # default /work; only for shell steps with image)
          host: 10.0.0.1           # SSH/SCP/Sync: remote host (required; supports ${VAR})
          port: 22                 # SSH/SCP/Sync: remote port (optional, default 22)
          user: deploy             # SSH/SCP/Sync: login user (required; supports ${VAR})
          auth: password           # SSH/SCP/Sync: password|key (optional, default password)
          secret: deploy-pass      # SSH/SCP/Sync: global secret NAME (skating secret
                                   # set <name>); the value is resolved at runtime
          direction: push          # SCP only: push (local->remote, default) | pull
          local: dist/             # SCP/Sync only: local path (supports ${VAR})
          remote: /opt/app/        # SCP/Sync only: remote path (supports ${VAR})
          delete: true             # Sync only: remove remote files missing locally
                                   # (optional, default true)
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
- type: (required) "shell", "lua", "ssh", "scp" or "sync"
- script: Inline script content. For shell: bash commands. For lua: Lua code.
  For ssh: bash commands run on the remote server.
- source: Path to external script file. Mutually exclusive with script (use one).
- image: (optional, shell steps only) Step-level docker image. When set and the
  docker CLI is available, the step runs inside a one-shot container; each step
  may use a different image. Empty = run on host shell.
- docker_options: (optional, shell steps only) Extra args appended to "docker run",
  e.g. ["--network host", "-v /tmp/cache:/var/cache"]. Placed after the built-in
  flags (name, workdir mount, env) and before the image name, so duplicates of
  built-in flags are overridden by these values.
- workdir: (optional) For shell steps with image: container-side working directory
  where the project directory is mounted; default "/work". For ssh steps: remote
  cd directory (no default, supports ${VAR} expansion). A leading "/" is added
  automatically for docker (e.g. "cache" becomes "/cache").
- when: Conditional expression or boolean literal (true/false, case-insensitive).
  Step is skipped if false. Empty/absent means always run.
- env: (optional) Step-level environment variables grouped by platform. Group key
  "default" applies on every platform; other keys (windows/linux/darwin...) apply
  when the key matches the runtime GOOS. Platform groups override same-name
  variables from default.
- host: (ssh/scp/sync steps only, required) Remote host (IP or domain). Supports
  ${VAR} expansion (e.g. "10.0.0.${SEG}"); undefined variables are kept as-is.
- port: (ssh/scp/sync steps only) Remote SSH port; default 22.
- user: (ssh/scp/sync steps only, required) Remote login user. Supports ${VAR}
  expansion; undefined variables are kept as-is.
- auth: (ssh/scp/sync steps only) "password" or "key"; default "password". With
  "key" the secret holds a PEM private key.
- secret: (ssh/scp/sync steps only, required) NAME of a global secret created
  with "skating secret set <name>". The actual password/key is resolved from
  ~/.skating/secrets.json at runtime — it never appears in the yaml or in logs.
- direction: (scp steps only) "push" (local -> remote, default) or "pull"
  (remote -> local).
- local: (scp/sync steps only, required) Local path. ${VAR} variables are
  expanded from the step env view (SKA_* system vars, step env, cross-step
  pool). For sync it must be a directory.
- remote: (scp/sync steps only, required) Remote path; also ${VAR}-expanded.
- delete: (sync steps only) Mirror deletion — remove remote files/dirs that no
  longer exist locally. Default true; set false to only add/update remote files.

### Stage Fields
- name: (required) Stage name
- description: (optional) Single-line description shown by "skating stages";
  display-only — it never affects execution, group validation, or env resolution
- when: (optional) Stage-level condition using the same syntax as step when:
  boolean literal true/false or a comparison expression. When false the whole
  stage is skipped (every step recorded as skipped, reason "stage condition not
  met") and the build still succeeds. Evaluated after stage env is merged and
  before step filtering, so --step cannot bypass it. An invalid expression fails
  every step of the stage and the build.
- parallel: (optional, default false) Run all steps concurrently
- steps: (required) Ordered list of Step objects
- env: (optional) Stage-level environment variables grouped by platform, shared
  by every step in the stage (scripts and When conditions both see them).
  Same grouping rules as step env. Priority: build env (SKA_*) < stage env <
  step env < --env (highest) — --env overrides same-name values at every level.

### Step Execution Rules
- Stages run sequentially; a failing stage stops the entire pipeline
- A stage when condition is evaluated before any step runs: false skips the whole
  stage (steps recorded as skipped) without failing the build; an invalid
  expression fails every step of the stage
- Within a serial stage, steps run in order; a failing step stops the stage
- Within a parallel stage, all steps start concurrently; first failure is reported after all complete
- With --step filtering, stages/steps not selected are recorded as skipped (visible
  in the summary); in --step <stage> mode a sequential stage continues past failing
  steps so the whole stage reports, while the overall build stays failed
- With --group filtering, only the selected stages run in declaration order; the
  first failing stage stops the group and every remaining selected stage is recorded
  as skipped, with the overall build still failed. The positional <project>+<group>
  / +<group> shorthand behaves identically. Unknown stages in any group are
  rejected at config-load time

## Cross-Step Variables
Steps can pass variables to subsequent steps via a build-level variable pool.
Variables in the pool are visible to every later step's script environment and
When condition (pool values override same-name step env keys).

Two ways to write into the pool:
- Shell and SSH steps: echo a marker line "::set-var KEY=VALUE" to
  stdout. The line is captured and does NOT appear in the log or step output.
- Lua steps: call set_env(key, value) — same effect, also visible to later steps.

--env still has the highest priority and is not overridden by pool variables;
otherwise pool variables override same-name keys in the per-step env view.

Examples:
` + "```yaml" + `
steps:
  - name: producer
    type: shell
    script: |
      echo "::set-var VERSION=v1.2.3"
  - name: consumer
    type: shell
    when: "VERSION == v1.2.3"
    script: |
      echo "building ${VERSION}"
` + "```" + `
Lua equivalent: ` + "```lua" + `set_env("VERSION", "v1.2.3")` + "```" + `

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
- Applies only to shell steps that set a step-level "image" (and docker CLI available):
  "docker run --rm <image> bash -x <script>". SKA_SHELL overrides the shell inside
  the container using the same rules
- docker_options entries are appended after the built-in flags and before the image
  name; a later duplicate flag wins (docker semantics)
- The project directory is mounted at the container working directory (default
  "/work", configurable per step via "workdir"; a leading "/" is added automatically)

## SSH Executor
SSH steps (type: ssh) connect to a remote server with a native SSH client
(golang.org/x/crypto/ssh) and run the script with remote bash — no local ssh.exe
or sshpass needed.

Example:
` + "```yaml" + `
steps:
  - name: deploy
    type: ssh
    host: 10.0.0.1
    port: 22
    user: deploy
    auth: password
    secret: deploy-pass
    workdir: /opt/app
    script: |
      ./deploy.sh ${SKA_GIT_BRANCH}
` + "```" + `

Behavior:
- Authentication: "password" (default) or "key" (secret holds a PEM private key,
  stored via skating secret set <name> --from-file <keyfile>)
- Secrets: the yaml stores only the secret NAME; the value lives in
  ~/.skating/secrets.json (mode 0600) and is resolved at runtime. Secret values
  are never logged
- Host key check: accept-all (InsecureIgnoreHostKey) — suitable for internal
  servers; the dial timeout is 30s
- The script is fed to remote "bash -s" via stdin (no command-line length or
  quoting limits). Step env (SKA_* + step env + cross-step pool) is injected as
  export lines before the script
- workdir: the remote runs "cd <workdir>" before the script (supports ${VAR}
  expansion, e.g. "/opt/${APP}")
- host/user support ${VAR} expansion from the step env view (SKA_* + step env +
  cross-step pool); undefined variables are kept as-is, so a typo surfaces at
  dial time instead of silently connecting to a wrong host
- stdout/stderr stream line-by-line to the live log (magenta) with ::set-var
  capture, same as shell steps; a non-zero remote exit code fails the step

## SCP / Sync Executor
scp (type: scp) copies a file or directory between local and remote over SFTP
(pkg/sftp) — no scp binary or OpenSSH installation needed. sync (type: sync)
mirrors a local directory to a remote directory like a simplified rsync:
unchanged files (same size + mtime, second precision) are skipped, and remote
files that no longer exist locally are removed. Both reuse the SSH connection
fields (host/port/user/auth/secret; host/user support ${VAR}) and support
${VAR} expansion in local and remote paths.

Examples:
` + "```yaml" + `
steps:
  - name: upload-artifact
    type: scp                       # push (local -> remote), the default
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass
    local: dist/app-${SKA_GIT_BRANCH}.tar.gz
    remote: /opt/app/app.tar.gz

  - name: fetch-logs
    type: scp
    direction: pull                 # pull: remote -> local
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass
    local: logs/
    remote: /var/log/myapp

  - name: sync-dist
    type: sync                      # incremental mirror upload with deletion
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass
    local: dist
    remote: /opt/app/dist
    delete: true                    # default; set false to keep extra remote files
` + "```" + `

Behavior:
- scp: single file or recursive directory; remote parent directories are
  created automatically and mtimes are preserved (so a follow-up sync skips
  unchanged files). The log gets a summary line
  "[scp] push local ↔ remote: N file(s), SIZE"
- scp pull: the remote path may be a file or a directory (downloaded
  recursively)
- sync: local must be a directory. On first deploy the remote directory may
  not exist yet (treated as empty). Files are compared by size + mtime
  (second precision); changed files are uploaded with mtime preserved. After
  upload, remote entries missing locally are removed when delete is true
  (the default)
- sync writes a summary line
  "[sync] local → remote: uploaded N, skipped N, removed N (SIZE)"
- Authentication, secret resolution, host key policy, 30s dial timeout and
  build-context cancellation behave exactly as in SSH steps

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
Copy a local file to ~/.skating/artifacts/<step-image-or-host>/<basename>.
Returns the destination absolute path on success, or empty string + error message on failure.
` + "`" + `path` + "`" + ` may be an absolute path or relative to the current step's working directory.

### Lua Sandbox
- The os and io modules are disabled in the Lua VM
- Only explicitly registered functions (sh, log, set_env, upload_artifact) are available
- Attempting to call unregistered functions will result in a Lua error

## When Conditions
Condition expressions support the following operators: ==, !=, >, <, >=, <=
Boolean literals are also accepted (stage-level and step-level when):
- true  (or TRUE / True): always run
- false (or FALSE / False): skip the step, or the whole stage (every step
  recorded as skipped with reason "stage condition not met"; the build still
  succeeds)

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
- SKA_GIT_COMMIT_ID: Auto-injected, current git commit id (full hash from
  git rev-parse HEAD) of the project; empty when the project has no .git.
- SKA_GIT_BRANCH: Auto-injected, current git branch name (git rev-parse
  --abbrev-ref HEAD), refreshed right before each step runs; not set when
  the project has no .git.
- SKA_OS: Auto-injected, runtime operating system (runtime.GOOS):
  linux / windows / darwin / ...
- SKA_SHELL: Optional, set via step env, overrides the executing shell (see Shell Executor).
- Step env: defined per step in .skating.yaml (default + platform groups).
- --env KEY=VAL (skating run): user-provided; highest priority, overrides SKA_*
  system values and same-name stage/step env on conflict.
- User-defined via set_env() in Lua scripts (current step + later steps, see
  Cross-Step Variables).
- env value interpolation: values in stage/step env may reference other variables
  with ${VAR} or $VAR and concatenate them with literal text (e.g.
  version: "0.2.${SKA_BUILD_ID}"). References are resolved by skating before the
  step runs, against build env / --env / variable pool / stage env; chains within
  the same env block are followed; undefined variables expand to an empty string.

## Data Storage
All data under ~/.skating/:

~/.skating/projects.yaml     Schema:
` + "```yaml" + `
projects:
  - name: myproject
    path: /absolute/path/to/project
    buildid: 5
    laststatus: success
    lastbuild: "2024-01-15T10:30:00Z"
    createdat: "2024-01-01T08:00:00Z"
` + "```" + `

~/.skating/logs/<project>/<N>.log   Plain text build logs, one file per build ID.

~/.skating/secrets.json             Global secrets (ssh passwords / private keys),
                                    file mode 0600. Managed by skating secret set/list/delete.

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
- env-vars:         Demonstrates env var sources, scopes, and precedence

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
配合 Shell、Lua、SSH 与文件传输（scp/sync）执行器，所有数据以本地 YAML
文件存储（无需数据库）。
当 shell 步骤配置了 step 级 docker 镜像（"image"）且主机上存在 docker CLI 时，
该步骤会在一次性 "docker run --rm" 容器内运行（挂载工作目录、注入环境变量）；
否则直接在 host shell 执行。不同步骤可以使用不同镜像。SSH 步骤通过原生 SSH
连接在远程服务器上执行 bash 脚本。scp/sync 步骤通过 SFTP 在本地与远端之间
传输文件（scp 语义 / rsync 式同步）。

## 命令

### skating init
在当前目录初始化一个 skating 项目。
- 创建 .skating.yaml，内含示例 build stage。模板带 step env 平台分组
  （default / windows / linux）以及 SKA_SHELL 示例
- 创建 scripts/ 目录及示例 shell/lua 脚本
- 将项目注册到全局 ~/.skating/projects.yaml
- 如果 .skating.yaml 已存在，返回错误；使用 --force 强制覆盖
- 项目名自动取当前目录名
- 步骤默认在 host shell 执行；在 .skating.yaml 中为 step 设置 docker 镜像
  可改为容器内执行

### skating run [项目名[+group]]
执行项目的完整流水线。位置参数可选：
- 传名称：运行已注册项目
- 不传（adhoc 模式）：使用当前目录的 .skating.yaml；未注册时自动注册
- <project>+<group>：只运行指定项目中的该 group 内的 stage
- +<group>（adhoc）：用当前目录的 .skating.yaml 只运行该 group；'+' 之前为
  项目名（为空即 adhoc）
选项：
- --step <stage> 或 <stage>/<step>（短选项 -s）：只运行指定 stage 或 step。未选中的
  stage/step 会记为 skipped。--step <stage> 模式下，串行 stage 中某 step
  失败后仍继续跑完剩余 step，便于看到整个 stage 的完整结果
- --group <name>（短选项 -g）：只运行 pipeline.groups 中定义的指定 group 内的
  stage。与 --step 互斥。位置参数形式 <project>+<group> 与 +<group>（ad-hoc
  当前目录）等价于 --group；它们与 -g/--group 以及 --step 均互斥。分隔符用
  '+' 而非 '@'（行首 '@' 会被 PowerShell 静默丢弃）。项目名本身含 '+' 不支持。
- --env KEY=VAL（短选项 -e）：向 step 注入自定义环境变量。可重复传入，后者覆盖前者；
  用户值优先级最高，覆盖系统 SKA_* 以及同名 stage/step env
- --timeout <dur>：覆盖本次运行的超时时间。接受 Go duration（"10m"/"600s"）或
  纯秒数（"600"）。优先级高于 .skating.yaml 顶层的 timeout 字段；未指定时使用
  配置值，缺省 10m。超时后正在运行的子进程会被终止，构建状态记为 timeout。
工作流：
1. 加载项目（注册表或 adhoc），从项目路径读取 .skating.yaml
2. 递增并注入 SKA_BUILD_ID（同时注入 SKA_BUILD_TIMESTAMP / SKA_BUILD_DATE / SKA_GIT_COMMIT_ID / SKA_OS）；
   SKA_GIT_BRANCH 在每个 step 运行前刷新注入
3. 按顺序执行各 Stage；若 Stage 设置 parallel=true，则并发执行其中的 Steps
4. 对每个 Stage：先合并 stage 级 env，再评估 stage 级 When 条件（如有）；为 false
   时整段跳过（所有 Step 记为 skipped，构建仍为成功），且不会启动任何 Step 过滤或
   goroutine
5. 对每个 Step：评估 When 条件（如有），再合并 step 级 env（按平台解析；
   同名变量 step 覆盖 stage），最后分发到 shell、lua、ssh、scp 或 sync 执行器
6. 输出按行实时流式转发到终端，同时缓冲用于日志持久化
7. 将日志保存到 ~/.skating/logs/<项目名>/<buildID>.log（纯文本，已剥离 ANSI）
8. 更新 projects.yaml 中的项目状态和时间戳
9. 运行所有已注册插件（内置 ConsoleNotifier + 用户 Yaegi 插件）
10. 打印构建摘要，包含每个 Step 的状态、图标和耗时

### skating config [项目名]
显示项目的 .skating.yaml 配置内容。项目名可选——默认取当前目录对应的项目
（读 .skating.yaml 的 name，缺省用目录名）。logs 和 clean 同规则。

### skating groups [项目名]
列出 pipeline.groups 中定义的 group、包含的 stage 及说明。
- 项目名可选：不传时读取当前目录的 .skating.yaml，且不要求项目已注册
- 只读命令：加载并校验配置后输出 GROUP / STAGES / DESCRIPTION 表格，
  group 名按字母序排序
- 空 description 显示为 "-"；stage 列表用 ", " 连接

### skating stages [项目名]
列出 pipeline.stages 中声明的 stage 及其说明。
- 项目名可选：不传时读取当前目录的 .skating.yaml，且不要求项目已注册
- 只读命令：加载并校验配置后输出 STAGE / DESCRIPTION 表格，顺序为声明顺序
  （也就是执行顺序）
- 空 description 显示为 "-"；description 仅用于展示，run 会忽略

### skating logs [项目名]
查看构建日志。
- 默认：显示最近一次构建的完整日志
- --id N：显示构建 ID 为 N 的完整日志
- --last N：显示最近 N 次构建的摘要（仅构建ID、状态，不含完整日志）

### skating clean [项目名]
清空项目的构建日志文件（默认当前目录对应的项目）。BuildID 计数器和项目注册信息保持不变。
--all 清空所有项目的日志。

### skating stats
显示正在运行的任务及实时进度。默认每 2s 刷新；加 --once 只输出一次后退出。
- --all：附带最近的历史终态记录
- --prune：清理 stale（僵尸）记录。当任务的进程未正常结束（kill -9、OOM、
  断电、容器被杀）时心跳停止，超过 15s 后该记录变为 stale。stale 记录不占用
  并发槽位（不参与 max_tasks 闸门，新构建不会被阻塞），但会一直显示在列表中，
  直到被清理或 7 天后自动删除。Concurrency: N/M 只统计真正运行的 running 任务，
  不含 stale。

### skating stop [项目名] <build-id>
停止本机正在运行的构建，或清理进程已退出的 stale（僵尸）记录。项目名可选
（默认取当前目录）。
- running 且心跳新鲜 → 终止进程（Unix 先 SIGTERM 再 SIGKILL；Windows 用
  taskkill /T /F），记录标记为 stopped
- 心跳超时（stale / 进程已退出）→ 直接删除僵尸记录
- 已结束（success/failure/timeout/stopped）→ 无需处理
先用 skating stats 查看 PROJECT 与 BUILD#。

### skating secret set/list/delete
管理全局 secret（SSH 密码/私钥），存储在 ~/.skating/secrets.json（文件权限
0600）。流水线 yaml 通过名字引用 secret——yaml 中永远不出现 secret 的值本身。
- set <name>：存储一个值。来源（互斥）：--value <v>、--from-file <path>，
  或两者都省略以使用隐藏式终端输入（不回显、不进 shell history）。
  私钥请用 --from-file
- list：仅显示名字和时间戳（从不显示值）
- delete <name>：删除一个 secret

### skating update
将当前目录 .skating.yaml 的 name 同步到全局注册表：
- 项目未注册 → 注册新项目
- 同名项目已注册 → 更新 path
- 同路径记录存在但名字不匹配（yaml 里改过名）→ 重命名项目

### skating ls
列出所有已注册项目的名称、路径、最近构建状态和最近构建时间。
状态值："success"（成功）、"failure"（失败）、"none"（从未构建）。

### skating version
输出版本信息（commit、构建日期）。

### skating skill
输出本文档。默认英文，使用 --lang zh-CN 切换中文。

## 流水线配置 (.skating.yaml)

### 完整 Schema
` + "```yaml" + `
name: project-name          # 项目标识
timeout: 10m                # 运行超时（Go duration 或秒数；默认 10m，
                            # 可用 skating run --timeout 覆盖）
pipeline:
  groups:                   # 命名 stage 集合，供 skating run --group/-g <name> 使用
    all:                    # mapping 形式：stages 必填，description 可选
      stages: [init, build, test]
      description: "完整流水线"
    only_build: [build]     # 仍支持列表简写（description 为空）
  stages:
    - name: stage-name
      description: "编译各平台二进制"  # 仅用于展示（skating stages）
      when: "branch == main"  # 可选 stage 条件；false 时整段跳过
      parallel: false       # 若为 true，该 Stage 内的 Steps 并发执行
      env:                  # stage 级环境变量，该 Stage 内所有 Steps 共享
        default:            # （可选；分组规则与 step env 相同：
          version: "0.2.${SKA_BUILD_ID}"  # ${VAR} 会展开；同名 step env 覆盖 stage）
        windows:
          Ext: ".exe"
      steps:
        - name: step-name
          type: shell|lua|ssh|scp|sync  # 执行器类型
          image: docker-image:tag  # 可选的 step 级 docker 镜像（仅对 shell 步骤
                                   # 生效）；为空则在 host shell 执行。不同步骤
                                   # 可使用不同镜像
          docker_options:          # 追加到 "docker run" 的额外参数（可选）
            - --network host
          workdir: /work           # 容器内工作目录（可选，默认 /work；
                                   # 仅对配置了 image 的 shell 步骤生效）
          host: 10.0.0.1           # SSH/SCP/Sync：远端主机（必填；支持 ${VAR}）
          port: 22                 # SSH/SCP/Sync：远端端口（可选，默认 22）
          user: deploy             # SSH/SCP/Sync：登录用户（必填；支持 ${VAR}）
          auth: password           # SSH/SCP/Sync：password|key（可选，默认 password）
          secret: deploy-pass      # SSH/SCP/Sync：全局 secret 名（skating secret
                                   # set <name> 创建）；运行时 resolve 其值
          direction: push          # 仅 SCP：push（本地→远端，默认）| pull
          local: dist/             # 仅 SCP/Sync：本地路径（支持 ${VAR}）
          remote: /opt/app/        # 仅 SCP/Sync：远端路径（支持 ${VAR}）
          delete: true             # 仅 Sync：删除本地已不存在的远端文件
                                   # （可选，默认 true）
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
- type：（必填）"shell"、"lua"、"ssh"、"scp" 或 "sync"
- script：内联脚本内容。shell 类型为 bash 命令，lua 类型为 Lua 代码，
  ssh 类型为在远端服务器上运行的 bash 命令
- source：外部脚本文件路径。与 script 互斥（只能使用一个）
- image：（可选，仅 shell 步骤）step 级 docker 镜像。设置后且 docker CLI 可用
  时，该步骤在一次性容器内执行；每个步骤可使用不同镜像。为空则在 host shell 执行
- docker_options：（可选，仅 shell 步骤）追加到 "docker run" 的额外参数，
  如 ["--network host", "-v /tmp/cache:/var/cache"]。位于内置参数（容器名、
  workdir 挂载、env）之后、镜像名之前，与内置 flag 重复时以这些值为准
- workdir：（可选）配置了 image 的 shell 步骤：项目目录挂载到的容器内工作
  目录，默认 "/work"；SSH 步骤：远端 cd 目录（无默认值，支持 ${VAR} 展开）。
  docker 场景不以 / 开头时自动补前缀（如 "cache" 等价 "/cache"）
- when：条件表达式或布尔字面量（true/false，大小写不敏感）。若为 false 则跳过
  该 Step。为空或未设置表示始终执行
- env：（可选）step 级环境变量，按平台分组。分组键 "default" 对所有平台生效；
  其余键（windows/linux/darwin...）仅在键名等于当前 GOOS 时生效；
  平台组覆盖 default 中同名变量
- host：（仅 ssh/scp/sync 步骤，必填）远端主机（IP 或域名）。支持 ${VAR} 展开
  （如 "10.0.0.${SEG}"）；未定义变量保留原样
- port：（仅 ssh/scp/sync 步骤）远端 SSH 端口；默认 22
- user：（仅 ssh/scp/sync 步骤，必填）登录用户。支持 ${VAR} 展开；
  未定义变量保留原样
- auth：（仅 ssh/scp/sync 步骤）"password" 或 "key"；默认 "password"。为 "key" 时
  secret 存 PEM 私钥
- secret：（仅 ssh/scp/sync 步骤，必填）全局 secret 名，用 "skating secret set
  <name>" 创建。真实密码/私钥运行时从 ~/.skating/secrets.json resolve
  ——yaml 和日志中永远不会出现
- direction：（仅 scp 步骤）"push"（本地→远端，默认）或 "pull"（远端→本地）
- local：（仅 scp/sync 步骤，必填）本地路径。支持 ${VAR} 变量展开（取自 step
  env 视图：SKA_* 系统变量、step env、跨 step 变量池）。sync 要求必须是目录
- remote：（仅 scp/sync 步骤，必填）远端路径；同样支持 ${VAR} 展开
- delete：（仅 sync 步骤）镜像删除——移除本地已不存在的远端文件/目录。
  默认 true；设为 false 则只新增/更新远端文件

### Stage 字段说明
- name：（必填）Stage 名称
- description：（可选）单行说明，由 "skating stages" 展示；仅用于展示，
  不影响执行、分组校验或环境变量解析
- when：（可选）stage 级条件，语法与 step 的 when 相同：布尔字面量 true/false
  或比较表达式。为 false 时整段跳过（每个 Step 记为 skipped，原因为
  "stage condition not met"），构建仍为成功。在 stage env 合并之后、step 过滤
  之前求值，因此 --step 无法绕过。表达式非法时该 Stage 所有 Step 记为 failed
  且构建失败
- parallel：（可选，默认 false）是否并发执行该 Stage 内的所有 Steps
- steps：（必填）Step 对象的有序列表
- env：（可选）stage 级环境变量，按平台分组，该 Stage 内所有 Steps 共享
  （脚本的 ${VAR} 与 When 条件均可见）。分组规则与 step env 相同。
  优先级：构建 env（SKA_*）< stage env < step env < --env（最高）
  ——同名变量最终以 --env 为准

### 执行规则
- Stage 按顺序执行；失败的 Stage 会终止整个流水线
- stage 级 when 条件在任何 Step 运行前求值：为 false 时整段跳过（Steps 记为
  skipped）且不使构建失败；表达式非法时该 Stage 所有 Step 记为 failed
- 串行 Stage 中，Steps 按顺序执行；失败的 Step 会终止该 Stage
- 并行 Stage 中，所有 Steps 同时启动；所有完成后报告首个失败
- 使用 --step 过滤时，未选中的 stage/step 记为 skipped（在摘要中可见）；
  --step <stage> 模式下串行 stage 失败后继续执行剩余 step 以报告完整结果，
  但整体构建状态仍为失败
- 使用 --group 过滤时，仅按声明顺序运行被选中的 stage；首个失败 stage 即停止
  该组，其后剩余的被选中 stage 记为 skipped，整体构建状态仍为失败。位置参数
  <project>+<group> / +<group> 简写行为完全一致。任何组中出现未知 stage 都会在
  配置加载阶段直接报错

## 跨 Step 变量
Step 可以通过构建级变量池向后续 Step 传递变量。池中的变量对之后每个 Step
的脚本环境与 When 条件可见（池变量覆盖同名的 step env 键）。

两种写入方式：
- shell 与 SSH 步骤：向 stdout 输出一行标记 "::set-var KEY=VALUE"。
  该行会被捕获，不会出现在日志与 step 返回输出中
- lua 步骤：调用 set_env(键, 值)——同样写入变量池，后续 Step 可见

--env 仍保持最高优先级，不会被池变量覆盖；除此之外，池变量在每 Step 的
env 视图中覆盖同名键。

示例：
` + "```yaml" + `
steps:
  - name: producer
    type: shell
    script: |
      echo "::set-var VERSION=v1.2.3"
  - name: consumer
    type: shell
    when: "VERSION == v1.2.3"
    script: |
      echo "building ${VERSION}"
` + "```" + `
Lua 等价写法：` + "```lua" + `set_env("VERSION", "v1.2.3")` + "```" + `

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
- 仅对配置了 step 级 "image" 的 shell 步骤生效（且 docker CLI 可用）：
  "docker run --rm <image> bash -x <script>"。SKA_SHELL 以同样规则覆盖容器内 shell
- docker_options 追加在内置参数之后、镜像名之前；与内置 flag 重复时后者覆盖
  （docker 语义）
- 项目目录挂载到容器内工作目录（默认 "/work"，可通过 step.workdir 配置；
  不以 / 开头时自动补前缀）

## SSH 执行器
SSH 步骤（type: ssh）使用原生 SSH 客户端（golang.org/x/crypto/ssh）连接远程
服务器，用远端 bash 运行脚本——无需本地 ssh.exe 或 sshpass。

示例：
` + "```yaml" + `
steps:
  - name: deploy
    type: ssh
    host: 10.0.0.1
    port: 22
    user: deploy
    auth: password
    secret: deploy-pass
    workdir: /opt/app
    script: |
      ./deploy.sh ${SKA_GIT_BRANCH}
` + "```" + `

行为：
- 认证："password"（默认）或 "key"（secret 存 PEM 私钥，用
  skating secret set <name> --from-file <keyfile> 存入）
- Secret：yaml 只存 secret 名字；值保存在 ~/.skating/secrets.json
  （0600 权限），运行时 resolve。secret 值永不写入日志
- Host key 校验：accept-all（InsecureIgnoreHostKey）——适合内网服务器；
  连接超时 30s
- 脚本经 stdin 喂给远端 "bash -s"（无命令行长度/引号转义限制）。
  step env（SKA_* + step env + 跨 step 变量池）以 export 行注入到脚本之前
- workdir：远端在脚本执行前先 "cd <workdir>"（支持 ${VAR} 展开，如 "/opt/${APP}"）
- host/user 支持从 step env 视角（SKA_* + step env + 跨 step 变量池）做 ${VAR}
  展开；未定义变量保留原样，拼错变量名会在连接时暴露原始值，而非静默连错主机
- stdout/stderr 按行实时转发到实时日志（品红色）并捕获 ::set-var，
  与 shell 步骤一致；远端非零退出码判定 step 失败

## SCP / Sync 执行器
scp（type: scp）通过 SFTP（pkg/sftp）在本地与远端之间复制文件或目录——无需
本地安装 scp 或 OpenSSH。sync（type: sync）把本地目录镜像到远端目录（简化版
rsync）：未变化文件（size + mtime 秒级一致）自动跳过，本地已不存在的远端文件
会被删除。两者复用 SSH 连接字段（host/port/user/auth/secret；host/user 支持
${VAR}），local/remote 均支持 ${VAR} 变量展开。

示例：
` + "```yaml" + `
steps:
  - name: upload-artifact
    type: scp                       # push（本地→远端），默认方向
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass
    local: dist/app-${SKA_GIT_BRANCH}.tar.gz
    remote: /opt/app/app.tar.gz

  - name: fetch-logs
    type: scp
    direction: pull                 # pull：远端→本地
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass
    local: logs/
    remote: /var/log/myapp

  - name: sync-dist
    type: sync                      # 增量镜像上传 + 删除多余文件
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass
    local: dist
    remote: /opt/app/dist
    delete: true                    # 默认；设为 false 保留远端多余文件
` + "```" + `

行为：
- scp：支持单文件或目录（目录递归）；自动创建远端父目录，并保留 mtime
  （便于后续 sync 增量跳过）。日志输出摘要行
  "[scp] push local ↔ remote: N file(s), SIZE"
- scp pull：远端路径可以是文件或目录（目录递归下载）
- sync：local 必须是目录。首次部署时远端目录可以不存在（视为空树）。
  文件按 size + mtime（秒级精度）比较，变化的文件上传并保留 mtime。
  上传完成后，若 delete 为 true（默认），删除远端存在但本地不存在的条目
- sync 输出摘要行
  "[sync] local → remote: uploaded N, skipped N, removed N (SIZE)"
- 认证、secret 解析、host key 策略、30s 连接超时与构建上下文取消
  与 SSH 步骤完全一致

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
设置环境变量。当前 step 内立即生效（sh()/getenv 可见），同时写入构建级
变量池，后续 Step（含 When 条件）也能读到。
` + "```lua" + `
set_env("CUSTOM_VAR", "value")
` + "```" + `

### upload_artifact(路径) -> (目标路径, 错误)
将本地文件复制到 ~/.skating/artifacts/<步骤镜像或-host>/<文件名>。
成功时返回目标绝对路径，失败时返回空字符串 + 错误信息。
` + "`" + `路径` + "`" + ` 可以是绝对路径，也可以是相对当前步骤工作目录的相对路径。

### Lua 安全沙箱
- Lua 虚拟机中禁用了 os 和 io 模块
- 仅允许显式注册的函数（sh、log、set_env、upload_artifact）
- 调用未注册函数将导致 Lua 错误

## When 条件
条件表达式支持以下运算符：==、!=、>、<、>=、<=
stage 级与 step 级 when 均支持布尔字面量：
- true（或 TRUE、True）：始终执行
- false（或 FALSE、False）：跳过该 Step；stage 级则为整段跳过（每个 Step 记为
  skipped，原因为 "stage condition not met"），构建仍为成功

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
- SKA_GIT_COMMIT_ID：自动注入，项目当前 git commit id（git rev-parse HEAD 的完整 hash）；
  项目无 .git 时为空。
- SKA_GIT_BRANCH：自动注入，当前 git 分支名（git rev-parse --abbrev-ref HEAD），
  每个 step 运行前刷新；项目无 .git 时不注入。
- SKA_OS：自动注入，运行时操作系统类型（runtime.GOOS）：
  linux / windows / darwin 等。
- SKA_SHELL：可选，通过 step env 设置，覆盖执行 shell（见 Shell 执行器）。
- step env：在 .skating.yaml 中按 step 定义（default + 平台分组）。
- --env KEY=VAL（skating run）：用户传入；优先级最高，覆盖 SKA_* 系统变量
  以及同名 stage/step env。
- 通过 Lua 的 set_env() 可自定义变量（当前 step + 后续 step 生效，见跨 Step 变量）。
- env 值拼接：stage/step env 的值可用 ${VAR} 或 $VAR 引用其他变量并与固定文本
  拼接（如 version: "0.2.${SKA_BUILD_ID}"）。引用在 step 运行前由 skating 展开，
  取值来自构建 env / --env / 变量池 / stage env；同一 env 块内的引用链会被跟随；
  未定义的变量展开为空串。

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

~/.skating/secrets.json      全局 secret（SSH 密码/私钥），文件权限 0600，
                             由 skating secret set/list/delete 管理。

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
- env-vars：        展示环境变量的来源、作用域与优先级

## AI 智能体快速开始
为用户的工程创建 CI 流水线：
1. 进入项目目录
2. 运行：skating init
3. 编辑 .skating.yaml 定义适合项目的 stages/steps
4. 运行：skating run（项目名可选；不传即使用当前目录）
5. 查看结果：skating logs
`
