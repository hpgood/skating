// Package sign 提供基于文件内容的 detached 数字签名与验签。
//
// 支持的算法与密钥类型：
//   - RSA：PKCS#1 v1.5 + SHA-256（rsa-sha256）
//   - ECDSA：SHA-256 + ASN.1 DER 签名（ecdsa-sha256）
//   - Ed25519：纯 Ed25519（ed25519）
//
// 私钥支持 x509 PEM（PKCS#8 / PKCS#1 / SEC1）与 OpenSSH 私钥；公钥支持
// PKIX / PKCS#1 PEM 与 OpenSSH authorized_keys 格式。
//
// 该包刻意不把密钥、签名原文拼进错误信息：上层会把 secret 的值写进日志，
// 回显材料等同于泄漏。
package sign

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// 支持的算法名（yaml 的 algorithm 字段，大小写不敏感）
const (
	AlgorithmRSASHA256   = "rsa-sha256"
	AlgorithmECDSASHA256 = "ecdsa-sha256"
	AlgorithmEd25519     = "ed25519"
)

var (
	// errEncryptedKey 加密私钥的统一提示：先解密再交给 skating
	errEncryptedKey = errors.New("encrypted private keys are not supported; decrypt the key first (e.g. openssl pkey -in key.pem -out plain.pem)")
	// errSignatureMismatch 验签失败统一返回该错误，不区分内部细节
	errSignatureMismatch = errors.New("signature mismatch")
)

// SignFile 读取 path 指向的文件内容，用 privateKeyPEM 生成 detached 签名。
// 返回 base64（StdEncoding，带 padding）编码的签名与最终算法。
// algorithm 为空时按私钥类型自动识别；显式指定时必须与私钥类型一致。
func SignFile(path string, privateKeyPEM []byte, algorithm string) (sigB64, resolved string, err error) {
	key, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return "", "", err
	}

	resolved, err = resolveAlgorithm(algorithm, privateKeyAlgorithm(key))
	if err != nil {
		return "", "", err
	}

	var sig []byte
	switch k := key.(type) {
	case *rsa.PrivateKey:
		digest, err := hashFile(path)
		if err != nil {
			return "", "", err
		}
		sig, err = rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, digest)
		if err != nil {
			return "", "", fmt.Errorf("rsa sign: %w", err)
		}
	case *ecdsa.PrivateKey:
		digest, err := hashFile(path)
		if err != nil {
			return "", "", err
		}
		sig, err = ecdsa.SignASN1(rand.Reader, k, digest)
		if err != nil {
			return "", "", fmt.Errorf("ecdsa sign: %w", err)
		}
	case ed25519.PrivateKey:
		// Ed25519 必须对完整消息签名，无法流式处理；大文件优先选 RSA/ECDSA
		data, err := os.ReadFile(path)
		if err != nil {
			return "", "", fmt.Errorf("read %q: %w", path, err)
		}
		sig = ed25519.Sign(k, data)
	default:
		return "", "", errors.New("unsupported private key type (want RSA, ECDSA or Ed25519)")
	}

	return base64.StdEncoding.EncodeToString(sig), resolved, nil
}

// VerifyFile 读取 path 指向的文件内容，用 publicKeyPEM 验签 signatureB64。
// 返回最终算法；验签失败时返回 signature mismatch。
// algorithm 为空时按公钥类型自动识别；显式指定时必须与公钥类型一致。
func VerifyFile(path, signatureB64 string, publicKeyPEM []byte, algorithm string) (resolved string, err error) {
	key, err := parsePublicKey(publicKeyPEM)
	if err != nil {
		return "", err
	}

	resolved, err = resolveAlgorithm(algorithm, publicKeyAlgorithm(key))
	if err != nil {
		return "", err
	}

	sig, err := decodeSignature(signatureB64)
	if err != nil {
		return "", err
	}

	switch k := key.(type) {
	case *rsa.PublicKey:
		digest, err := hashFile(path)
		if err != nil {
			return "", err
		}
		if err := rsa.VerifyPKCS1v15(k, crypto.SHA256, digest, sig); err != nil {
			return "", errSignatureMismatch
		}
	case *ecdsa.PublicKey:
		digest, err := hashFile(path)
		if err != nil {
			return "", err
		}
		if !ecdsa.VerifyASN1(k, digest, sig) {
			return "", errSignatureMismatch
		}
	case ed25519.PublicKey:
		// 与签名一致：Ed25519 需要完整消息内容
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %q: %w", path, err)
		}
		if !ed25519.Verify(k, data, sig) {
			return "", errSignatureMismatch
		}
	default:
		return "", errors.New("unsupported public key type (want RSA, ECDSA or Ed25519)")
	}

	return resolved, nil
}

// parsePrivateKey 解析 PEM/OpenSSH 私钥，返回归一化后的具体密钥类型。
// 解析顺序与 OpenSSL 常见输出保持一致：PKCS#8 → PKCS#1 → SEC1，
// 都失败后再尝试 OpenSSH 私钥格式。
func parsePrivateKey(pemBytes []byte) (any, error) {
	if block, _ := pem.Decode(pemBytes); block != nil {
		if isEncryptedPEMBlock(block) {
			return nil, errEncryptedKey
		}
		if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			return normalizePrivateKey(k)
		}
		if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			return normalizePrivateKey(k)
		}
		if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
			return normalizePrivateKey(k)
		}
	}

	// OpenSSH 私钥（BEGIN OPENSSH PRIVATE KEY）以及少数未走 x509 的格式
	k, err := ssh.ParseRawPrivateKey(pemBytes)
	if err != nil {
		if strings.Contains(err.Error(), "passphrase") {
			return nil, errEncryptedKey
		}
		return nil, fmt.Errorf("unsupported private key format (want PEM PKCS#8/PKCS#1/SEC1 or OpenSSH): %w", err)
	}
	return normalizePrivateKey(k)
}

// parsePublicKey 解析 PEM 或 OpenSSH authorized_keys 公钥。
func parsePublicKey(pemBytes []byte) (any, error) {
	if block, _ := pem.Decode(pemBytes); block != nil {
		if k, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
			return normalizePublicKey(k)
		}
		if k, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
			return normalizePublicKey(k)
		}
	}

	pub, _, _, _, err := ssh.ParseAuthorizedKey(pemBytes)
	if err != nil {
		return nil, errors.New("unsupported public key format (want PEM PKIX/PKCS#1 or OpenSSH authorized key)")
	}
	cpk, ok := pub.(ssh.CryptoPublicKey)
	if !ok {
		return nil, fmt.Errorf("unsupported public key type %q (want RSA, ECDSA or Ed25519)", pub.Type())
	}
	return normalizePublicKey(cpk.CryptoPublicKey())
}

// isEncryptedPEMBlock 识别传统 PEM（Proc-Type: 4,ENCRYPTED）与 PKCS#8 加密块。
func isEncryptedPEMBlock(block *pem.Block) bool {
	if block.Type == "ENCRYPTED PRIVATE KEY" {
		return true
	}
	return strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED")
}

// normalizePrivateKey 把 x509/ssh 返回的私钥收敛到本包处理的三种类型。
func normalizePrivateKey(key any) (any, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		if k != nil {
			return k, nil
		}
	case *ecdsa.PrivateKey:
		if k != nil {
			return k, nil
		}
	case ed25519.PrivateKey:
		if len(k) == ed25519.PrivateKeySize {
			return k, nil
		}
	case *ed25519.PrivateKey:
		if k != nil && len(*k) == ed25519.PrivateKeySize {
			return *k, nil
		}
	}
	return nil, errors.New("unsupported private key type (want RSA, ECDSA or Ed25519)")
}

// normalizePublicKey 把 x509/ssh 返回的公钥收敛到本包处理的三种类型。
func normalizePublicKey(key any) (any, error) {
	switch k := key.(type) {
	case *rsa.PublicKey:
		if k != nil {
			return k, nil
		}
	case *ecdsa.PublicKey:
		if k != nil {
			return k, nil
		}
	case ed25519.PublicKey:
		if len(k) == ed25519.PublicKeySize {
			return k, nil
		}
	}
	return nil, errors.New("unsupported public key type (want RSA, ECDSA or Ed25519)")
}

// privateKeyAlgorithm 返回私钥对应的算法名；未知类型返回空串。
func privateKeyAlgorithm(key any) string {
	switch key.(type) {
	case *rsa.PrivateKey:
		return AlgorithmRSASHA256
	case *ecdsa.PrivateKey:
		return AlgorithmECDSASHA256
	case ed25519.PrivateKey:
		return AlgorithmEd25519
	}
	return ""
}

// publicKeyAlgorithm 返回公钥对应的算法名；未知类型返回空串。
func publicKeyAlgorithm(key any) string {
	switch key.(type) {
	case *rsa.PublicKey:
		return AlgorithmRSASHA256
	case *ecdsa.PublicKey:
		return AlgorithmECDSASHA256
	case ed25519.PublicKey:
		return AlgorithmEd25519
	}
	return ""
}

// resolveAlgorithm 合并显式 algorithm 与密钥类型推导出的算法。
// 显式值非法或不匹配时返回明确错误（只描述算法名，不涉及密钥内容）。
func resolveAlgorithm(explicit, keyAlgorithm string) (string, error) {
	if keyAlgorithm == "" {
		return "", errors.New("unsupported key type (want RSA, ECDSA or Ed25519)")
	}
	norm, err := normalizeAlgorithm(explicit)
	if err != nil {
		return "", err
	}
	if norm == "" {
		return keyAlgorithm, nil
	}
	if norm != keyAlgorithm {
		return "", fmt.Errorf("algorithm %q does not match key type (key is %s)", norm, keyAlgorithm)
	}
	return norm, nil
}

// normalizeAlgorithm 校验并归一化 algorithm 字段（大小写不敏感）。
// 空串返回空串与 nil，由调用方按密钥类型推导。
func normalizeAlgorithm(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "":
		return "", nil
	case AlgorithmRSASHA256, AlgorithmECDSASHA256, AlgorithmEd25519:
		return s, nil
	default:
		return "", fmt.Errorf("unsupported algorithm %q (want %s, %s or %s)",
			s, AlgorithmRSASHA256, AlgorithmECDSASHA256, AlgorithmEd25519)
	}
}

// hashFile 流式计算文件 SHA-256，避免大文件整体读入内存。
func hashFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	return h.Sum(nil), nil
}

// decodeSignature 解码 base64 签名：先按带 padding 解析，失败再按无 padding
// 解析（兼容 openssl base64 -A 以及手工去掉 '=' 的输出）；首尾空白忽略。
func decodeSignature(b64 string) ([]byte, error) {
	s := strings.TrimSpace(b64)
	if s == "" {
		return nil, errors.New("signature is empty")
	}
	if sig, err := base64.StdEncoding.DecodeString(s); err == nil {
		return sig, nil
	}
	if sig, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return sig, nil
	}
	return nil, errors.New("signature is not valid base64")
}
