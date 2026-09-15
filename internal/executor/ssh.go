package executor

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// runSsh 通过原生 SSH 连接远端，把 script 喂给远端 bash 执行。
// 输出逐行进入 lineStreamer：颜色日志、::set-var 跨 step 变量捕获与
// shell/docker 步骤完全一致。
func (r *Runner) runSsh(step Step, env map[string]string, logFn func(msg string)) (string, error) {
	conn, err := r.dialSSH(step, env)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	client := conn.Client

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("step %q: new session: %w", step.Name, err)
	}
	defer session.Close()

	stdoutW := &lineStreamer{logFn: logFn, color: colorSSH, onSetVar: r.onSetVar}
	stderrW := &lineStreamer{logFn: logFn, color: colorSSH, onSetVar: r.onSetVar}
	session.Stdout = stdoutW
	session.Stderr = stderrW

	// 脚本通过 stdin 喂给远端 bash，规避命令行长度与引号转义问题
	session.Stdin = strings.NewReader(buildRemoteScript(step, env))

	err = session.Run("bash -s")
	stdoutW.flush()
	stderrW.flush()
	output := stdoutW.String() + stderrW.String()

	if err != nil {
		var ee *ssh.ExitError
		if ok := asExitError(err, &ee); ok {
			return output, fmt.Errorf("ssh command failed (remote exit status %d):\n%s", ee.ExitStatus(), output)
		}
		return output, fmt.Errorf("ssh exec: %w\n%s", err, output)
	}
	return output, nil
}

// dialSSH 建立到远端的 SSH 连接，供 ssh/scp/sync 步骤共用：
// 校验 host/user/secret 必填项（host/user 支持 ${VAR} 变量展开），
// host key 校验采用 accept-all（CI 工具常见做法，内部服务器/云主机场景省事），
// 30s 连接超时，r.ctx 取消（构建超时）时自动关闭连接。
// 返回的 sshConn 须由调用方 Close，以释放看门狗 goroutine。
func (r *Runner) dialSSH(step Step, env map[string]string) (*sshConn, error) {
	// host/user 支持 "AAA${Var}" 形式的变量展开（如 host: 10.0.0.${SEG}），
	// 未定义变量保留原样，展开后做必填校验
	host := expandVars(step.Host, env)
	user := expandVars(step.User, env)
	if host == "" {
		return nil, fmt.Errorf("step %q: ssh host is required", step.Name)
	}
	if user == "" {
		return nil, fmt.Errorf("step %q: ssh user is required", step.Name)
	}
	if step.Secret == "" {
		return nil, fmt.Errorf("step %q: ssh secret is required (skating secret set <name>)", step.Name)
	}

	port := step.Port
	if port == 0 {
		port = 22
	}

	authMethods, err := sshAuthMethods(step.Auth, step.Secret)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", step.Name, err)
	}

	inner, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("step %q: ssh dial %s@%s:%d: %w", step.Name, user, host, port, err)
	}

	conn := &sshConn{Client: inner, done: make(chan struct{})}
	// 取消/超时时关闭连接：正在进行的传输/执行会随之以错误返回
	if r.ctx != nil && r.ctx != context.Background() {
		go func() {
			select {
			case <-r.ctx.Done():
				_ = conn.Close()
			case <-conn.done:
			}
		}()
	}
	return conn, nil
}

// sshConn 包装 ssh.Client：Close 幂等，并保证看门狗 goroutine 退出
type sshConn struct {
	*ssh.Client
	closeOnce sync.Once
	done      chan struct{}
}

func (c *sshConn) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	return c.Client.Close()
}

// asExitError 避免 errors.As 依赖：直接类型断言即可覆盖 *ssh.ExitError
func asExitError(err error, target **ssh.ExitError) bool {
	if ee, ok := err.(*ssh.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// sshAuthMethods 根据认证方式与材料构建 ssh.AuthMethod
func sshAuthMethods(auth, secret string) ([]ssh.AuthMethod, error) {
	if auth == "" {
		auth = "password"
	}
	switch strings.ToLower(auth) {
	case "password":
		return []ssh.AuthMethod{ssh.Password(secret)}, nil
	case "key":
		signer, err := ssh.ParsePrivateKey([]byte(secret))
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	default:
		return nil, fmt.Errorf("unknown ssh auth %q (want password|key)", auth)
	}
}

// buildRemoteScript 组装远端执行的完整脚本：
// 1) export 注入环境变量（env 视图与 shell 步骤一致：全局 env + step env + 变量池）
// 2) cd 到远端工作目录（step.WorkDir）
// 3) 用户脚本
func buildRemoteScript(step Step, env map[string]string) string {
	var sb strings.Builder
	for _, k := range slices.Sorted(maps.Keys(env)) {
		sb.WriteString("export " + shQuote(k+"="+env[k]) + "\n")
	}
	if step.WorkDir != "" {
		// workdir 支持 ${VAR} 展开（如 /opt/${APP}）
		sb.WriteString("cd " + shQuote(expandVars(step.WorkDir, env)) + "\n")
	}
	sb.WriteString(step.Script)
	if !strings.HasSuffix(step.Script, "\n") {
		sb.WriteString("\n")
	}
	return sb.String()
}

// shQuote bash 单引号转义
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
