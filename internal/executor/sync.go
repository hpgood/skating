package executor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/pkg/sftp"
)

// runSync 把本地目录镜像到远端目录（rsync 语义的简化版）：
//  1. 增量：遍历本地文件，与远端比较 size + mtime（秒级精度），未变化则跳过
//  2. 镜像删除：delete（缺省 true）时删除远端存在但本地不存在的文件/目录
//
// local/remote 支持 ${VAR} 变量展开；local 必须是目录。
// 输出经 logFn 打印上传/跳过/删除统计。
func (r *Runner) runSync(step Step, env map[string]string, logFn func(msg string)) (string, error) {
	local := expandVars(step.Local, env)
	remote := expandVars(step.Remote, env)
	if local == "" {
		return "", fmt.Errorf("step %q: sync local is required", step.Name)
	}
	if remote == "" {
		return "", fmt.Errorf("step %q: sync remote is required", step.Name)
	}
	info, err := os.Stat(local)
	if err != nil {
		return "", fmt.Errorf("step %q: sync local %q: %w", step.Name, local, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("step %q: sync local %q is not a directory", step.Name, local)
	}

	del := step.Delete == nil || *step.Delete

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

	// 1) 扫描远端目录树（relPath 以 / 分隔），供增量比较与镜像删除；
	// 远端目录尚不存在（首次部署）视为空树
	remoteTree := map[string]remoteEntry{}
	if err := scanRemote(cl, remote, remoteTree); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("step %q: sync scan remote %q: %w", step.Name, remote, err)
	}

	// 2) 增量上传
	var st syncStats
	if err := syncDir(cl, local, remote, remoteTree, &st); err != nil {
		return "", fmt.Errorf("step %q: sync upload: %w", step.Name, err)
	}

	// 3) 镜像删除远端多余文件
	if del {
		if err := pruneRemote(cl, local, remote, &st); err != nil {
			return "", fmt.Errorf("step %q: sync prune: %w", step.Name, err)
		}
	}

	msg := fmt.Sprintf("[sync] %s → %s: uploaded %d, skipped %d, removed %d (%s)",
		local, remote, st.uploaded, st.skipped, st.removed, humanSize(st.bytes))
	if logFn != nil {
		logFn(msg)
	}
	return msg, nil
}

type remoteEntry struct {
	size  int64
	mtime time.Time
	isDir bool
}

type syncStats struct {
	uploaded int
	skipped  int
	removed  int
	bytes    int64
}

// scanRemote 递归收集远端目录树下所有条目，key 为相对 dir 的路径（/ 分隔）
func scanRemote(cl *sftp.Client, dir string, out map[string]remoteEntry) error {
	entries, err := cl.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		rel := path.Join(dir, e.Name())
		out[rel] = remoteEntry{size: e.Size(), mtime: e.ModTime(), isDir: e.IsDir()}
		if e.IsDir() {
			if err := scanRemote(cl, rel, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// syncDir 遍历本地目录增量上传：远端同名文件 size 相同且 mtime（秒级）
// 相同则跳过，否则通过 pushFile 上传
func syncDir(cl *sftp.Client, localDir, remoteDir string, tree map[string]remoteEntry, st *syncStats) error {
	return filepath.WalkDir(localDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(localDir, p)
			if err != nil {
				return err
			}
			rp := path.Join(remoteDir, filepath.ToSlash(rel))
			if re, ok := tree[rp]; ok && !re.isDir &&
				re.size == info.Size() &&
				re.mtime.Equal(info.ModTime().Truncate(time.Second)) {
				st.skipped++
				return nil
			}
			n, err := pushFile(cl, p, rp)
			if err != nil {
				return err
			}
			st.uploaded++
			st.bytes += n
		}
		return nil
	})
}

// pruneRemote 删除远端存在但本地不存在的条目：
// 本地无对应目录时整棵远端子树 RemoveAll；文件逐个 Remove。
// 本地 Stat 出现非 NotExist 错误（如权限）时保守中止，避免误删。
func pruneRemote(cl *sftp.Client, localDir, remoteDir string, st *syncStats) error {
	entries, err := cl.ReadDir(remoteDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		rp := path.Join(remoteDir, e.Name())
		lp := filepath.Join(localDir, e.Name())
		_, statErr := os.Stat(lp)
		switch {
		case statErr == nil:
			// 本地存在：目录递归深入，文件不动
			if e.IsDir() {
				if err := pruneRemote(cl, lp, rp, st); err != nil {
					return err
				}
			}
		case os.IsNotExist(statErr):
			// 本地没有：远端多余，删除（目录整棵删）
			if err := cl.RemoveAll(rp); err != nil {
				return err
			}
			st.removed++
		default:
			return statErr
		}
	}
	return nil
}
