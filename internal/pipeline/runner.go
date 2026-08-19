package pipeline

import (
	"fmt"
	"sync"
	"time"
)

// Executor 定义步骤执行接口
type Executor interface {
	Execute(step Step, env map[string]string) (string, error)
}

// RunOptions 控制 RunPipeline 的过滤行为。
//
//   - StageName == "" 且 StepName == ""：运行完整 pipeline（向后兼容）。
//   - StageName == "X" 且 StepName == ""：只运行名字为 X 的整个 stage。
//     其他 stage 在 results 里以 status=skipped 留下记录，便于审计。
//   - StageName == "X" 且 StepName == "Y"：只运行 X stage 里的 Y step。
//     其他 stage 和 X 里其他 step 都记为 skipped。
//
// 不匹配的 stage/step 会记为 status="skipped"，调用方可观察到到底跑了什么。
type RunOptions struct {
	StageName string
	StepName  string
}

// RunPipeline 按顺序执行 pipeline 的各个 stage。
// Stage.Parallel 为 true 时，该 stage 内的 steps 并行执行。
// 遇到任一步骤失败则停止后续步骤。
// logFn 用于实时输出日志。
// opts 可选：传 nil 等价于 RunOptions{}（运行完整 pipeline）。
func RunPipeline(p *Pipeline, exec Executor, env map[string]string, logFn func(string), opts *RunOptions) ([]*PipelineResult, error) {
	if opts == nil {
		opts = &RunOptions{}
	}
	var results []*PipelineResult

	for _, stage := range p.Stages {
		// 阶段级过滤：如果指定了 stage，跳过其他 stage 但记录 skipped 结果
		if opts.StageName != "" && stage.Name != opts.StageName {
			for _, step := range stage.Steps {
				results = append(results, &PipelineResult{
					StageName: stage.Name,
					StepName:  step.Name,
					Status:    "skipped",
					Output:    "stage not selected by --step filter",
				})
				if logFn != nil {
					logFn(fmt.Sprintf("[%s/%s] skipped (stage not selected by --step filter)", stage.Name, step.Name))
				}
			}
			continue
		}

		stageResults, err := runStage(stage, exec, env, logFn, opts)
		results = append(results, stageResults...)
		if err != nil {
			// step-filter 模式：让剩下的 stage 至少以 skipped 形式出现，
			// 让用户看到"哪些 stage 因为 filter 没跑到"。
			// 默认模式（opts 为零值）：保留原"遇错即停"语义。
			if opts.StageName != "" || opts.StepName != "" {
				continue
			}
			return results, err
		}
	}

	return results, nil
}

func runStage(stage Stage, exec Executor, env map[string]string, logFn func(string), opts *RunOptions) ([]*PipelineResult, error) {
	// step 级过滤：在原 stage 上做"投影"——只保留匹配的 step，其余记为 skipped
	if opts.StepName != "" {
		var projected Stage = stage
		var kept []Step
		var skipped []*PipelineResult
		for _, step := range stage.Steps {
			if step.Name == opts.StepName {
				kept = append(kept, step)
			} else {
				skipped = append(skipped, &PipelineResult{
					StageName: stage.Name,
					StepName:  step.Name,
					Status:    "skipped",
					Output:    "step not selected by --step filter",
				})
				if logFn != nil {
					logFn(fmt.Sprintf("[%s/%s] skipped (step not selected by --step filter)", stage.Name, step.Name))
				}
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
			innerResults, err = runParallelInner(projected, exec, env, logFn)
		} else {
			innerResults, err = runSequentialInner(projected, exec, env, logFn)
		}
		stageResults = append(stageResults, innerResults...)
		return stageResults, err
	}

	// 整 stage 模式（opts.StepName == ""，但 opts.StageName != ""）：
	// 让 stage 内 sequential step 失败后继续执行剩余 step，
	// 这样 --step <stage> 能完整跑完 stage。
	if opts.StageName != "" && !stage.Parallel {
		innerResults, err := runSequentialContinueOnError(stage, exec, env, logFn)
		return innerResults, err
	}

	if stage.Parallel {
		return runParallel(stage, exec, env, logFn)
	}
	return runSequential(stage, exec, env, logFn)
}

func runSequential(stage Stage, exec Executor, env map[string]string, logFn func(string)) ([]*PipelineResult, error) {
	return runSequentialInner(stage, exec, env, logFn)
}

func runSequentialInner(stage Stage, exec Executor, env map[string]string, logFn func(string)) ([]*PipelineResult, error) {
	var results []*PipelineResult
	for _, step := range stage.Steps {
		result := executeStep(stage.Name, step, exec, env, logFn)
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
func runSequentialContinueOnError(stage Stage, exec Executor, env map[string]string, logFn func(string)) ([]*PipelineResult, error) {
	var results []*PipelineResult
	var firstErr error
	for _, step := range stage.Steps {
		result := executeStep(stage.Name, step, exec, env, logFn)
		results = append(results, result)
		if result.Status == "failed" && firstErr == nil {
			firstErr = fmt.Errorf("step %q failed: %s", step.Name, result.Error)
		}
	}
	return results, firstErr
}

func runParallel(stage Stage, exec Executor, env map[string]string, logFn func(string)) ([]*PipelineResult, error) {
	return runParallelInner(stage, exec, env, logFn)
}

func runParallelInner(stage Stage, exec Executor, env map[string]string, logFn func(string)) ([]*PipelineResult, error) {
	results := make([]*PipelineResult, len(stage.Steps))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i := range stage.Steps {
		wg.Add(1)
		go func(idx int, step Step) {
			defer wg.Done()
			result := executeStep(stage.Name, step, exec, env, logFn)
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

func executeStep(stageName string, step Step, exec Executor, env map[string]string, logFn func(string)) *PipelineResult {
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
			logFn(fmt.Sprintf("[%s/%s] condition error: %v", stageName, step.Name, err))
			return result
		}
		if !ok {
			result.Status = "skipped"
			result.Output = "condition not met"
			logFn(fmt.Sprintf("[%s/%s] skipped (condition not met)", stageName, step.Name))
			return result
		}
	}

	logFn(fmt.Sprintf("[%s/%s] starting...", stageName, step.Name))

	start := time.Now()
	output, err := exec.Execute(step, env)
	elapsed := time.Since(start)

	result.Duration = elapsed.Truncate(time.Millisecond).String()

	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		logFn(fmt.Sprintf("[%s/%s] FAILED (%s): %s", stageName, step.Name, result.Duration, err.Error()))
	} else {
		result.Status = "success"
		result.Output = output
		logFn(fmt.Sprintf("[%s/%s] success (%s)", stageName, step.Name, result.Duration))
	}

	return result
}