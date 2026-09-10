package pipeline

import (
	"fmt"
	"maps"
	"os"

	"gopkg.in/yaml.v3"
)

// Step 表示 pipeline 中的一个执行步骤
type Step struct {
	Name   string                       `yaml:"name"`
	Type   string                       `yaml:"type"`   // shell 或 lua
	Script string                       `yaml:"script"` // 内联脚本
	Source string                       `yaml:"source"` // 外部脚本路径
	When   string                       `yaml:"when"`   // 条件表达式
	Env    map[string]map[string]string `yaml:"env"`    // 环境变量（按平台分组）
}

// ResolveEnv 按平台解析 step 级环境变量。
// 分组键约定：default 注入所有平台；其余键（windows/linux/darwin 等）
// 仅在键名等于 goos 时注入。平台专属变量优先覆盖 default 中的同名变量。
// goos 由调用方传入（通常为 runtime.GOOS），便于测试。
// 未命中任何变量时返回 nil。
func (s Step) ResolveEnv(goos string) map[string]string {
	if len(s.Env) == 0 {
		return nil
	}
	merged := make(map[string]string)
	maps.Copy(merged, s.Env["default"])
	maps.Copy(merged, s.Env[goos])
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// Stage 表示 pipeline 中的一个阶段
type Stage struct {
	Name     string `yaml:"name"`
	Steps    []Step `yaml:"steps"`
	Parallel bool   `yaml:"parallel"`
}

// Pipeline 表示完整的 pipeline
type Pipeline struct {
	Stages []Stage `yaml:"stages"`
}

// PipelineResult 记录单个步骤的执行结果
type PipelineResult struct {
	StageName string
	StepName  string
	Status    string // success, failed, skipped
	Output    string
	Error     string
	Duration  string
}

// Config 是 .skating.yaml 的顶层结构
type Config struct {
	Name     string         `yaml:"name"`
	Image    string         `yaml:"image"`
	Pipeline PipelineConfig `yaml:"pipeline"`
}

// PipelineConfig 嵌入到项目配置中的 pipeline 定义
type PipelineConfig struct {
	Stages []Stage `yaml:"stages"`
}

// LoadPipeline 从 .skating.yaml 中读取 pipeline 配置
func LoadPipeline(configPath string) (*Pipeline, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if len(cfg.Pipeline.Stages) == 0 {
		return nil, fmt.Errorf("no pipeline stages defined in %s", configPath)
	}

	return &Pipeline{Stages: cfg.Pipeline.Stages}, nil
}
