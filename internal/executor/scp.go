package executor

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
)

// runScp 通过 sftp 在本地与远端之间复制文件或目录（scp 语义）：
//   - direction: "push"（本地→远端，缺省）或 "pull"（远端→本地）
//   - local/remote 支持 ${VAR} 变量展开（如 build/${SKA_GIT_BRANCH}/app.tar.gz）
//   - 文件与目录均支持；目录递归复制，保留 mtime（便于后续 sync 增量）
//
// 输出经 logFn 打印传输摘要（文件数与字节数）。
func (r *Runner) runScp(step Step, env map[string]string, logFn func(msg string)) (string, error) {
	local := expandVars(step.Local, env)
	remote := expandVars(step.Remote, env)
	if local == "" {
		return "", fmt.Errorf("step %q: scp local is required", step.Name)
	}
	if remote == "" {
		return "", fmt.Errorf("step %q: scp remote is required", step.Name)
	}

	direction := strings.ToLower(step.Direction)
	if direction == "" {
		direction = "push"
	}
	if direction != "push" && direction != "pull" {
		return "", fmt.Errorf("step %q: unknown scp direction %q (want push|pull)", step.Name, step.Direction)
	}

	conn, err := r.dialSSH(step, env)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	cl, err := sftp.NewClient(conn.Client)
	if err != nil {
		return "", fmt.Errorf("step %q: sftp client: %w", step.Name, err)
	}
	defer cl.Close()

	tr := &transfer{}
	switch direction {
	case "push":
		info, err := os.Stat(local)
		if err != nil {
			return "", fmt.Errorf("step %q: scp local %q: %w", step.Name, local, err)
		}
		if info.IsDir() {
			err = pushDir(cl, local, remote, tr)
		} else {
			_, err = pushFile(cl, local, remote)
		}
	case "pull":
		fi, err := cl.Stat(remote)
		if err != nil {
			return "", fmt.Errorf("step %q: scp remote %q: %w", step.Name, remote, err)
		}
		if fi.IsDir() {
			err = pullDir(cl, remote, local, tr)
		} else {
			_, err = pullFile(cl, remote, local)
		}
	}
	if err != nil {
		return "", fmt.Errorf("step %q: scp %s: %w", step.Name, direction, err)
	}

	msg := fmt.Sprintf("[scp] %s %s ↔ %s: %d file(s), %s", direction, local, remote, tr.n, humanSize(tr.bytes))
	if logFn != nil {
		logFn(msg)
	}
	return msg, nil
}

// expandVars 把 ${KEY}/$KEY 替换为 env[KEY]；未定义的变量保留原样，
// 避免拼错变量名时静默生成错误路径
func expandVars(s string, env map[string]string) string {
	return os.Expand(s, func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return "${" + k + "}"
	})
}

// transfer 累计传输文件数与字节数
type transfer struct {
	n     int
	bytes int64
}

// pushFile 上传单个文件到远端，自动创建远端父目录并保留 mtime，
// 返回传输字节数
func pushFile(cl *sftp.Client, localPath, remotePath string) (int64, error) {
	src, err := os.Open(localPath)
	if err != nil {
		return 0, err
	}
	defer src.Close()

	if dir := path.Dir(remotePath); dir != "" && dir != "." && dir != "/" {
		if err := cl.MkdirAll(dir); err != nil {
			return 0, fmt.Errorf("mkdir remote dir %s: %w", dir, err)
		}
	}
	dst, err := cl.Create(remotePath)
	if err != nil {
		return 0, err
	}
	defer dst.Close()

	n, err := io.Copy(dst, src)
	if err != nil {
		return n, err
	}
	// 保留 mtime：scp push 的产物随后可被 sync 增量跳过
	if info, statErr := src.Stat(); statErr == nil {
		_ = cl.Chtimes(remotePath, info.ModTime(), info.ModTime())
	}
	return n, nil
}

// pushDir 递归上传本地目录到远端目录（远端目录不存在时自动创建）
func pushDir(cl *sftp.Client, localDir, remoteDir string, tr *transfer) error {
	return filepath.WalkDir(localDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(localDir, p)
		if err != nil {
			return err
		}
		rp := path.Join(remoteDir, filepath.ToSlash(rel))
		if d.IsDir() {
			return cl.MkdirAll(rp)
		}
		n, err := pushFile(cl, p, rp)
		if err != nil {
			return err
		}
		tr.n++
		tr.bytes += n
		return nil
	})
}

// pullFile 从远端下载单个文件到本地，自动创建本地父目录并保留 mtime，
// 返回传输字节数
func pullFile(cl *sftp.Client, remotePath, localPath string) (int64, error) {
	src, err := cl.Open(remotePath)
	if err != nil {
		return 0, err
	}
	defer src.Close()

	if dir := filepath.Dir(localPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
	}
	dst, err := os.Create(localPath)
	if err != nil {
		return 0, err
	}
	defer dst.Close()

	n, err := io.Copy(dst, src)
	if err != nil {
		return n, err
	}
	if fi, statErr := src.Stat(); statErr == nil {
		_ = os.Chtimes(localPath, fi.ModTime(), fi.ModTime())
	}
	return n, nil
}

// pullDir 递归下载远端目录到本地目录
func pullDir(cl *sftp.Client, remoteDir, localDir string, tr *transfer) error {
	entries, err := cl.ReadDir(remoteDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		rp := path.Join(remoteDir, e.Name())
		lp := filepath.Join(localDir, e.Name())
		if e.IsDir() {
			if err := pullDir(cl, rp, lp, tr); err != nil {
				return err
			}
			continue
		}
		n, err := pullFile(cl, rp, lp)
		if err != nil {
			return err
		}
		tr.n++
		tr.bytes += n
	}
	return nil
}

// humanSize 把字节数格式化为人类可读大小（B/KB/MB/GB）
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
