package executor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// runShell 执行 Shell 脚本
func (r *Runner) runShell(step Step, workDir string, env map[string]string) (string, error) {
	var cmd *exec.Cmd

	if step.Script != "" {
		// 内联脚本：写入临时文件后执行
		tmpFile, err := os.CreateTemp("", "skating-shell-*.sh")
		if err != nil {
			return "", fmt.Errorf("create temp file: %w", err)
		}
		defer os.Remove(tmpFile.Name())

		if _, err := tmpFile.WriteString(step.Script); err != nil {
			tmpFile.Close()
			return "", fmt.Errorf("write temp script: %w", err)
		}
		tmpFile.Close()

		cmd = r.buildShellCmd(tmpFile.Name(), workDir, env)
	} else if step.Source != "" {
		// 外部脚本
		scriptPath := step.Source
		if !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(workDir, scriptPath)
		}
		cmd = r.buildShellCmd(scriptPath, workDir, env)
	} else {
		return "", fmt.Errorf("step %q: neither script nor source specified", step.Name)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		output += stderr.String()
	}

	if err != nil {
		return output, fmt.Errorf("shell command failed: %w\n%s", err, output)
	}

	return output, nil
}

// buildShellCmd 根据操作系统构建 shell 命令
func (r *Runner) buildShellCmd(scriptPath string, workDir string, env map[string]string) *exec.Cmd {
	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		// Windows: 使用 PowerShell 执行
		cmd = exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	} else {
		// Linux/macOS: 使用 bash 执行
		cmd = exec.Command("bash", scriptPath)
	}

	cmd.Dir = workDir

	// 注入环境变量
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	return cmd
}