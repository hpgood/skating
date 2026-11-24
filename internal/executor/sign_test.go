package executor

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
)

// newSignKeyPair 生成 Ed25519 密钥对（PKCS#8 私钥 PEM + PKIX 公钥 PEM），
// 签名测试关注文件/路径/校验逻辑，不依赖具体算法强度。
func newSignKeyPair(t *testing.T) (privPEM, pubPEM []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
}

func writeSignFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRunner_Sign_DefaultOutput(t *testing.T) {
	privPEM, _ := newSignKeyPair(t)
	workDir := t.TempDir()
	artifact := filepath.Join(workDir, "build", "app.bin")
	writeSignFile(t, artifact, []byte("payload"))

	r := NewRunner()
	step := Step{Name: "sign-artifact", Type: "sign", Secret: string(privPEM), File: "build/app.bin"}
	out, err := r.Execute(step, workDir, nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "[sign]") || !strings.Contains(out, "ed25519") {
		t.Errorf("summary = %q, want '[sign]' and algorithm", out)
	}

	sigPath := artifact + ".sig"
	data, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatalf("read signature: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Errorf("signature file should end with newline, got %q", data)
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data))); err != nil {
		t.Errorf("signature is not valid base64: %v", err)
	}
}

func TestRunner_Sign_CustomOutputExpandsVars(t *testing.T) {
	privPEM, _ := newSignKeyPair(t)
	workDir := t.TempDir()
	writeSignFile(t, filepath.Join(workDir, "dist", "v1", "app.bin"), []byte("payload"))

	r := NewRunner()
	step := Step{
		Name:      "sign-artifact",
		Type:      "sign",
		Secret:    string(privPEM),
		File:      "dist/${TAG}/app.bin",
		Output:    "sigs/${TAG}/app.sig",
		Algorithm: "",
	}
	env := map[string]string{"TAG": "v1"}
	if _, err := r.Execute(step, workDir, env, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sigPath := filepath.Join(workDir, "sigs", "v1", "app.sig")
	if _, err := os.Stat(sigPath); err != nil {
		t.Errorf("custom signature not written (parent dirs should be created): %v", err)
	}
}

func TestRunner_Verify_SuccessAndTamper(t *testing.T) {
	privPEM, pubPEM := newSignKeyPair(t)
	workDir := t.TempDir()
	artifact := filepath.Join(workDir, "build", "app.bin")
	writeSignFile(t, artifact, []byte("payload"))
	writeSignFile(t, filepath.Join(workDir, "keys", "release.pub"), pubPEM)

	r := NewRunner()
	signStep := Step{Name: "sign", Type: "sign", Secret: string(privPEM), File: "build/app.bin"}
	if _, err := r.Execute(signStep, workDir, nil, nil); err != nil {
		t.Fatalf("sign: %v", err)
	}

	verifyStep := Step{Name: "verify", Type: "verify", File: "build/app.bin", PublicKey: "keys/release.pub"}
	out, err := r.Execute(verifyStep, workDir, nil, nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(out, "[verify]") || !strings.Contains(out, "OK") {
		t.Errorf("summary = %q, want '[verify] ... OK'", out)
	}

	// 篡改产物后验签必须失败
	writeSignFile(t, artifact, []byte("tampered"))
	if _, err := r.Execute(verifyStep, workDir, nil, nil); err == nil {
		t.Fatal("verify should fail on tampered artifact")
	} else if !strings.Contains(err.Error(), "signature mismatch") {
		t.Errorf("error = %v, want signature mismatch", err)
	}
}

func TestRunner_Verify_PublicKeyFromSecret(t *testing.T) {
	privPEM, pubPEM := newSignKeyPair(t)
	workDir := t.TempDir()
	writeSignFile(t, filepath.Join(workDir, "app.bin"), []byte("payload"))

	r := NewRunner()
	if _, err := r.Execute(Step{Name: "sign", Type: "sign", Secret: string(privPEM), File: "app.bin"}, workDir, nil, nil); err != nil {
		t.Fatalf("sign: %v", err)
	}
	step := Step{Name: "verify", Type: "verify", File: "app.bin", Secret: string(pubPEM)}
	if _, err := r.Execute(step, workDir, nil, nil); err != nil {
		t.Fatalf("verify with public key from secret: %v", err)
	}
}

func TestRunner_Verify_KeySourceValidation(t *testing.T) {
	privPEM, pubPEM := newSignKeyPair(t)
	workDir := t.TempDir()
	writeSignFile(t, filepath.Join(workDir, "app.bin"), []byte("payload"))

	r := NewRunner()
	if _, err := r.Execute(Step{Name: "sign", Type: "sign", Secret: string(privPEM), File: "app.bin"}, workDir, nil, nil); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// 两者都空
	if _, err := r.Execute(Step{Name: "v", Type: "verify", File: "app.bin"}, workDir, nil, nil); err == nil {
		t.Error("verify with neither public_key nor secret should fail")
	} else if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error = %v, want 'exactly one'", err)
	}

	// 两者都填
	both := Step{Name: "v", Type: "verify", File: "app.bin", Secret: string(pubPEM), PublicKey: "app.bin"}
	if _, err := r.Execute(both, workDir, nil, nil); err == nil {
		t.Error("verify with both public_key and secret should fail")
	} else if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error = %v, want 'exactly one'", err)
	}
}

func TestRunner_Sign_MissingInputs(t *testing.T) {
	privPEM, _ := newSignKeyPair(t)
	workDir := t.TempDir()
	writeSignFile(t, filepath.Join(workDir, "app.bin"), []byte("payload"))
	r := NewRunner()

	if _, err := r.Execute(Step{Name: "s", Type: "sign", Secret: string(privPEM)}, workDir, nil, nil); err == nil {
		t.Error("sign without file should fail")
	} else if !strings.Contains(err.Error(), "sign file is required") {
		t.Errorf("error = %v, want 'sign file is required'", err)
	}

	if _, err := r.Execute(Step{Name: "s", Type: "sign", File: "app.bin"}, workDir, nil, nil); err == nil {
		t.Error("sign without secret should fail")
	} else if !strings.Contains(err.Error(), "sign secret is required") {
		t.Errorf("error = %v, want 'sign secret is required'", err)
	}
}

func TestRunner_Sign_DoesNotLeakKeyMaterial(t *testing.T) {
	workDir := t.TempDir()
	writeSignFile(t, filepath.Join(workDir, "app.bin"), []byte("payload"))
	const marker = "LEAKED-KEY-MATERIAL"
	badPEM := "-----BEGIN PRIVATE KEY-----\n" + marker + "\n-----END PRIVATE KEY-----\n"

	r := NewRunner()
	_, err := r.Execute(Step{Name: "s", Type: "sign", File: "app.bin", Secret: badPEM}, workDir, nil, nil)
	if err == nil {
		t.Fatal("sign with invalid key should fail")
	}
	if strings.Contains(err.Error(), marker) {
		t.Errorf("secret/key material leaked into error: %v", err)
	}
}
