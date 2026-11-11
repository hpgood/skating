package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	lua "github.com/yuin/gopher-lua"
)

// Step 代表一个构建步骤
type Step struct {
	Name   string // "shell"、"lua"、"ssh"、"scp" 或 "sync"
	Type   string // "shell"、"lua"、"ssh"、"scp" 或 "sync"
	Script string // 内联脚本内容
	Source string // 外部脚本文件路径

	// Image 本步骤在 Docker 容器中执行时使用的镜像（仅对 shell 步骤生效）；
	// 为空时在 host shell 直接执行。不同步骤可使用不同镜像。
	Image string
	// DockerOptions 追加到 docker run 的额外参数（如 --network host、-v ...）
	DockerOptions []string
	// WorkDir 工作目录：shell 容器内挂载点（默认 /work）；ssh 步骤为远端 cd 目录
	WorkDir string

	// 以下为 SSH 远程执行配置（仅 type: ssh 生效）
	Host string // 远端主机（IP 或域名），必填
	// Port SSH 端口，同时支持整数与字符串
	// （字符串支持 ${VAR} 展开）；留空或 0 表示 22
	Port any
	User string // 登录用户，必填
	// Auth 认证方式："password" 或 "key"，空为 "password"
	Auth string
	// Secret 认证材料：密码明文或私钥 PEM。由上层在运行时 resolve
	// （来自全局 secret 存储），不落 yaml、不进日志
	Secret string

	// 以下为文件传输配置（仅 type: scp / sync 生效）
	// Direction 传输方向（仅 scp）："push"（本地→远端，默认）或 "pull"（远端→本地）
	Direction string
	// Local 本地路径（scp 支持文件或目录；sync 必须是目录）
	Local string
	// Remote 远端路径（scp 支持文件或目录；sync 必须是目录）
	Remote string
	// Delete 镜像删除（仅 sync）：远端存在但本地不存在的文件/目录是否删除。
	// 缺省 true（nil 视为 true），传 false 保留远端多余文件
	Delete *bool
}

// Executor 定义执行器接口
type Executor interface {
	Execute(step Step, workDir string, env map[string]string) (string, error)
}

// Runner 是 Executor 的具体实现
type Runner struct {
	luaState        *lua.LState             // 保留复用的 Lua 虚拟机
	dockerAvailable bool                    // host 是否检测到 docker CLI
	ctx             context.Context         // 取消后终止正在运行的 shell/docker 子进程（超时控制），零值时为 context.Background()
	onSetVar        func(key, value string) // ::set-var 标记行 / Lua set_env 的跨 step 变量回调；nil 时忽略
}

// NewRunner 创建一个新的 Runner，并检测 Docker 可用性
func NewRunner() *Runner {
	r := &Runner{ctx: context.Background()}
	r.dockerAvailable = detectDocker()
	return r
}

// WithContext 注入取消上下文：ctx 被 cancel（如超时）后，
// 正在执行的 shell/docker 子进程会被终止。须在 Execute 之前调用。
func (r *Runner) WithContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.ctx = ctx
}

// IsDockerAvailable 返回 host 是否检测到 docker CLI
func (r *Runner) IsDockerAvailable() bool {
	return r.dockerAvailable
}

// OnSetVar 注册跨 step 变量回调：脚本输出 "::set-var KEY=VALUE" 行或
// Lua 调用 set_env 时触发。回调由上层实现（写入构建级共享变量池），
// 须保证并发安全（并行 stage 下多个 step 可能同时触发）。
// 未注册回调时 ::set-var 协议行同样被捕获丢弃（不污染日志），
// 仅变量不写入变量池；Lua set_env 仅当前 step 内生效。
func (r *Runner) OnSetVar(fn func(key, value string)) {
	r.onSetVar = fn
}

// Execute 根据 step.Type 分发到对应的执行器
func (r *Runner) Execute(step Step, workDir string, env map[string]string, logFn func(msg string)) (string, error) {
	switch step.Type {
	case "shell":
		return r.runShell(step, workDir, env, logFn)
	case "lua":
		return r.runLua(step, workDir, env, logFn)
	case "ssh":
		return r.runSsh(step, env, logFn)
	case "scp":
		return r.runScp(step, env, logFn)
	case "sync":
		return r.runSync(step, env, logFn)
	default:
		return "", fmt.Errorf("unknown step type: %q", step.Type)
	}
}

// detectDocker 检测 host 是否可执行 docker 命令
// 测试可通过 SKATING_DISABLE_DOCKER=1 环境变量禁用，便于 host shell fallback 测试
func detectDocker() bool {
	if os.Getenv("SKATING_DISABLE_DOCKER") == "1" {
		return false
	}
	_, err := exec.LookPath("docker")
	return err == nil
}
