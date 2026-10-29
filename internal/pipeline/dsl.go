package pipeline

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Step 表示 pipeline 中的一个执行步骤
type Step struct {
	Name   string                       `yaml:"name"`
	Type   string                       `yaml:"type"`   // shell、lua 或 ssh
	Script string                       `yaml:"script"` // 内联脚本
	Source string                       `yaml:"source"` // 外部脚本路径
	When   string                       `yaml:"when"`   // 条件表达式
	Env    map[string]map[string]string `yaml:"env"`    // 环境变量（按平台分组）

	// Image 本 step 使用 Docker 容器执行时的镜像（仅对 shell 步骤生效）。
	// 为空时在 host shell 直接执行。不同步骤可使用不同镜像。
	Image string `yaml:"image"`
	// DockerOptions 追加到 docker run 的额外参数（如 --network host、
	// -v /tmp/cache:/var/cache）。位于内置参数之后、镜像名之前，
	// 重复 flag 由 docker 按后值生效（可覆盖内置的 -w 等）。
	DockerOptions []string `yaml:"docker_options"`
	// WorkDir 工作目录：docker 容器内挂载点（仅对配置了 image 的 shell 步骤生效），
	// 为空时默认 /work，不以 / 开头时自动补前缀（如 "cache" 等价 "/cache"）；
	// ssh 步骤为远端 cd 目录
	WorkDir string `yaml:"workdir"`

	// 以下为 SSH 远程执行字段（仅 type: ssh 生效）
	Host string `yaml:"host"` // 远端主机（IP 或域名），必填
	Port int    `yaml:"port"` // SSH 端口，缺省 22
	User string `yaml:"user"` // 登录用户，必填
	// Auth 认证方式："password" 或 "key"，缺省 "password"
	Auth string `yaml:"auth"`
	// Secret 是全局 secret 的名字（skating secret set <name> 保存），
	// 运行时从 ~/.skating/secrets.json resolve 出真实密码/私钥。
	// yaml 中只保存名字，不保存认证材料本身
	Secret string `yaml:"secret"`

	// 以下为文件传输字段（仅 type: scp / sync 生效）
	// Direction 传输方向（仅 scp）："push"（本地→远端，缺省）或 "pull"（远端→本地）
	Direction string `yaml:"direction"`
	// Local 本地路径，支持 ${SKA_*} 变量展开（scp 支持文件或目录；sync 必须是目录）
	Local string `yaml:"local"`
	// Remote 远端路径，支持 ${SKA_*} 变量展开（scp 支持文件或目录；sync 必须是目录）
	Remote string `yaml:"remote"`
	// Delete 镜像删除（仅 sync）：远端存在但本地不存在的文件/目录是否删除。
	// 缺省 true（不写该字段即删除），传 false 保留远端多余文件
	Delete *bool `yaml:"delete"`

	// StageName 运行时由 runner 填充所属 stage 名（不参与 yaml 序列化），
	// 供上层适配器做进度上报等用途
	StageName string `yaml:"-"`
}

// resolveEnvGroups 按平台解析分组式环境变量（default + 平台组）。
// 分组键约定：default 注入所有平台；其余键（windows/linux/darwin 等）
// 仅在键名等于 goos 时注入。平台专属变量优先覆盖 default 中的同名变量。
// goos 由调用方传入（通常为 runtime.GOOS），便于测试。
// 未命中任何变量时返回 nil。Step.ResolveEnv 与 Stage.ResolveEnv 共用。
func resolveEnvGroups(groups map[string]map[string]string, goos string) map[string]string {
	if len(groups) == 0 {
		return nil
	}
	merged := make(map[string]string)
	maps.Copy(merged, groups["default"])
	maps.Copy(merged, groups[goos])
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// ResolveEnv 按平台解析 step 级环境变量（default + 当前平台组），
// 见 resolveEnvGroups 的分组与覆盖规则
func (s Step) ResolveEnv(goos string) map[string]string {
	return resolveEnvGroups(s.Env, goos)
}

// Stage 表示 pipeline 中的一个阶段
type Stage struct {
	Name string `yaml:"name"`
	// Description 仅用于展示（skating stages），不参与执行、分组校验或环境变量
	Description string `yaml:"description"`
	// When 是 stage 级条件，与 step 的 when 同语法；false 时跳过整个 stage。
	// 支持布尔字面量（true / false，大小写不敏感）与比较表达式。
	When     string `yaml:"when"`
	Steps    []Step `yaml:"steps"`
	Parallel bool   `yaml:"parallel"`
	// Env stage 级环境变量（按平台分组，与 step env 同构）：
	// stage 内所有 step 共享，优先级介于构建 env 与 step env 之间
	//（同名变量 step 覆盖 stage）
	Env map[string]map[string]string `yaml:"env"`
}

// ResolveEnv 按平台解析 stage 级环境变量（default + 当前平台组），
// 见 resolveEnvGroups 的分组与覆盖规则
func (st Stage) ResolveEnv(goos string) map[string]string {
	return resolveEnvGroups(st.Env, goos)
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
	Timeout  Duration       `yaml:"timeout"` // 整个 pipeline 的运行超时（如 10m），缺省用 DefaultTimeout
	Pipeline PipelineConfig `yaml:"pipeline"`
}

// Group 是 pipeline.groups 中的一个命名 stage 集合。
// 支持两种 yaml 写法：
//
//	all: [init, build]                                  # 列表简写，description 为空
//	all: {stages: [init, build], description: "全部"}   # mapping 形式
type Group struct {
	Stages      []string `yaml:"stages"`
	Description string   `yaml:"description"`
}

// UnmarshalYAML 同时接受列表简写与 {stages, description} mapping 形式。
func (g *Group) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		return node.Decode(&g.Stages)
	}
	type plain Group
	var p plain
	if err := node.Decode(&p); err != nil {
		return err
	}
	*g = Group(p)
	return nil
}

// PipelineConfig 嵌入到项目配置中的 pipeline 定义
type PipelineConfig struct {
	// Groups 定义命名 stage 集合（name -> Group），供 `run --group <name>`
	// 选择性地运行一组 stage。组名与 stage 名处于不同命名空间。
	// 每个 group 可写成 stage 列表简写，或 {stages, description} mapping 形式。
	// 执行顺序始终按 Stages 的声明顺序，组内书写顺序无影响。
	Groups map[string]Group `yaml:"groups"`
	Stages []Stage          `yaml:"stages"`
}

// DefaultTimeout 未配置 timeout 字段时的默认运行超时
const DefaultTimeout = 10 * time.Minute

// Duration 支持 Go duration 字符串（"10m"/"600s"）与纯数字（按秒）的 yaml 时长类型。
// yaml.v3 不原生支持 time.Duration，需自定义反序列化。
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err == nil {
		dur, err := ParseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(dur)
		return nil
	}
	// 非字符串节点（真数字类型）按秒解释
	var n int64
	if err := node.Decode(&n); err == nil {
		*d = Duration(time.Duration(n) * time.Second)
		return nil
	}
	return fmt.Errorf("invalid duration value at line %d: %s", node.Line, node.Value)
}

// ParseDuration 解析时长：Go duration 字符串（"10m"/"600s"）或纯数字（按秒）。
// yaml 的 timeout 字段与 run --timeout 共用同一解析规则。
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	// 纯数字按秒解释（yaml.v3 也会把裸数字 decode 成字符串）
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return dur, nil
}

// Or 返回 d；d 非正数时返回 def
func (d Duration) Or(def time.Duration) time.Duration {
	if d > 0 {
		return time.Duration(d)
	}
	return def
}

// LoadConfig 从 .skating.yaml 读取完整顶层配置（pipeline + timeout 等）
func LoadConfig(configPath string) (*Config, error) {
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

	if err := validateGroups(cfg.Pipeline.Groups, cfg.Pipeline.Stages); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validateGroups 校验 pipeline.groups 的每一项：每个组非空，且组内每个条目
// 都必须是已声明的 stage 名。组内重复条目按集合语义静默去重。
// 组名排序遍历，保证错误信息确定性；已知 stage 列表按声明顺序输出。
func validateGroups(groups map[string]Group, stages []Stage) error {
	if len(groups) == 0 {
		return nil
	}

	stageNames := make([]string, 0, len(stages))
	known := make(map[string]bool, len(stages))
	for _, s := range stages {
		stageNames = append(stageNames, s.Name)
		known[s.Name] = true
	}

	for _, g := range slices.Sorted(maps.Keys(groups)) {
		refs := groups[g].Stages
		if len(refs) == 0 {
			return fmt.Errorf("pipeline.groups[%q] is empty", g)
		}
		var unknown []string
		seen := make(map[string]bool, len(refs))
		for _, r := range refs {
			if seen[r] {
				continue
			}
			seen[r] = true
			if !known[r] {
				unknown = append(unknown, r)
			}
		}
		if len(unknown) > 0 {
			return fmt.Errorf("pipeline.groups[%q] references unknown stage(s) %v (known stages: %v)", g, unknown, stageNames)
		}
	}

	return nil
}

// LoadPipeline 从 .skating.yaml 中读取 pipeline 配置
func LoadPipeline(configPath string) (*Pipeline, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return &Pipeline{Stages: cfg.Pipeline.Stages}, nil
}
