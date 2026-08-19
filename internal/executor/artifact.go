package executor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// saveArtifact 把指定路径的文件复制到 ~/.skating/artifacts/<runner-image-or-host>/<basename>
// 返回目标绝对路径。文件不存在或读取失败时返回错误。
// runner 镜像名作为子目录，避免不同项目/镜像的产物混淆。
func (r *Runner) saveArtifact(srcPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home: %w", err)
	}

	bucket := "host"
	if r.dockerImage != "" {
		bucket = sanitizePath(r.dockerImage)
	}

	artDir := filepath.Join(home, ".skating", "artifacts", bucket)
	if err := os.MkdirAll(artDir, 0755); err != nil {
		return "", fmt.Errorf("create artifact dir: %w", err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	destPath := filepath.Join(artDir, filepath.Base(srcPath))
	dst, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create dest: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("copy: %w", err)
	}

	return destPath, nil
}

// sanitizePath 把 docker 镜像名 (e.g. "golang:1.21") 转成安全的目录名
func sanitizePath(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '/' || c == '\\' || c == ':':
			out = append(out, '_')
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}