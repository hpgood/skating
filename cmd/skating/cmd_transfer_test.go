package main

// scp/sync 步骤端到端测试：yaml 中只保存 secret 名，认证材料在运行时
// 从 ~/.skating/secrets.json resolve 出来交给 executor，全程不落日志；
// 传输经由 testssh 的 sftp subsystem 操作真实文件系统。

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hpgood/skating/internal/store"
	"github.com/hpgood/skating/internal/testssh"
)

func TestRun_ScpStepPushWithSecret(t *testing.T) {
	home := setupE2E(t)
	sftpRoot := t.TempDir()
	addr := testssh.StartSftp(t, testssh.PasswordConfig("deployer", "testpass"), sftpRoot)
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	// 全局 secret：认证材料保存在 ~/.skating/secrets.json，yaml 只引用名字
	s, err := store.NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := s.SetSecret("deploy-pass", "testpass"); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	projDir := filepath.Join(home, "scpproj")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)
	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	// 产物文件：scp push 的 local 支持 ${VAR} 展开（--env 注入）
	artifact := filepath.Join(projDir, "dist", "app.tar.gz")
	if err := os.MkdirAll(filepath.Dir(artifact), 0755); err != nil {
		t.Fatalf("mkdir artifact: %v", err)
	}
	if err := os.WriteFile(artifact, []byte("artifact-payload"), 0644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	yamlContent := fmt.Sprintf(`name: scpproj
pipeline:
  stages:
    - name: ship
      steps:
        - name: upload
          type: scp
          host: %s
          port: %d
          user: deployer
          auth: password
          secret: deploy-pass
          local: dist/app.tar.gz
          remote: rel/${GITTAG}/app.tar.gz
`, host, port)
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "scpproj", "--env", "GITTAG=v1")
	if strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("run failed: %q", stderr)
	}
	combined := stdout + stderr

	// 1. 文件真正落到 sftpRoot/rel/v1/app.tar.gz
	got, err := os.ReadFile(filepath.Join(sftpRoot, "rel", "v1", "app.tar.gz"))
	if err != nil {
		t.Fatalf("artifact not uploaded: %v\noutput: %s", err, combined)
	}
	if string(got) != "artifact-payload" {
		t.Errorf("uploaded content = %q, want artifact-payload", got)
	}
	// 2. 认证材料不落日志
	if strings.Contains(combined, "testpass") {
		t.Errorf("secret value leaked into logs: %q", combined)
	}
	// 3. 传输摘要进入日志
	if !strings.Contains(combined, "[scp] push") {
		t.Errorf("scp summary missing: %q", combined)
	}
}

func TestRun_SyncStepMirror(t *testing.T) {
	home := setupE2E(t)
	sftpRoot := t.TempDir()
	addr := testssh.StartSftp(t, testssh.PasswordConfig("deployer", "testpass"), sftpRoot)
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	s, err := store.NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := s.SetSecret("deploy-pass", "testpass"); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	projDir := filepath.Join(home, "syncproj")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)
	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	site := filepath.Join(projDir, "site")
	if err := os.MkdirAll(site, 0755); err != nil {
		t.Fatalf("mkdir site: %v", err)
	}
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<h1>hi</h1>"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// 远端预置多余文件，镜像删除应清掉它
	if err := os.MkdirAll(filepath.Join(sftpRoot, "www"), 0755); err != nil {
		t.Fatalf("mkdir remote: %v", err)
	}
	stale := filepath.Join(sftpRoot, "www", "stale.txt")
	if err := os.WriteFile(stale, []byte("stale"), 0644); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	yamlContent := fmt.Sprintf(`name: syncproj
pipeline:
  stages:
    - name: ship
      steps:
        - name: mirror
          type: sync
          host: %s
          port: %d
          user: deployer
          auth: password
          secret: deploy-pass
          local: site
          remote: www
`, host, port)
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "syncproj")
	if strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("run failed: %q", stderr)
	}
	combined := stdout + stderr

	// index.html 已上传
	got, err := os.ReadFile(filepath.Join(sftpRoot, "www", "index.html"))
	if err != nil {
		t.Fatalf("index.html not uploaded: %v\noutput: %s", err, combined)
	}
	if string(got) != "<h1>hi</h1>" {
		t.Errorf("uploaded content = %q", got)
	}
	// 远端多余文件被镜像删除
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale.txt should be pruned")
	}
	// sync 摘要进入日志
	if !strings.Contains(combined, "[sync]") || !strings.Contains(combined, "uploaded 1") {
		t.Errorf("sync summary missing: %q", combined)
	}
}
