package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPipeline_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `name: demo
image: golang:1.21
pipeline:
  stages:
    - name: build
      parallel: false
      steps:
        - name: compile
          type: shell
          script: |
            echo "building"
`
	if err := os.WriteFile(filepath.Join(dir, ".skating.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := LoadPipeline(filepath.Join(dir, ".skating.yaml"))
	if err != nil {
		t.Fatalf("LoadPipeline: %v", err)
	}
	if len(p.Stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(p.Stages))
	}
	if p.Stages[0].Name != "build" {
		t.Errorf("stage name = %q, want build", p.Stages[0].Name)
	}
	if len(p.Stages[0].Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(p.Stages[0].Steps))
	}
}

func TestLoadPipeline_NoStages(t *testing.T) {
	dir := t.TempDir()
	cfg := `name: demo
image: golang:1.21
`
	if err := os.WriteFile(filepath.Join(dir, ".skating.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadPipeline(filepath.Join(dir, ".skating.yaml"))
	if err == nil {
		t.Fatal("expected error for missing stages")
	}
}

func TestLoadPipeline_MissingFile(t *testing.T) {
	_, err := LoadPipeline("/nonexistent/path/.skating.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadPipeline_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	// 顶层 steps 而不是 pipeline.stages — 这是之前 init 模板的 bug
	cfg := `name: demo
image: golang:1.21
steps:
  - name: build
    type: shell
    script: |
      echo "x"
`
	if err := os.WriteFile(filepath.Join(dir, ".skating.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadPipeline(filepath.Join(dir, ".skating.yaml"))
	if err == nil {
		t.Fatal("expected error: schema requires pipeline.stages[], not top-level steps[]")
	}
}

func TestLoadPipeline_ParallelStage(t *testing.T) {
	dir := t.TempDir()
	cfg := `name: demo
pipeline:
  stages:
    - name: concurrent
      parallel: true
      steps:
        - {name: a, type: shell, script: "echo a"}
        - {name: b, type: shell, script: "echo b"}
`
	if err := os.WriteFile(filepath.Join(dir, ".skating.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := LoadPipeline(filepath.Join(dir, ".skating.yaml"))
	if err != nil {
		t.Fatalf("LoadPipeline: %v", err)
	}
	if !p.Stages[0].Parallel {
		t.Error("parallel flag not parsed correctly")
	}
}