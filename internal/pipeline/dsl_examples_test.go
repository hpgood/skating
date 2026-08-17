package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExamplesParse 测试 examples 目录下所有 .skating.yaml 可被正确解析
func TestExamplesParse(t *testing.T) {
	examplesDir := filepath.Join("..", "..", "examples")
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Skipf("examples 目录不存在，跳过: %v", err)
	}

	found := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		configPath := filepath.Join(examplesDir, entry.Name(), ".skating.yaml")
		if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
			continue
		}

		found = true
		t.Run(entry.Name(), func(t *testing.T) {
			pipeline, err := LoadPipeline(configPath)
			if err != nil {
				t.Fatalf("解析 %s 失败: %v", configPath, err)
			}

			if pipeline == nil {
				t.Fatal("pipeline 为 nil")
			}

			// 验证每个 stage 结构
			for _, stage := range pipeline.Stages {
				if stage.Name == "" {
					t.Error("stage name 为空")
				}
				for _, step := range stage.Steps {
					if step.Name == "" {
						t.Error("step name 为空")
					}
					if step.Type != "shell" && step.Type != "lua" {
						t.Errorf("未知的 step type: %q (应为 shell 或 lua)", step.Type)
					}
					if step.Script == "" && step.Source == "" {
						t.Errorf("step %q 既没有 script 也没有 source", step.Name)
					}
				}
			}

			t.Logf("%s: %d stages", entry.Name(), len(pipeline.Stages))
		})
	}

	if !found {
		t.Fatal("未找到任何示例项目")
	}
}

// TestExamplePipelineStages 验证每个示例的 stage/step 数量
func TestExamplePipelineStages(t *testing.T) {
	tests := []struct {
		name       string
		minStages  int
		minSteps   int
		hasLua     bool
		hasShell   bool
		hasParallel bool
		hasWhen    bool
	}{
		{"go-project", 3, 4, false, true, false, false},
		{"node-project", 3, 3, false, true, false, false},
		{"mixed-shell-lua", 3, 3, true, true, false, false},
		{"parallel-stages", 3, 3, true, true, true, false},
		{"conditional-build", 2, 3, true, true, false, true},
	}

	examplesDir := filepath.Join("..", "..", "examples")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(examplesDir, tt.name, ".skating.yaml")
			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				t.Skipf("%s 不存在，跳过", configPath)
			}

			pipeline, err := LoadPipeline(configPath)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}

			if len(pipeline.Stages) < tt.minStages {
				t.Errorf("stage 数量: got %d, want >= %d", len(pipeline.Stages), tt.minStages)
			}

			// 统计所有 steps
			var totalSteps int
			var hasLua, hasShell, hasParallel, hasWhen bool
			for _, stage := range pipeline.Stages {
				totalSteps += len(stage.Steps)
				if stage.Parallel {
					hasParallel = true
				}
				for _, step := range stage.Steps {
					if step.Type == "lua" {
						hasLua = true
					}
					if step.Type == "shell" {
						hasShell = true
					}
					if step.When != "" {
						hasWhen = true
					}
				}
			}

			if totalSteps < tt.minSteps {
				t.Errorf("step 总数: got %d, want >= %d", totalSteps, tt.minSteps)
			}
			if tt.hasLua && !hasLua {
				t.Error("期望包含 lua step")
			}
			if tt.hasShell && !hasShell {
				t.Error("期望包含 shell step")
			}
			if tt.hasParallel && !hasParallel {
				t.Error("期望包含 parallel stage")
			}
			if tt.hasWhen && !hasWhen {
				t.Error("期望包含 when 条件")
			}
		})
	}
}