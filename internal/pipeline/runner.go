package pipeline

import (
	"fmt"
	"maps"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hpgood/skating/internal/executor"
)

// Executor 定义步骤执行接口。logFn 允许 executor 内部按行输出实时日志，
// 调用方通过 logFn 把 executor 内部产生的 stdout/stderr 透传到上层日志流。
type Executor interface {
	Execute(step Step, env map[string]string, logFn func(string)) (string, error)
}

// RunOptions 控制 RunPipeline 的过滤行为。
//
//   - StageName == "" 且 StepName == "" 且 Stages 为空：运行完整 pipeline
//     （向后兼容）。
//   - StageName == "X" 且 StepName == ""：只运行名字为 X 的整个 stage。
//     其他 stage 在 results 里以 status=skipped 留下记录，便于审计。
//   - StageName == "X" 且 StepName == "Y"：只运行 X stage 里的 Y step。
//     其他 stage 和 X 里其他 step 都记为 skipped。
//   - Stages 非空（“组模式”，--group）：只运行集合内的 stage，执行顺序仍按
//     pipeline 声明顺序；未选中的 stage 记为 skipped。与 --step 的区别在于
//     失败语义：组模式下首个失败 stage 之后的（选中的）stage 也记为 skipped
//     （原因 "skipped after earlier stage failure"），整体返回 error。
//
// 不匹配的 stage/step 会记为 status="skipped"，调用方可观察到到底跑了什么。
type RunOptions struct {
	StageName string
	StepName  string
	// Stages 是 --group 选中的 stage 名集合；非空即“组模式”。
	Stages map[string]bool
	// UserEnv 是 --env 注入的用户变量，优先级最高：
	// 在 stage env / step env / 跨 step 变量池之后覆盖同名变量。
	// nil 表示没有用户注入，保持默认优先级。
	UserEnv map[string]string
}

// SelectsStage 判断当前选项是否选中名为 name 的 stage。
// 除 --group 的 Stages 集合外（组模式）一律返回 true。
func (o *RunOptions) SelectsStage(name string) bool {
	if o == nil {
		return true
	}
	if o.StageName != "" {
		return name == o.StageName
	}
	if len(o.Stages) > 0 {
		return o.Stages[name]
	}
	return true
}

// RunPipeline 按顺序执行 pipeline 的各个 stage。
// Stage.Parallel 为 true 时，该 stage 内的 steps 并行执行。
// 遇到任一步骤失败则停止后续步骤。
// logFn 用于实时输出日志。
// opts 可选：传 nil 等价于 RunOptions{}（运行完整 pipeline）。
// vars 可选：构建级共享变量池，承载脚本经 "::set-var" 输出标记或 Lua set_env
// 传递给后续 step 的变量（脚本环境变量与 When 条件均可见）；传 nil 表示不启用。
func RunPipeline(p *Pipeline, exec Executor, env map[string]string, logFn func(string), opts *RunOptions, vars *VarPool) ([]*PipelineResult, error) {
	if opts == nil {
		opts = &RunOptions{}
	}
	groupMode := len(opts.Stages) > 0
	var results []*PipelineResult
	var firstErr error
	var failedEarlier bool

	for _, stage := range p.Stages {
		// 阶段级过滤：如果指定了 stage（--step <stage>）或组（--group），
		// 跳过其他 stage 但记录 skipped 结果。
		if !opts.SelectsStage(stage.Name) {
			reason := "stage not selected by --step filter"
			if groupMode {
				reason = "stage not selected by --group filter"
			}
			appendSkippedStage(&results, stage, reason, logFn)
			continue
		}

		// 组模式：首个失败 stage 之后的所有剩余 stage 记为 skipped，
		// 让用户看到"哪些 stage 因前面失败没跑到"。
		if groupMode && failedEarlier {
			appendSkippedStage(&results, stage, "skipped after earlier stage failure", logFn)
			continue
		}

		stageResults, err := runStage(stage, exec, env, logFn, opts, vars)
		results = append(results, stageResults...)
		if err != nil {
			// step-filter 模式：让剩下的 stage 至少以 skipped 形式出现，
			// 让用户看到"哪些 stage 因为 filter 没跑到"。
			// 组模式：记录首个错误并继续遍历，让后续 stage 记为 skipped，
			// 整体仍返回 error。
			// 默认模式（opts 为零值）：保留原"遇错即停"语义。
			if opts.StageName != "" || opts.StepName != "" {
				// 不立刻 return —— 继续遍历，让后续 stage 至少以 skipped 形式记录。
				// 但要保留 firstErr 让循环结束后返回给调用方（不能让 cmd_run 误以为成功）。
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if groupMode {
				if firstErr == nil {
					firstErr = err
				}
				failedEarlier = true
				continue
			}
			return results, err
		}
	}

	return results, firstErr
}

// newSkippedResult 构造一条 status=skipped 的结果记录，统一 skipped 结果形状。
func newSkippedResult(stageName, stepName, reason string) *PipelineResult {
	return &PipelineResult{
		StageName: stageName,
		StepName:  stepName,
		Status:    "skipped",
		Output:    reason,
	}
}

// logSkipped 输出可视化的 skip 提示（logFn 为 nil 时静默）。
func logSkipped(logFn func(string), stageName, stepName, reason string) {
	if logFn != nil {
		logFn(executor.ColorWarn(fmt.Sprintf("[%s/%s] skipped (%s)", stageName, stepName, reason)))
	}
}

// appendSkippedStage 为 stage 内的每个 step 追加一条 status=skipped 的结果，
// 并通过 logFn 输出可视化的 skip 提示（logFn 为 nil 时只记录结果）。
func appendSkippedStage(results *[]*PipelineResult, stage Stage, reason string, logFn func(string)) {
	for _, step := range stage.Steps {
		*results = append(*results, newSkippedResult(stage.Name, step.Name, reason))
		logSkipped(logFn, stage.Name, step.Name, reason)
	}
}

// expandEnvValues 对 m 中每个值做 ${VAR} / $VAR 展开，支持与固定文本拼接
// （如 "0.2.${SKA_BUILD_ID}" → "0.2.7"）。引用优先取 m 内的同名键（支持组内
// 相互引用），否则取 base（已累积的 env）。未定义的变量展开为空串。
// 迭代至稳定（上限 len(m) 次）以解析 A→B→C 这类引用链；返回新 map，不改入参。
func expandEnvValues(m, base map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	out := maps.Clone(m)
	for i := 0; i < len(out); i++ {
		changed := false
		for k, v := range out {
			nv := os.Expand(v, func(name string) string {
				if val, ok := out[name]; ok {
					return val
				}
				return base[name]
			})
			if nv != v {
				out[k] = nv
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return out
}

// withUserEnv 返回叠加了用户环境变量的 env 副本。userEnv 来自 --env，
// 优先级最高：覆盖构建 env、stage/step env 以及同名跨 step 变量。
// userEnv 为空时原样返回，不做克隆。
func withUserEnv(env, userEnv map[string]string) map[string]string {
	if len(userEnv) == 0 {
		return env
	}
	out := maps.Clone(env)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, userEnv)
	return out
}

// mergeEnvLayer 把一层 stage/step env 合并到 base 上：
//   - 先按 base（含 --env 覆盖）展开 layer 中的 ${VAR}；
//   - layer 中与 --env 同名的键直接丢弃，改由 --env 提供，
//     保证插值也看到用户值；
//   - 合并后再用 --env 覆盖一次，确保用户值最终胜出。
//
// 返回新 map，不修改 base 与 layer。
func mergeEnvLayer(base, layer, userEnv map[string]string) map[string]string {
	base = withUserEnv(base, userEnv)
	if len(layer) > 0 {
		if len(userEnv) > 0 {
			filtered := maps.Clone(layer)
			for k := range userEnv {
				delete(filtered, k)
			}
			layer = filtered
		}
		m := expandEnvValues(layer, base)
		if len(m) > 0 {
			merged := maps.Clone(base)
			if merged == nil {
				merged = map[string]string{}
			}
			maps.Copy(merged, m)
			base = merged
		}
	}
	return withUserEnv(base, userEnv)
}

func runStage(stage Stage, exec Executor, env map[string]string, logFn func(string), opts *RunOptions, vars *VarPool) ([]*PipelineResult, error) {
	// 合并 stage 级 env（default + 当前平台组）到本 stage 的 base env，
	// 并按 --env > stage env 的优先级覆盖同名变量。
	// stage 内所有 step 共享，When 条件同样可见。
	// 克隆避免污染共享构建 env。
	env = mergeEnvLayer(env, stage.ResolveEnv(runtime.GOOS), opts.UserEnv)

	// stage 级条件：在 stage env 合并之后、step 过滤与并行分发之前求值，
	// 使构建 env、stage env、跨 step 变量池与 git 上下文都可见；显式
	// --step <stage>/<step> 也无法绕过 when: false。
	// 求值错误视为失败（每个 step 一条 failed 结果），条件不满足则整段 skipped
	// 并返回 nil —— 跳过的 stage 不算构建失败。
	if stage.When != "" {
		// 变量池可能覆盖 stage env，再叠加一次 --env 保证用户值在条件中优先。
		ok, err := evalWhenWithEnv(stage.When, withUserEnv(vars.Merged(env), opts.UserEnv))
		if err != nil {
			var results []*PipelineResult
			for _, step := range stage.Steps {
				results = append(results, &PipelineResult{
					StageName: stage.Name,
					StepName:  step.Name,
					Status:    "failed",
					Error:     fmt.Sprintf("eval stage when: %v", err),
				})
				if logFn != nil {
					logFn(executor.ColorFail(fmt.Sprintf("[%s/%s] condition error: %v", stage.Name, step.Name, err)))
				}
			}
			return results, fmt.Errorf("stage %q when condition: %w", stage.Name, err)
		}
		if !ok {
			var results []*PipelineResult
			appendSkippedStage(&results, stage, "stage condition not met", logFn)
			return results, nil
		}
	}

	// step 级过滤：在原 stage 上做"投影"——只保留匹配的 step，其余记为 skipped
	if opts.StepName != "" {
		var projected Stage = stage
		var kept []Step
		var skipped []*PipelineResult
		for _, step := range stage.Steps {
			if step.Name == opts.StepName {
				kept = append(kept, step)
			} else {
				skipped = append(skipped, newSkippedResult(stage.Name, step.Name, "step not selected by --step filter"))
				logSkipped(logFn, stage.Name, step.Name, "step not selected by --step filter")
			}
		}
		projected.Steps = kept

		var stageResults []*PipelineResult
		stageResults = append(stageResults, skipped...)
		if len(projected.Steps) == 0 {
			// 用户指定了 step 但该 stage 里没有这个 step —— 不报错，
			// 但要让 runner 仍然返回上层（不当作 stage 失败）。
			return stageResults, nil
		}
		var err error
		var innerResults []*PipelineResult
		if projected.Parallel {
			innerResults, err = runParallelInner(projected, exec, env, logFn, vars, opts.UserEnv)
		} else {
			innerResults, err = runSequentialInner(projected, exec, env, logFn, vars, opts.UserEnv)
		}
		stageResults = append(stageResults, innerResults...)
		return stageResults, err
	}

	// 整 stage 模式（opts.StepName == ""，但 opts.StageName != ""）：
	// 让 stage 内 sequential step 失败后继续执行剩余 step，
	// 这样 --step <stage> 能完整跑完 stage。
	if opts.StageName != "" && !stage.Parallel {
		innerResults, err := runSequentialContinueOnError(stage, exec, env, logFn, vars, opts.UserEnv)
		return innerResults, err
	}

	if stage.Parallel {
		return runParallel(stage, exec, env, logFn, vars, opts.UserEnv)
	}
	return runSequential(stage, exec, env, logFn, vars, opts.UserEnv)
}

func runSequential(stage Stage, exec Executor, env map[string]string, logFn func(string), vars *VarPool, userEnv map[string]string) ([]*PipelineResult, error) {
	return runSequentialInner(stage, exec, env, logFn, vars, userEnv)
}

func runSequentialInner(stage Stage, exec Executor, env map[string]string, logFn func(string), vars *VarPool, userEnv map[string]string) ([]*PipelineResult, error) {
	var results []*PipelineResult
	for _, step := range stage.Steps {
		result := executeStepWithUserEnv(stage.Name, step, exec, env, userEnv, logFn, vars) // logFn 原样透传到 executeStep
		results = append(results, result)
		if result.Status == "failed" {
			return results, fmt.Errorf("step %q failed: %s", step.Name, result.Error)
		}
	}
	return results, nil
}

// runSequentialContinueOnError 用于 --step <stage> 模式：sequential stage 内
// 一个 step 失败后仍继续执行剩余 step（让用户看到整个 stage 完整结果）。
// 整体返回的 err 会保留第一个失败的 error 给上层做 status 决策。
func runSequentialContinueOnError(stage Stage, exec Executor, env map[string]string, logFn func(string), vars *VarPool, userEnv map[string]string) ([]*PipelineResult, error) {
	var results []*PipelineResult
	var firstErr error
	for _, step := range stage.Steps {
		result := executeStepWithUserEnv(stage.Name, step, exec, env, userEnv, logFn, vars)
		results = append(results, result)
		if result.Status == "failed" && firstErr == nil {
			firstErr = fmt.Errorf("step %q failed: %s", step.Name, result.Error)
		}
	}
	return results, firstErr
}

func runParallel(stage Stage, exec Executor, env map[string]string, logFn func(string), vars *VarPool, userEnv map[string]string) ([]*PipelineResult, error) {
	return runParallelInner(stage, exec, env, logFn, vars, userEnv)
}

func runParallelInner(stage Stage, exec Executor, env map[string]string, logFn func(string), vars *VarPool, userEnv map[string]string) ([]*PipelineResult, error) {
	results := make([]*PipelineResult, len(stage.Steps))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i := range stage.Steps {
		wg.Add(1)
		go func(idx int, step Step) {
			defer wg.Done()
			result := executeStepWithUserEnv(stage.Name, step, exec, env, userEnv, logFn, vars)
			mu.Lock()
			results[idx] = result
			if result.Status == "failed" && firstErr == nil {
				firstErr = fmt.Errorf("step %q failed: %s", step.Name, result.Error)
			}
			mu.Unlock()
		}(i, stage.Steps[i])
	}

	wg.Wait()
	return results, firstErr
}

// executeStep 是不带 --env 用户变量的兼容入口，行为与 executeStepWithUserEnv 一致。
func executeStep(stageName string, step Step, exec Executor, env map[string]string, logFn func(string), vars *VarPool) *PipelineResult {
	return executeStepWithUserEnv(stageName, step, exec, env, nil, logFn, vars)
}

// executeStepWithUserEnv 执行单个 step。userEnv 来自 --env，优先级最高：
// 在变量池与 step env 之后覆盖同名变量，When 条件同样看到用户值。
func executeStepWithUserEnv(stageName string, step Step, exec Executor, env map[string]string, userEnv map[string]string, logFn func(string), vars *VarPool) *PipelineResult {
	// 合并构建级共享变量（::set-var / Lua set_env 传入）到本 step 的 env 视图：
	// 池为空时原样返回（不克隆）。随后叠加 --env，确保用户值优先。
	env = withUserEnv(vars.Merged(env), userEnv)

	result := &PipelineResult{
		StageName: stageName,
		StepName:  step.Name,
	}

	// 条件判断
	if step.When != "" {
		// 自动注入 git 上下文变量（branch / commit / git_dirty）到 ctx
		ctx := BuildGitContext("")
		// 把 ctx 也合并到 env（裸标识符查询时优先用 env 命中），保持双视图
		for k, v := range ctx {
			if _, exists := env[k]; !exists {
				env[k] = v
			}
		}
		ok, err := EvalWhen(step.When, env, ctx)
		if err != nil {
			result.Status = "failed"
			result.Error = fmt.Sprintf("eval condition: %v", err)
			logFn(executor.ColorFail(fmt.Sprintf("[%s/%s] condition error: %v", stageName, step.Name, err)))
			return result
		}
		if !ok {
			result.Status = "skipped"
			result.Output = "condition not met"
			logFn(executor.ColorWarn(fmt.Sprintf("[%s/%s] skipped (condition not met)", stageName, step.Name)))
			return result
		}
	}

	logFn(fmt.Sprintf("[%s/%s] starting...", stageName, step.Name))

	// 合并 step 级环境变量（default 全平台，windows/linux 等按当前平台注入）。
	// 值中的 ${VAR} / $VAR 会按已累积的 env（构建 env + stage env + 变量池）展开，
	// 支持拼接（如 "0.2.${SKA_BUILD_ID}"）。与 --env 同名的键由 --env 覆盖。
	// 克隆避免污染共享 env。
	stepEnv := mergeEnvLayer(env, step.ResolveEnv(runtime.GOOS), userEnv)

	// 填充所属 stage 名，供上层 Executor 适配器做进度上报
	step.StageName = stageName

	start := time.Now()
	output, err := exec.Execute(step, stepEnv, logFn)
	elapsed := time.Since(start)

	result.Duration = elapsed.Truncate(time.Millisecond).String()

	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		logFn(executor.ColorFail(fmt.Sprintf("[%s/%s] FAILED (%s): %s", stageName, step.Name, result.Duration, err.Error())))
	} else {
		result.Status = "success"
		result.Output = output
		// shell 步骤的输出已在 executor 内按行流式转发到 logFn，这里不再整段重复；
		// lua 步骤的 print 输出只缓存在 result.Output，仍需在此补发到日志。
		if step.Type != "shell" {
			if out := strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n")); out != "" {
				logFn(out)
			}
		}
		logFn(executor.ColorSuccess(fmt.Sprintf("[%s/%s] success (%s)", stageName, step.Name, result.Duration)))
	}

	return result
}
