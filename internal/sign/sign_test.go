package sign

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// pemEncode 把 DER 编码成 PEM 块
func pemEncode(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

// pkcs8PEM 把任意私钥编码成 PKCS#8 PEM
func pkcs8PEM(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	return pemEncode("PRIVATE KEY", der)
}

// pkixPEM 把公钥编码成 PKIX PEM
func pkixPEM(t *testing.T, pub any) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal pkix: %v", err)
	}
	return pemEncode("PUBLIC KEY", der)
}

// opensshPrivatePEM 把私钥编码成 OpenSSH 私钥格式
func opensshPrivatePEM(t *testing.T, key any) []byte {
	t.Helper()
	block, err := ssh.MarshalPrivateKey(key, "skating-test")
	if err != nil {
		t.Fatalf("marshal openssh private key: %v", err)
	}
	return pem.EncodeToMemory(block)
}

// opensshPublicPEM 把公钥编码成 OpenSSH authorized_keys 格式
func opensshPublicPEM(t *testing.T, pub any) []byte {
	t.Helper()
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("new ssh public key: %v", err)
	}
	return ssh.MarshalAuthorizedKey(sshPub)
}

func writeArtifact(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.bin")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	return path
}

// runRoundTrip 对每一组私钥/公钥编码做签名 → 验签往返，并验证篡改文件后失败。
func runRoundTrip(t *testing.T, wantAlg string, privVariants, pubVariants map[string][]byte) {
	t.Helper()
	content := []byte("artifact-payload-\x00\x01\x02")
	path := writeArtifact(t, content)

	for privName, priv := range privVariants {
		for pubName, pub := range pubVariants {
			t.Run(privName+"/"+pubName, func(t *testing.T) {
				sig, resolved, err := SignFile(path, priv, "")
				if err != nil {
					t.Fatalf("SignFile: %v", err)
				}
				if resolved != wantAlg {
					t.Errorf("resolved algorithm = %q, want %q", resolved, wantAlg)
				}
				if _, err := base64.StdEncoding.DecodeString(sig); err != nil {
					t.Errorf("signature is not standard base64: %v", err)
				}

				gotAlg, err := VerifyFile(path, sig, pub, "")
				if err != nil {
					t.Fatalf("VerifyFile: %v", err)
				}
				if gotAlg != wantAlg {
					t.Errorf("verified algorithm = %q, want %q", gotAlg, wantAlg)
				}

				// 篡改文件内容后必须验签失败
				badPath := writeArtifact(t, append(append([]byte{}, content...), 'x'))
				if _, err := VerifyFile(badPath, sig, pub, ""); err == nil {
					t.Error("VerifyFile succeeded on tampered file, want mismatch")
				}
			})
		}
	}
}

func TestSignVerifyRSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa: %v", err)
	}
	privVariants := map[string][]byte{
		"pkcs1":   pemEncode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key)),
		"pkcs8":   pkcs8PEM(t, key),
		"openssh": opensshPrivatePEM(t, key),
	}
	pubVariants := map[string][]byte{
		"pkix":    pkixPEM(t, &key.PublicKey),
		"pkcs1":   pemEncode("RSA PUBLIC KEY", x509.MarshalPKCS1PublicKey(&key.PublicKey)),
		"openssh": opensshPublicPEM(t, &key.PublicKey),
	}
	runRoundTrip(t, AlgorithmRSASHA256, privVariants, pubVariants)
}

func TestSignVerifyECDSA(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa: %v", err)
	}
	sec1, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal sec1: %v", err)
	}
	privVariants := map[string][]byte{
		"sec1":    pemEncode("EC PRIVATE KEY", sec1),
		"pkcs8":   pkcs8PEM(t, key),
		"openssh": opensshPrivatePEM(t, key),
	}
	pubVariants := map[string][]byte{
		"pkix":    pkixPEM(t, &key.PublicKey),
		"openssh": opensshPublicPEM(t, &key.PublicKey),
	}
	runRoundTrip(t, AlgorithmECDSASHA256, privVariants, pubVariants)
}

func TestSignVerifyEd25519(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	privVariants := map[string][]byte{
		"pkcs8":   pkcs8PEM(t, priv),
		"openssh": opensshPrivatePEM(t, priv),
	}
	pubVariants := map[string][]byte{
		"pkix":    pkixPEM(t, pub),
		"openssh": opensshPublicPEM(t, pub),
	}
	runRoundTrip(t, AlgorithmEd25519, privVariants, pubVariants)
}

func TestSignFileExplicitAlgorithm(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa: %v", err)
	}
	priv := pkcs8PEM(t, key)
	pub := pkixPEM(t, &key.PublicKey)
	path := writeArtifact(t, []byte("payload"))

	for _, alg := range []string{"rsa-sha256", "RSA-SHA256", " Rsa-Sha256 "} {
		sig, resolved, err := SignFile(path, priv, alg)
		if err != nil {
			t.Fatalf("SignFile(%q): %v", alg, err)
		}
		if resolved != AlgorithmRSASHA256 {
			t.Errorf("SignFile(%q) resolved = %q, want %q", alg, resolved, AlgorithmRSASHA256)
		}
		if _, err := VerifyFile(path, sig, pub, alg); err != nil {
			t.Errorf("VerifyFile(%q): %v", alg, err)
		}
	}
}

func TestSignFileAlgorithmMismatch(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa: %v", err)
	}
	path := writeArtifact(t, []byte("payload"))
	_, _, err = SignFile(path, pkcs8PEM(t, key), AlgorithmECDSASHA256)
	if err == nil {
		t.Fatal("expected algorithm mismatch error")
	}
	if !strings.Contains(err.Error(), "does not match key type") {
		t.Errorf("error = %q, want 'does not match key type'", err)
	}
}

func TestSignFileUnsupportedAlgorithm(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa: %v", err)
	}
	path := writeArtifact(t, []byte("payload"))
	_, _, err = SignFile(path, pkcs8PEM(t, key), "md5")
	if err == nil {
		t.Fatal("expected unsupported algorithm error")
	}
	if !strings.Contains(err.Error(), "unsupported algorithm") {
		t.Errorf("error = %q, want 'unsupported algorithm'", err)
	}
}

func TestVerifyFileMismatch(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa: %v", err)
	}
	priv := pkcs8PEM(t, key)
	pub := pkixPEM(t, &key.PublicKey)
	path := writeArtifact(t, []byte("payload"))

	sig, _, err := SignFile(path, priv, "")
	if err != nil {
		t.Fatalf("SignFile: %v", err)
	}

	// 篡改签名
	raw, _ := base64.StdEncoding.DecodeString(sig)
	raw[len(raw)-1] ^= 0xff
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := VerifyFile(path, tampered, pub, ""); err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Errorf("VerifyFile(tampered sig) error = %v, want signature mismatch", err)
	}

	// 错误的公钥
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate other ecdsa: %v", err)
	}
	if _, err := VerifyFile(path, sig, pkixPEM(t, &other.PublicKey), ""); err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Errorf("VerifyFile(wrong pub) error = %v, want signature mismatch", err)
	}
}

func TestVerifyFileBase64Variants(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	path := writeArtifact(t, []byte("payload"))
	priv := pkcs8PEM(t, privKey)
	pub := pkixPEM(t, pubKey)

	sig, _, err := SignFile(path, priv, "")
	if err != nil {
		t.Fatalf("SignFile: %v", err)
	}

	variants := map[string]string{
		"with padding":    sig,
		"without padding": strings.TrimRight(sig, "="),
		"surrounding ws":  "\n\t " + sig + "\r\n",
	}
	for name, variant := range variants {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyFile(path, variant, pub, ""); err != nil {
				t.Errorf("VerifyFile: %v", err)
			}
		})
	}

	if _, err := VerifyFile(path, "!!!not-base64!!!", pub, ""); err == nil || !strings.Contains(err.Error(), "not valid base64") {
		t.Errorf("VerifyFile(invalid base64) error = %v, want 'not valid base64'", err)
	}
}

func TestEncryptedPrivateKeyRejected(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(key, "skating-test", []byte("secret-pass"))
	if err != nil {
		t.Fatalf("marshal encrypted key: %v", err)
	}
	encrypted := pem.EncodeToMemory(block)

	path := writeArtifact(t, []byte("payload"))
	_, _, err = SignFile(path, encrypted, "")
	if err == nil {
		t.Fatal("expected encrypted key error")
	}
	if !strings.Contains(err.Error(), "encrypted private keys are not supported") {
		t.Errorf("error = %q, want encrypted-key message", err)
	}
}

func TestInvalidKeyMaterial(t *testing.T) {
	path := writeArtifact(t, []byte("payload"))

	if _, _, err := SignFile(path, []byte("not a key"), ""); err == nil {
		t.Error("SignFile(garbage private key) should fail")
	} else if strings.Contains(err.Error(), "not a key") {
		t.Errorf("private key material leaked into error: %q", err)
	}

	if _, err := VerifyFile(path, "AAAA", []byte("not a key"), ""); err == nil {
		t.Error("VerifyFile(garbage public key) should fail")
	} else if strings.Contains(err.Error(), "not a key") {
		t.Errorf("public key material leaked into error: %q", err)
	}
}

func TestSignFileMissingFile(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "nope.bin")
	if _, _, err := SignFile(missing, pkcs8PEM(t, key), ""); err == nil {
		t.Fatal("expected missing-file error")
	}
}
