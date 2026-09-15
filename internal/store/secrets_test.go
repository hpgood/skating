package store

import (
	"errors"
	"os"
	"testing"
)

func TestStore_SetAndGetSecret(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetSecret("ssh-deploy", "p@ssw0rd"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	got, err := s.GetSecret("ssh-deploy")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got != "p@ssw0rd" {
		t.Errorf("value = %q, want %q", got, "p@ssw0rd")
	}
}

func TestStore_SetSecret_Overwrite(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetSecret("k", "v1"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if err := s.SetSecret("k", "v2"); err != nil {
		t.Fatalf("SetSecret overwrite: %v", err)
	}
	list, err := s.ListSecrets()
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	got, _ := s.GetSecret("k")
	if got != "v2" {
		t.Errorf("value = %q, want v2 (overwritten)", got)
	}
}

func TestStore_GetSecret_NotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetSecret("missing")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("err = %v, want ErrSecretNotFound", err)
	}
}

func TestStore_RemoveSecret(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetSecret("k", "v"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if err := s.RemoveSecret("k"); err != nil {
		t.Fatalf("RemoveSecret: %v", err)
	}
	if _, err := s.GetSecret("k"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("after remove: err = %v, want ErrSecretNotFound", err)
	}
	if err := s.RemoveSecret("k"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("remove missing: err = %v, want ErrSecretNotFound", err)
	}
}

func TestStore_SetSecret_EmptyName(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetSecret("", "v"); err == nil {
		t.Fatal("empty name should fail")
	}
}

func TestStore_SecretsFilePermission0600(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetSecret("k", "v"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	info, err := os.Stat(s.secretsPath())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Windows 上 chmod 不生效（仅只读位），只在 Unix 平台断言权限
	if info.Mode().Perm() != 0600 && os.PathSeparator == '/' {
		t.Errorf("perm = %o, want 600", info.Mode().Perm())
	}
}
