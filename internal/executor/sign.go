package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hpgood/skating/internal/sign"
)

// runSign 用 secret 中保存的私钥对单个文件做 detached 签名：
//   - file 必填，支持 ${VAR} 展开；相对路径基于 workDir（与 shell 的 cmd.Dir 一致）
//   - output 缺省 <file>.sig；父目录不存在时自动创建，已存在的签名直接覆盖
//   - secret 由上层 adapter resolve 成私钥 PEM（yaml 中只存名字）
//
// 签名以 base64（带 padding）单行 + 末尾换行写入 output。
// 日志只输出路径与算法，不输出任何密钥/签名内容。
func (r *Runner) runSign(step Step, workDir string, env map[string]string, logFn func(string)) (string, error) {
	file := resolveStepPath(step.File, workDir, env)
	if file == "" {
		return "", fmt.Errorf("step %q: sign file is required", step.Name)
	}
	if step.Secret == "" {
		return "", fmt.Errorf("step %q: sign secret is required (skating secret set <name>, then set secret: <name> in yaml)", step.Name)
	}
	out := resolveStepPath(step.Output, workDir, env)
	if out == "" {
		out = file + ".sig"
	}
	algorithm := strings.TrimSpace(step.Algorithm)

	if err := r.ctx.Err(); err != nil {
		return "", fmt.Errorf("step %q: context canceled before sign: %w", step.Name, err)
	}

	sigB64, resolved, err := sign.SignFile(file, []byte(step.Secret), algorithm)
	if err != nil {
		return "", fmt.Errorf("step %q: sign %q: %w", step.Name, file, err)
	}

	if dir := filepath.Dir(out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("step %q: create signature dir %q: %w", step.Name, dir, err)
		}
	}
	if err := os.WriteFile(out, []byte(sigB64+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("step %q: write signature %q: %w", step.Name, out, err)
	}

	if err := r.ctx.Err(); err != nil {
		return "", fmt.Errorf("step %q: context canceled after sign: %w", step.Name, err)
	}

	msg := fmt.Sprintf("[sign] %s -> %s (%s)", file, out, resolved)
	if logFn != nil {
		logFn(msg)
	}
	return msg, nil
}

// runVerify 校验 detached 签名：
//   - file 必填；signature 缺省 <file>.sig
//   - 公钥来源二选一：public_key（本地文件）或 secret（公钥 PEM 存在 secret 中）
//   - 验签失败时 step 失败，错误信息不包含公钥/签名内容
//
// 成功时输出 "[verify] <file> OK (<algorithm>)" 摘要。
func (r *Runner) runVerify(step Step, workDir string, env map[string]string, logFn func(string)) (string, error) {
	file := resolveStepPath(step.File, workDir, env)
	if file == "" {
		return "", fmt.Errorf("step %q: verify file is required", step.Name)
	}

	pubPath := resolveStepPath(step.PublicKey, workDir, env)
	sigPath := resolveStepPath(step.Signature, workDir, env)
	if sigPath == "" {
		sigPath = file + ".sig"
	}

	hasPub := pubPath != ""
	hasSecret := step.Secret != ""
	if hasPub == hasSecret {
		return "", fmt.Errorf("step %q: verify requires exactly one of public_key or secret", step.Name)
	}

	var pubPEM []byte
	if hasSecret {
		pubPEM = []byte(step.Secret)
	} else {
		data, err := os.ReadFile(pubPath)
		if err != nil {
			return "", fmt.Errorf("step %q: read public key %q: %w", step.Name, pubPath, err)
		}
		pubPEM = data
	}

	sigBytes, err := os.ReadFile(sigPath)
	if err != nil {
		return "", fmt.Errorf("step %q: read signature %q: %w", step.Name, sigPath, err)
	}

	if err := r.ctx.Err(); err != nil {
		return "", fmt.Errorf("step %q: context canceled before verify: %w", step.Name, err)
	}

	resolved, err := sign.VerifyFile(file, string(sigBytes), pubPEM, strings.TrimSpace(step.Algorithm))
	if err != nil {
		return "", fmt.Errorf("step %q: verify %q: %w", step.Name, file, err)
	}

	if err := r.ctx.Err(); err != nil {
		return "", fmt.Errorf("step %q: context canceled after verify: %w", step.Name, err)
	}

	msg := fmt.Sprintf("[verify] %s OK (%s)", file, resolved)
	if logFn != nil {
		logFn(msg)
	}
	return msg, nil
}

// resolveStepPath 展开 ${VAR} 并把相对路径解析到项目目录（workDir），
// 与 shell 步骤的 cmd.Dir 语义一致。空输入返回空串。
func resolveStepPath(p, workDir string, env map[string]string) string {
	p = strings.TrimSpace(expandVars(p, env))
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(workDir, p)
}
