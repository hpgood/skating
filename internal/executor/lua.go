package executor

import (
	"fmt"
	"strings"
	"sync"

	lua "github.com/yuin/gopher-lua"
)

// runLua 执行 Lua 脚本
func (r *Runner) runLua(step Step, workDir string, env map[string]string) (string, error) {
	L, err := r.getLuaState()
	if err != nil {
		return "", err
	}

	// 注册安全 API（每次执行刷新 env 引用）
	// 传入父 Runner 引用和 workDir，使 Lua 调用的 sh() 能复用
	r.registerLuaAPI(L, env, workDir)

	// 加载并执行 Lua 代码
	var luaCode string
	if step.Script != "" {
		luaCode = step.Script
	} else if step.Source != "" {
		luaCode = fmt.Sprintf("dofile(%q)", step.Source)
	} else {
		return "", fmt.Errorf("step %q: neither script nor source specified", step.Name)
	}

	// 捕获 print 输出（Lua print 写到 stdout）
	var printBuf threadSafeBuffer
	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		top := L.GetTop()
		parts := make([]string, 0, top)
		for i := 1; i <= top; i++ {
			parts = append(parts, L.Get(i).String())
		}
		printBuf.WriteString(strings.Join(parts, "\t"))
		printBuf.WriteString("\n")
		return 0
	}))
	defer L.SetGlobal("print", L.NewFunction(luaPrintOriginal))

	if err := L.DoString(luaCode); err != nil {
		return printBuf.String(), fmt.Errorf("step %q: lua execution failed: %w", step.Name, err)
	}

	return printBuf.String(), nil
}

// threadSafeBuffer 简单字符串缓冲（Lua DoString 是单线程的，不需要锁）
type threadSafeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *threadSafeBuffer) WriteString(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.WriteString(s)
}

func (b *threadSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// luaPrintOriginal 恢复 Lua 默认 print 函数（写到 Go stdout）
// 当前实现保留为 no-op；print 输出已经被上面捕获到 printBuf
func luaPrintOriginal(L *lua.LState) int {
	top := L.GetTop()
	parts := make([]string, 0, top)
	for i := 1; i <= top; i++ {
		parts = append(parts, L.Get(i).String())
	}
	fmt.Println(strings.Join(parts, "\t"))
	return 0
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

// registerLuaAPI 向 Lua 虚拟机注册安全的 Go API
// parentWorkDir 来自父步骤的工作目录；Lua sh() 内部调用时复用，避免 workdir 丢失
func (r *Runner) registerLuaAPI(L *lua.LState, env map[string]string, parentWorkDir string) {
	// sh(command) - 执行 shell 命令，复用父 Runner 配置
	L.SetGlobal("sh", L.NewFunction(func(L *lua.LState) int {
		command := L.CheckString(1)

		// 复用父 Runner 的配置（workdir, env, docker image），仅覆盖 Script 字段
		step := Step{
			Name:   "lua-sh",
			Type:   "shell",
			Script: command,
		}
		out, err := r.runShell(step, parentWorkDir, env)
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
		if env == nil {
			env = map[string]string{}
		}
		env[key] = value
		return 0
	}))

	// upload_artifact(path) - 上传构建产物（写到 ~/.skating/artifacts/）
	L.SetGlobal("upload_artifact", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		dest, err := r.saveArtifact(path)
		if err != nil {
			L.Push(lua.LString(""))
			L.Push(lua.LString(fmt.Sprintf("upload_artifact failed: %v", err)))
			return 2
		}
		L.Push(lua.LString(dest))
		return 1
	}))
}