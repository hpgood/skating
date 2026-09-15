package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrSecretNotFound secret 名不存在
var ErrSecretNotFound = errors.New("secret not found")

// SecretEntry 单条 secret 记录（value 明文存储，文件权限 0600 保护）
type SecretEntry struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	CreatedAt string `json:"createdat"`
	UpdatedAt string `json:"updatedat"`
}

// secretsFile 是 secrets.json 的顶层结构
type secretsFile struct {
	Secrets []SecretEntry `json:"secrets"`
}

func (s *Store) secretsPath() string {
	return filepath.Join(s.baseDir, "secrets.json")
}

// loadSecrets 读取全部 secret；文件不存在时返回空集合
func (s *Store) loadSecrets() (*secretsFile, error) {
	var sf secretsFile
	data, err := os.ReadFile(s.secretsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &sf, nil
		}
		return nil, fmt.Errorf("read secrets: %w", err)
	}
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse secrets: %w", err)
	}
	return &sf, nil
}

// saveSecrets 写入 secrets.json 并收紧权限（0600；Windows 上 chmod 无实际效果）
func (s *Store) saveSecrets(sf *secretsFile) error {
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("encode secrets: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.secretsPath()), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	if err := os.WriteFile(s.secretsPath(), data, 0600); err != nil {
		return fmt.Errorf("write secrets: %w", err)
	}
	return nil
}

// SetSecret 创建或覆盖一条 secret
func (s *Store) SetSecret(name, value string) error {
	if name == "" {
		return fmt.Errorf("secret name must not be empty")
	}
	sf, err := s.loadSecrets()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range sf.Secrets {
		if sf.Secrets[i].Name == name {
			sf.Secrets[i].Value = value
			sf.Secrets[i].UpdatedAt = now
			return s.saveSecrets(sf)
		}
	}
	sf.Secrets = append(sf.Secrets, SecretEntry{
		Name:      name,
		Value:     value,
		CreatedAt: now,
		UpdatedAt: now,
	})
	return s.saveSecrets(sf)
}

// GetSecret 按名读取 secret 值；不存在时返回包装的 ErrSecretNotFound
func (s *Store) GetSecret(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("secret name must not be empty")
	}
	sf, err := s.loadSecrets()
	if err != nil {
		return "", err
	}
	for _, e := range sf.Secrets {
		if e.Name == name {
			return e.Value, nil
		}
	}
	return "", fmt.Errorf("secret %q: %w", name, ErrSecretNotFound)
}

// ListSecrets 返回全部 secret 的名字与元信息（不含 value）
func (s *Store) ListSecrets() ([]SecretEntry, error) {
	sf, err := s.loadSecrets()
	if err != nil {
		return nil, err
	}
	return sf.Secrets, nil
}

// RemoveSecret 删除一条 secret；不存在时返回包装的 ErrSecretNotFound
func (s *Store) RemoveSecret(name string) error {
	sf, err := s.loadSecrets()
	if err != nil {
		return err
	}
	for i, e := range sf.Secrets {
		if e.Name == name {
			sf.Secrets = append(sf.Secrets[:i], sf.Secrets[i+1:]...)
			return s.saveSecrets(sf)
		}
	}
	return fmt.Errorf("secret %q: %w", name, ErrSecretNotFound)
}
