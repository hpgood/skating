package executor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hpgood/skating/internal/testssh"
)

// startSftpServer 启动带 sftp subsystem 的测试服务器，
// sftpRoot 为 sftp 操作的基准目录，返回 host/port/root
func startSftpServer(t *testing.T) (string, int, string) {
	t.Helper()
	root := t.TempDir()
	addr := testssh.StartSftp(t, testssh.PasswordConfig("testuser", "testpass"), root)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)
	return host, port, root
}

func transferStep(typ, host string, port int) Step {
	return Step{
		Name: "ship", Type: typ,
		Host: host, Port: port, User: "testuser",
		Auth: "password", Secret: "testpass",
	}
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// --- scp ---

func TestRunner_ExecuteScp_PushFile(t *testing.T) {
	host, port, root := startSftpServer(t)

	localDir := t.TempDir()
	src := filepath.Join(localDir, "app.tar.gz")
	writeFileT(t, src, "payload-123")

	r := NewRunner()
	step := transferStep("scp", host, port)
	step.Local = src
	step.Remote = "deploy/app.tar.gz" // 相对路径基于 sftpRoot

	if _, err := r.Execute(step, "ignored", nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := readFile(t, filepath.Join(root, "deploy", "app.tar.gz")); got != "payload-123" {
		t.Errorf("remote content = %q, want payload-123", got)
	}
}

func TestRunner_ExecuteScp_PushDir(t *testing.T) {
	host, port, root := startSftpServer(t)

	localDir := t.TempDir()
	writeFileT(t, filepath.Join(localDir, "bin", "app"), "binary")
	writeFileT(t, filepath.Join(localDir, "bin", "sub", "lib.so"), "lib")
	writeFileT(t, filepath.Join(localDir, "README.md"), "readme")

	r := NewRunner()
	step := transferStep("scp", host, port)
	step.Local = localDir
	step.Remote = "srv"

	if _, err := r.Execute(step, "ignored", nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for rel, want := range map[string]string{
		"srv/bin/app":        "binary",
		"srv/bin/sub/lib.so": "lib",
		"srv/README.md":      "readme",
	} {
		if got := readFile(t, filepath.Join(root, filepath.FromSlash(rel))); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
}

func TestRunner_ExecuteScp_PullDir(t *testing.T) {
	host, port, root := startSftpServer(t)
	// 预置远端文件（sftpRoot 下）
	writeFileT(t, filepath.Join(root, "logs", "a.log"), "log-a")
	writeFileT(t, filepath.Join(root, "logs", "sub", "b.log"), "log-b")

	localOut := filepath.Join(t.TempDir(), "out")
	r := NewRunner()
	step := transferStep("scp", host, port)
	step.Direction = "pull"
	step.Local = localOut
	step.Remote = "logs"

	if _, err := r.Execute(step, "ignored", nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := readFile(t, filepath.Join(localOut, "a.log")); got != "log-a" {
		t.Errorf("a.log = %q", got)
	}
	if got := readFile(t, filepath.Join(localOut, "sub", "b.log")); got != "log-b" {
		t.Errorf("sub/b.log = %q", got)
	}
}

func TestRunner_ExecuteScp_VarExpansion(t *testing.T) {
	host, port, root := startSftpServer(t)

	localDir := t.TempDir()
	src := filepath.Join(localDir, "app.bin")
	writeFileT(t, src, "data")

	r := NewRunner()
	step := transferStep("scp", host, port)
	step.Local = src
	step.Remote = "rel/${SKA_GIT_BRANCH}/app.bin"

	env := map[string]string{"SKA_GIT_BRANCH": "main"}
	if _, err := r.Execute(step, "ignored", env, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := readFile(t, filepath.Join(root, "rel", "main", "app.bin")); got != "data" {
		t.Errorf("remote content = %q, want data under rel/main", got)
	}
}

func TestRunner_ExecuteScp_BadRequest(t *testing.T) {
	host, port, _ := startSftpServer(t)
	r := NewRunner()
	cases := []Step{
		{Name: "no-local", Type: "scp", Host: host, Port: port, User: "u", Secret: "s", Remote: "a"},
		{Name: "no-remote", Type: "scp", Host: host, Port: port, User: "u", Secret: "s", Local: "a"},
		{Name: "bad-dir", Type: "scp", Host: host, Port: port, User: "u", Secret: "s", Local: "a", Remote: "b", Direction: "rsync"},
		{Name: "no-such-file", Type: "scp", Host: host, Port: port, User: "u", Secret: "s", Local: "Z:/no/such/file", Remote: "b"},
	}
	for _, step := range cases {
		if _, err := r.Execute(step, "ignored", nil, nil); err == nil {
			t.Errorf("step %q: expected error", step.Name)
		}
	}
}

// --- sync ---

func TestRunner_ExecuteSync_Incremental(t *testing.T) {
	host, port, root := startSftpServer(t)

	localDir := t.TempDir()
	writeFileT(t, filepath.Join(localDir, "a.txt"), "aaa")
	writeFileT(t, filepath.Join(localDir, "b.txt"), "bbb")
	writeFileT(t, filepath.Join(localDir, "sub", "c.txt"), "ccc")

	r := NewRunner()
	step := transferStep("sync", host, port)
	step.Local = localDir
	step.Remote = "site"

	// 第一次：全部上传
	out1, err := r.Execute(step, "ignored", nil, nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if !strings.Contains(out1, "uploaded 3, skipped 0") {
		t.Errorf("first sync out = %q, want uploaded 3, skipped 0", out1)
	}

	// 第二次：无变化，全部跳过
	out2, err := r.Execute(step, "ignored", nil, nil)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if !strings.Contains(out2, "uploaded 0, skipped 3") {
		t.Errorf("second sync out = %q, want uploaded 0, skipped 3", out2)
	}

	// 修改一个文件（内容与大小变化），只重传该文件
	writeFileT(t, filepath.Join(localDir, "b.txt"), "b-changed-longer")
	out3, err := r.Execute(step, "ignored", nil, nil)
	if err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if !strings.Contains(out3, "uploaded 1, skipped 2") {
		t.Errorf("third sync out = %q, want uploaded 1, skipped 2", out3)
	}
	_ = root
}

func TestRunner_ExecuteSync_Prune(t *testing.T) {
	host, port, root := startSftpServer(t)

	localDir := t.TempDir()
	writeFileT(t, filepath.Join(localDir, "keep.txt"), "keep")

	r := NewRunner()
	step := transferStep("sync", host, port)
	step.Local = localDir
	step.Remote = "site"

	// 远端预置多余文件与目录（sync 会镜像删除）
	writeFileT(t, filepath.Join(root, "site", "stale.txt"), "stale")
	writeFileT(t, filepath.Join(root, "site", "olddir", "x.txt"), "x")

	if _, err := r.Execute(step, "ignored", nil, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}
	for _, rel := range []string{"site/stale.txt", "site/olddir/x.txt"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s should be pruned", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "site", "keep.txt")); err != nil {
		t.Errorf("keep.txt should exist: %v", err)
	}

	// delete=false：保留远端多余文件
	writeFileT(t, filepath.Join(root, "site", "stale.txt"), "stale-again")
	noDel := false
	step.Delete = &noDel
	if _, err := r.Execute(step, "ignored", nil, nil); err != nil {
		t.Fatalf("sync(no delete): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "site", "stale.txt")); err != nil {
		t.Errorf("stale.txt should be kept when delete=false: %v", err)
	}
}

func TestRunner_ExecuteSync_LocalNotDir(t *testing.T) {
	host, port, _ := startSftpServer(t)
	r := NewRunner()
	step := transferStep("sync", host, port)
	step.Local = filepath.Join(t.TempDir(), "not-a-dir")
	step.Remote = "site"
	if _, err := r.Execute(step, "ignored", nil, nil); err == nil {
		t.Error("expected error when local is not a directory")
	}
}

func TestExpandVars(t *testing.T) {
	env := map[string]string{"SKA_GIT_BRANCH": "main", "VER": "1.0"}
	cases := map[string]string{
		"build/${SKA_GIT_BRANCH}/app.tar.gz": "build/main/app.tar.gz",
		"rel/$VER/bin":                       "rel/1.0/bin",
		"unchanged/${NOT_SET}":               "unchanged/${NOT_SET}",
		"plain":                              "plain",
	}
	for in, want := range cases {
		if got := expandVars(in, env); got != want {
			t.Errorf("expandVars(%q) = %q, want %q", in, got, want)
		}
	}
}
