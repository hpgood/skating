package main

// sign/verify 步骤端到端测试：私钥经 skating secret 保存，yaml 只引用名字；
// sign 生成 detached .sig，verify 用项目内的 public_key 文件验签。
// 全程断言私钥内容不进入 stdout/stderr。

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hpgood/skating/internal/store"
)

func TestRun_SignAndVerifySteps(t *testing.T) {
	home := setupE2E(t)

	projDir := filepath.Join(home, "signproj")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)
	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	// 项目内生成 Ed25519 密钥对：私钥进 secret 存储，公钥落盘供 verify 使用
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	keysDir := filepath.Join(projDir, "keys")
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		t.Fatalf("mkdir keys: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, "release.pub"), pubPEM, 0644); err != nil {
		t.Fatalf("write public key: %v", err)
	}

	s, err := store.NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := s.SetSecret("release-signing-key", string(privPEM)); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	artifact := filepath.Join(projDir, "dist", "app.bin")
	if err := os.MkdirAll(filepath.Dir(artifact), 0755); err != nil {
		t.Fatalf("mkdir artifact: %v", err)
	}
	if err := os.WriteFile(artifact, []byte("release-artifact-payload"), 0644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	yamlContent := `name: signproj
pipeline:
  stages:
    - name: sign
      steps:
        - name: sign-artifact
          type: sign
          secret: release-signing-key
          file: dist/app.bin
        - name: verify-artifact
          type: verify
          file: dist/app.bin
          public_key: keys/release.pub
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "signproj")
	if strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("run failed: %q", stderr)
	}
	combined := stdout + stderr

	// 1. .sig 生成且为合法 base64（单行 + 末尾换行）
	sigPath := artifact + ".sig"
	sigBytes, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatalf("signature not created: %v\noutput: %s", err, combined)
	}
	if !strings.HasSuffix(string(sigBytes), "\n") {
		t.Errorf("signature should end with newline: %q", sigBytes)
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigBytes))); err != nil {
		t.Errorf("signature is not valid base64: %v", err)
	}

	// 2. 摘要进入日志
	if !strings.Contains(combined, "[sign]") {
		t.Errorf("sign summary missing: %q", combined)
	}
	if !strings.Contains(combined, "[verify]") || !strings.Contains(combined, "OK") {
		t.Errorf("verify summary missing: %q", combined)
	}

	// 3. 私钥内容不落日志（取 PEM 第一行正文做精确比对）
	var keyLine string
	for _, line := range strings.Split(string(privPEM), "\n") {
		if line != "" && !strings.HasPrefix(line, "-----") {
			keyLine = line
			break
		}
	}
	if keyLine == "" {
		t.Fatal("failed to extract private key body line")
	}
	if strings.Contains(combined, keyLine) {
		t.Errorf("private key material leaked into logs: %q", combined)
	}

	// 4. 篡改产物后只跑 verify：必须失败且构建状态为 failure
	if err := os.WriteFile(artifact, []byte("tampered-payload"), 0644); err != nil {
		t.Fatalf("tamper artifact: %v", err)
	}
	stdout, stderr = executeCmd(t, "run", "signproj", "--step", "sign/verify-artifact")
	combined = stdout + stderr
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("verify on tampered artifact should fail, got stderr=%q", stderr)
	}
	if strings.Contains(combined, "=== Build SUCCESS ===") {
		t.Errorf("tampered verification must not report Build SUCCESS: %q", combined)
	}
	if !strings.Contains(combined, "Build FAILED") {
		t.Errorf("expected 'Build FAILED' marker: %q", combined)
	}
	if !strings.Contains(combined, "signature mismatch") {
		t.Errorf("expected 'signature mismatch' in error: %q", combined)
	}
}
