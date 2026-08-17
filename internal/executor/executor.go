package executor

import (
	"fmt"

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
	logFn    func(string) // Lua log 回调函数
	luaState *lua.LState  // 保留复用的 Lua 虚拟机
}

// NewRunner 创建一个新的 Runner
func NewRunner() *Runner {
	return &Runner{}
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
