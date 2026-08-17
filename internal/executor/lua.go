package executor

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// runLua 执行 Lua 脚本
func (r *Runner) runLua(step Step, workDir string, env map[string]string) (string, error) {
	L, err := r.getLuaState()
	if err != nil {
		return "", err
	}

	// 注册安全 API（每次执行刷新 env 引用）
	r.registerLuaAPI(L, env)

	// 加载并执行 Lua 代码
	var luaCode string
	if step.Script != "" {
		luaCode = step.Script
	} else if step.Source != "" {
		luaCode = fmt.Sprintf("dofile(%q)", step.Source)
	} else {
		return "", fmt.Errorf("step %q: neither script nor source specified", step.Name)
	}

	if err := L.DoString(luaCode); err != nil {
		return "", fmt.Errorf("lua execution failed: %w", err)
	}

	return "", nil
}

// getLuaState 获取或创建 Lua 虚拟机（保留复用）
func (r *Runner) getLuaState() (*lua.LState, error) {
	if r.luaState != nil {
		return r.luaState, nil
	}

	L := lua.NewState()

	// 移除危险模块
	L.SetGlobal("os", lua.LNil)
	L.SetGlobal("io", lua.LNil)

	r.luaState = L
	return L, nil
}

// CloseLua 关闭 Lua 虚拟机
func (r *Runner) CloseLua() {
	if r.luaState != nil {
		r.luaState.Close()
		r.luaState = nil
	}
}

// runShellCmd 从 Lua 的 sh() API 调用，执行 shell 命令
func runShellCmd(command string, env map[string]string) (string, error) {
	r := &Runner{}
	step := Step{
		Name:   "lua-sh",
		Type:   "shell",
		Script: command,
	}
	return r.runShell(step, "", env)
}

// registerLuaAPI 向 Lua 虚拟机注册安全的 Go API
func (r *Runner) registerLuaAPI(L *lua.LState, env map[string]string) {
	// sh(command) - 执行 shell 命令
	L.SetGlobal("sh", L.NewFunction(func(L *lua.LState) int {
		command := L.CheckString(1)

		out, err := runShellCmd(command, env)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(fmt.Sprintf("sh error: %v\n%s", err, out)))
			return 2
		}

		L.Push(lua.LString(out))
		return 1
	}))

	// log(message) - 输出日志
	L.SetGlobal("log", L.NewFunction(func(L *lua.LState) int {
		message := L.CheckString(1)
		if r.logFn != nil {
			r.logFn(message)
		}
		fmt.Print(message)
		return 0
	}))

	// set_env(key, value) - 设置环境变量
	L.SetGlobal("set_env", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		value := L.CheckString(2)
		env[key] = value
		return 0
	}))

	// upload_artifact(path) - 上传构建产物（暂未实现）
	L.SetGlobal("upload_artifact", L.NewFunction(func(L *lua.LState) int {
		fmt.Println("upload_artifact not yet supported")
		return 0
	}))
}
