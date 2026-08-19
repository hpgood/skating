package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunner_SaveArtifact_ToHostBucket(t *testing.T) {
	r := NewRunner() // dockerImage 空 → bucket = "host"

	// 准备源文件
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "myartifact.bin")
	if err := os.WriteFile(srcPath, []byte("binary content"), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// 重定向 home 到 t.TempDir，让 saveArtifact 写到隔离目录
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	// 重新调用 SaveLog 不会受影响；我们要的是 saveArtifact 用 homeDir

	dest, err := r.saveArtifact(srcPath)
	if err != nil {
		t.Fatalf("saveArtifact: %v", err)
	}

	expected := filepath.Join(tmpHome, ".skating", "artifacts", "host", "myartifact.bin")
	if dest != expected {
		t.Errorf("dest path = %q, want %q", dest, expected)
	}

	// 文件内容应一致
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != "binary content" {
		t.Errorf("content = %q, want 'binary content'", got)
	}
}

func TestRunner_SaveArtifact_BucketedByDockerImage(t *testing.T) {
	r := NewRunner()
	r.SetDockerImage("golang:1.25")

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "out")
	os.WriteFile(srcPath, []byte("ok"), 0644)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dest, err := r.saveArtifact(srcPath)
	if err != nil {
		t.Fatalf("saveArtifact: %v", err)
	}

	// docker 镜像名 "golang:1.25" → sanitize 后 "golang_1.25"
	expected := filepath.Join(tmpHome, ".skating", "artifacts", "golang_1.25", "out")
	if dest != expected {
		t.Errorf("dest = %q, want %q", dest, expected)
	}
}

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"golang:1.25", "golang_1.25"},
		{"my/image", "my_image"},
		{"weird:name/path", "weird_name_path"},
		{"normal", "normal"},
		{"with space", "with_space"},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got := sanitizePath(tc.in)
			if got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRunner_SaveArtifact_MissingSource(t *testing.T) {
	r := NewRunner()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	_, err := r.saveArtifact("/nonexistent/file")
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}