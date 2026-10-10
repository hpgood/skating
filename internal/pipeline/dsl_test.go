package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPipeline_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `name: demo
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

func TestLoadConfig_GroupsValid(t *testing.T) {
	dir := t.TempDir()
	cfg := `name: demo
pipeline:
  groups:
    all:        [build, test]
    only_build: [build]
  stages:
    - name: build
      steps:
        - {name: compile, type: shell, script: "echo b"}
    - name: test
      steps:
        - {name: run, type: shell, script: "echo t"}
`
	if err := os.WriteFile(filepath.Join(dir, ".skating.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, err := LoadConfig(filepath.Join(dir, ".skating.yaml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(c.Pipeline.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d: %v", len(c.Pipeline.Groups), c.Pipeline.Groups)
	}
	got := c.Pipeline.Groups["all"]
	if len(got) != 2 || got[0] != "build" || got[1] != "test" {
		t.Errorf("groups[all] = %v, want [build test]", got)
	}
	if only := c.Pipeline.Groups["only_build"]; len(only) != 1 || only[0] != "build" {
		t.Errorf("groups[only_build] = %v, want [build]", only)
	}
}

func TestLoadConfig_GroupsUnknownStage(t *testing.T) {
	dir := t.TempDir()
	cfg := `name: demo
pipeline:
  groups:
    all: [build, deploy]
  stages:
    - name: build
      steps:
        - {name: compile, type: shell, script: "echo b"}
`
	if err := os.WriteFile(filepath.Join(dir, ".skating.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadConfig(filepath.Join(dir, ".skating.yaml"))
	if err == nil {
		t.Fatal("expected error for unknown stage in group")
	}
	msg := err.Error()
	if !strings.Contains(msg, `pipeline.groups["all"]`) {
		t.Errorf("error should mention group name, got: %q", msg)
	}
	if !strings.Contains(msg, "deploy") {
		t.Errorf("error should mention unknown stage 'deploy', got: %q", msg)
	}
	if !strings.Contains(msg, "build") {
		t.Errorf("error should list known stages (containing 'build'), got: %q", msg)
	}
}

func TestLoadConfig_GroupEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg := `name: demo
pipeline:
  groups:
    empty: []
  stages:
    - name: build
      steps:
        - {name: compile, type: shell, script: "echo b"}
`
	if err := os.WriteFile(filepath.Join(dir, ".skating.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadConfig(filepath.Join(dir, ".skating.yaml"))
	if err == nil {
		t.Fatal("expected error for empty group")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty group, got: %q", err.Error())
	}
}
