package executor

import (
	"fmt"
	"os"
	"os/exec"

	lua "github.com/yuin/gopher-lua"
)

// Step 代表一个构建步骤
type Step struct {
	Name   string
	Type   string // "shell" 或 "lua"
	Script string // 内联脚本内容
	Source string // 外部脚本文件路径
}

// Executor 定义执行器接口
type Executor interface {
	Execute(step Step, workDir string, env map[string]string) (string, error)
}

// Runner 是 Executor 的具体实现
type Runner struct {
	logFn           func(string) // Lua log 回调函数
	luaState        *lua.LState  // 保留复用的 Lua 虚拟机
	dockerImage     string       // .skating.yaml 的 image 字段，空则不启用 Docker
	dockerAvailable bool         // host 是否检测到 docker CLI
}

// NewRunner 创建一个新的 Runner，并检测 Docker 可用性
func NewRunner() *Runner {
	r := &Runner{}
	r.dockerAvailable = detectDocker()
	return r
}

// NewRunnerWithImage 创建一个配置了 Docker 镜像的 Runner
func NewRunnerWithImage(image string) *Runner {
	r := &Runner{
		dockerImage:     image,
		dockerAvailable: detectDocker(),
	}
	return r
}

// SetDockerImage 在 Runner 已创建后设置 image 字段
func (r *Runner) SetDockerImage(image string) {
	r.dockerImage = image
	r.dockerAvailable = detectDocker()
}

// IsDockerAvailable 返回 host 是否检测到 docker CLI
func (r *Runner) IsDockerAvailable() bool {
	return r.dockerAvailable
}

// DockerImage 返回当前配置的 docker 镜像（空字符串表示未启用）
func (r *Runner) DockerImage() string {
	return r.dockerImage
}

// Execute 根据 step.Type 分发到对应的执行器
func (r *Runner) Execute(step Step, workDir string, env map[string]string) (string, error) {
	switch step.Type {
	case "shell":
		return r.runShell(step, workDir, env)
	case "lua":
		return r.runLua(step, workDir, env)
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