package executor

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// runShell 执行 Shell 脚本
// 若 Runner 配置了 dockerImage 且主机有 docker，则在容器中运行；否则在 host 直接执行。
func (r *Runner) runShell(step Step, workDir string, env map[string]string, logFn func(msg string)) (string, error) {
	var cmd *exec.Cmd

	// 优先使用 Docker 容器执行
	if r.dockerImage != "" && r.dockerAvailable {
		return r.runShellInDocker(step, workDir, env, logFn)
	}

	shell := "bash"
	if runtime.GOOS == "windows" {
		shell = "powershell"
	}
	userShell, ok := env["SKA_SHELL"]
	if ok {
		shell = userShell
	}
	isPowerShell := isPwsh(shell)
	color := colorForShell(shell)
	if step.Script != "" {
		// 内联脚本：写入临时文件后执行
		ext := ".sh"

		if isPowerShell {
			ext = ".ps1"
		}
		script := step.Script

		if isPowerShell {
			// 开启调试模式；注意是前置拼接，不能覆盖掉用户脚本
			// 强制 PowerShell 输出流用 UTF-8：中文 Windows 下 powershell.exe 重定向输出默认走 GBK，Go 侧按 UTF-8 读会乱码
			script = "Set-PSDebug -Trace 1\r\n" + psUTF8Line + psEnvPreamble(env) + "\r\n" + script + "\r\n" + psExitLine
		}
		tmpFile, err := os.CreateTemp("", "skating-shell-*"+ext)
		if err != nil {
			return "", fmt.Errorf("create temp file: %w", err)
		}
		defer os.Remove(tmpFile.Name())

		// logFn("script: " + script)

		data := script
		if isPowerShell {
			// PowerShell 5.1 读无 BOM 脚本按 ANSI 解码，加 UTF-8 BOM 保证脚本内的中文不乱码
			data = "\xEF\xBB\xBF" + data
		}
		if _, err := tmpFile.WriteString(data); err != nil {
			tmpFile.Close()
			return "", fmt.Errorf("write temp script: %w", err)
		}
		tmpFile.Close()

		cmd = r.buildShellCmd(tmpFile.Name(), workDir, env, logFn)
	} else if step.Source != "" {
		// 外部脚本
		scriptPath := step.Source
		if !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(workDir, scriptPath)
		}
		cmd = r.buildShellCmd(scriptPath, workDir, env, logFn)
	} else {
		return "", fmt.Errorf("step %q: neither script nor source specified", step.Name)
	}

	stdoutW := &lineStreamer{logFn: logFn, color: color}
	stderrW := &lineStreamer{logFn: logFn, color: color}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	err := cmd.Run()
	// Run 返回时内部拷贝已完成，转发最后一段未换行的残余输出
	stdoutW.flush()
	stderrW.flush()

	output := stdoutW.String() + stderrW.String()

	if err != nil {
		return output, fmt.Errorf("shell command failed: %w\n%s", err, output)
	}

	return output, nil
}

// runShellInDocker 在 Docker 容器内执行 shell 步骤
// 通过 `docker run --rm` 启动一次性容器，挂载 workDir，注入 env
func (r *Runner) runShellInDocker(step Step, workDir string, env map[string]string, logFn func(msg string)) (string, error) {
	if step.Script == "" && step.Source == "" {
		return "", fmt.Errorf("step %q: neither script nor source specified", step.Name)
	}

	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}

	// 内联脚本需要写入文件后挂载到容器；外部脚本直接走绝对路径
	var containerScriptPath string
	hostScriptPath := ""
	tmpFileCleanup := func() {}

	if step.Script != "" {
		tmpFile, err := os.CreateTemp("", "skating-shell-*.sh")
		if err != nil {
			return "", fmt.Errorf("create temp file: %w", err)
		}
		if _, err := tmpFile.WriteString(step.Script); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("write temp script: %w", err)
		}
		tmpFile.Close()
		hostScriptPath = tmpFile.Name()
		tmpFileCleanup = func() { os.Remove(hostScriptPath) }
		containerScriptPath = "/tmp/skating-script.sh"
	} else {
		absSource, err := filepath.Abs(step.Source)
		if err != nil {
			return "", fmt.Errorf("resolve source path: %w", err)
		}
		hostScriptPath = absSource
		containerScriptPath = "/workdir/" + filepath.Base(absSource)
	}
	defer tmpFileCleanup()

	// 构建 docker run 命令
	args := []string{"run", "--rm"}

	// 挂载 workDir 到 /workdir（容器内固定的工程根目录）
	args = append(args, "-v", fmt.Sprintf("%s:/workdir", absWorkDir))
	args = append(args, "-w", "/workdir")

	// 内联脚本还需额外挂载
	if step.Script != "" {
		args = append(args, "-v", fmt.Sprintf("%s:%s", hostScriptPath, containerScriptPath))
	}

	// 注入环境变量
	for k, v := range env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	args = append(args, r.dockerImage)

	shell := "bash"

	userShell, ok := env["SKA_SHELL"]
	_args := []string{"-x"}
	if ok {
		arr := strings.Split(userShell, " ")
		shell = arr[0]
		if len(arr) > 1 {
			_args = arr[1:]
		}
	}
	args = append(args, shell)
	args = append(args, _args...)
	args = append(args, containerScriptPath)

	cmd := exec.Command("docker", args...)
	stdoutW := &lineStreamer{logFn: logFn, color: colorDocker}
	stderrW := &lineStreamer{logFn: logFn, color: colorDocker}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	err = cmd.Run()
	stdoutW.flush()
	stderrW.flush()
	output := stdoutW.String() + stderrW.String()

	if err != nil {
		return output, fmt.Errorf("docker shell command failed: %w\n%s", err, output)
	}
	return output, nil
}

// buildShellCmd 根据操作系统构建 shell 命令
func (r *Runner) buildShellCmd(scriptPath string, workDir string, env map[string]string, logFn func(msg string)) *exec.Cmd {
	var cmd *exec.Cmd

	args := []string{}
	shell := "bash"

	userShell, ok := env["SKA_SHELL"]
	if ok {
		arr := strings.Split(userShell, " ")
		shell = arr[0]
		if len(arr) > 1 {
			args = arr[1:]
		}
		args = append(args, scriptPath)
	} else {
		if runtime.GOOS == "windows" {
			// Windows: 使用 PowerShell 执行（powershell.exe 不支持 -Trace 参数）
			shell = "powershell"
		} else {
			// Linux/macOS: 使用 bash 执行
			shell = "bash"
		}
	}

	if shell == "bash" || shell == "sh" {
		args = []string{"-x", scriptPath}
	} else if shell == "powershell" {
		args = []string{"-ExecutionPolicy", "Bypass", "-File", scriptPath}
	} else if len(args) == 0 {
		args = append(args, scriptPath)
	}

	if logFn != nil {
		logFn(colorize(colorForShell(shell), shell+" "+strings.Join(args, " ")))
	}

	cmd = exec.Command(shell, args...)
	cmd.Dir = workDir

	// 注入环境变量
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	return cmd
}

// psUTF8Line 让 PowerShell 把输出流切到 UTF-8（pwsh/PS7 默认 UTF-8，此行对其无副作用）
const psUTF8Line = "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8\r\n"

// psExitLine 让脚本中 native 命令（go build 等）的退出码成为脚本退出码。
// powershell -File 脚本正常结束时固定返回 0，最后一条命令失败会被吞掉；
// 仅在运行过 native 命令（LASTEXITCODE 变量存在）时才 exit，纯 cmdlet 脚本不受影响，
// 用户脚本里显式 exit 的语义也不受影响（追加行不会执行）。
// 做法与 GitHub Actions pwsh shell 一致。
const psExitLine = "if (Test-Path -LiteralPath variable:\\LASTEXITCODE) { exit $LASTEXITCODE }\r\n"

func psEnvPreamble(env map[string]string) string {
	var sb strings.Builder
	for _, k := range slices.Sorted(maps.Keys(env)) {
		if !isSimplePSVarName(&sb, k) {
			continue
		}
		fmt.Fprintf(&sb, "$%s = $env:%s\n", k, k)
	}
	return sb.String()
}

func isSimplePSVarName(sb *strings.Builder, k string) bool {
	if k == "" {
		return false
	}
	if k == "Error" || k == "Host" || k == "PID" {
		fmt.Fprintf(sb, "ignore special env: %s  \n", k)
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isPwsh 判断是否为 PowerShell（powershell.exe / pwsh）
func isPwsh(shell string) bool {
	return strings.Contains(shell, "powershell") || strings.Contains(shell, "pwsh")
}

// lineStreamer 实现 io.Writer：在累积完整输出的同时，按行实时转发到 logFn。
// logFn 为 nil 时只累积、不转发（返回值语义与原先完全一致）。
// color 非空时按对应 shell 的颜色上色后转发（累积的原始输出不受影响）。
type lineStreamer struct {
	out     strings.Builder // 完整输出（作为返回值）
	pending strings.Builder // 尚未凑齐一行的残余内容
	logFn   func(string)
	color   string
}

func (w *lineStreamer) Write(p []byte) (int, error) {
	w.out.Write(p)
	w.pending.Write(p)
	s := w.pending.String()
	w.pending.Reset()
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		w.emit(strings.TrimSuffix(s[:i], "\r"))
		s = s[i+1:]
	}
	w.pending.WriteString(s)
	return len(p), nil
}

// flush 在命令结束后转发最后一段未换行的残余输出
func (w *lineStreamer) flush() {
	s := w.pending.String()
	w.pending.Reset()
	if s != "" {
		w.emit(strings.TrimSuffix(s, "\r"))
	}
}

func (w *lineStreamer) String() string { return w.out.String() }

func (w *lineStreamer) emit(line string) {
	if w.logFn != nil {
		w.logFn(colorize(w.color, line))
	}
}
