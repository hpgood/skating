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
// 若 Runner 配置了 dockerImage 且主机有 docker，则在容器中运行；否则在 host 直接执行。
func (r *Runner) runShell(step Step, workDir string, env map[string]string) (string, error) {
	var cmd *exec.Cmd

	// 优先使用 Docker 容器执行
	if r.dockerImage != "" && r.dockerAvailable {
		return r.runShellInDocker(step, workDir, env)
	}

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

// runShellInDocker 在 Docker 容器内执行 shell 步骤
// 通过 `docker run --rm` 启动一次性容器，挂载 workDir，注入 env
func (r *Runner) runShellInDocker(step Step, workDir string, env map[string]string) (string, error) {
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
	args = append(args, "bash", containerScriptPath)

	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		output += stderr.String()
	}

	if err != nil {
		return output, fmt.Errorf("docker shell command failed: %w\n%s", err, output)
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