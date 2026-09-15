package main

// SSH 步骤端到端测试：yaml 中只保存 secret 名，认证材料在运行时
// 从 ~/.skating/secrets.json resolve 出来交给 executor，全程不落日志。

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

func TestRun_SshStepPasswordAuth(t *testing.T) {
	home := setupE2E(t)
	addr := testssh.Start(t, testssh.PasswordConfig("deployer", "testpass"))
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

	projDir := filepath.Join(home, "sshproj")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)
	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	yamlContent := fmt.Sprintf(`name: sshproj
pipeline:
  stages:
    - name: deploy
      steps:
        - name: remote-echo
          type: ssh
          host: %s
          port: %d
          user: deployer
          auth: password
          secret: deploy-pass
          script: |
            echo "::set-var R=ok"
            echo "REPLY=done"
        - name: after-ssh
          type: shell
          when: "R == ok"
          script: |
            echo "AFTER-RAN"
`, host, port)
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "sshproj")
	if strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("run failed: %q", stderr)
	}
	combined := stdout + stderr

	// 1. ssh step 输出流式进入日志
	if !strings.Contains(combined, "REPLY=done") {
		t.Errorf("ssh output missing REPLY=done: %q", combined)
	}
	// 2. ssh step 内 ::set-var 写入变量池，后续 shell step 的 When 命中
	if !strings.Contains(combined, "AFTER-RAN") {
		t.Errorf("::set-var from ssh step not visible to next step: %q", combined)
	}
	// 3. 认证材料不落日志
	if strings.Contains(combined, "testpass") {
		t.Errorf("secret value leaked into logs: %q", combined)
	}
}

func TestRun_SshStepSecretNotFound(t *testing.T) {
	home := setupE2E(t)
	// 不注册任何 secret
	projDir := filepath.Join(home, "sshmiss")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)
	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	yamlContent := `name: sshmiss
pipeline:
  stages:
    - name: deploy
      steps:
        - name: remote
          type: ssh
          host: 127.0.0.1
          user: deployer
          secret: no-such-secret
          script: |
            echo hi
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	_, stderr := executeCmd(t, "run", "sshmiss")
	if !strings.Contains(stderr, "no-such-secret") || !strings.Contains(stderr, "resolve secret") {
		t.Errorf("expected resolve secret error mentioning name, got %q", stderr)
	}
}
