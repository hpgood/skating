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

// RunPipeline 按顺序执行 pipeline 的各个 stage。
// Stage.Parallel 为 true 时，该 stage 内的 steps 并行执行。
// 遇到任一步骤失败则停止后续步骤。
// logFn 用于实时输出日志。
func RunPipeline(p *Pipeline, exec Executor, env map[string]string, logFn func(string)) ([]*PipelineResult, error) {
	var results []*PipelineResult

	for _, stage := range p.Stages {
		stageResults, err := runStage(stage, exec, env, logFn)
		results = append(results, stageResults...)
		if err != nil {
			return results, err
		}
	}

	return results, nil
}

func runStage(stage Stage, exec Executor, env map[string]string, logFn func(string)) ([]*PipelineResult, error) {
	if stage.Parallel {
		return runParallel(stage, exec, env, logFn)
	}
	return runSequential(stage, exec, env, logFn)
}

func runSequential(stage Stage, exec Executor, env map[string]string, logFn func(string)) ([]*PipelineResult, error) {
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

func runParallel(stage Stage, exec Executor, env map[string]string, logFn func(string)) ([]*PipelineResult, error) {
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
		ok, err := EvalWhen(step.When, env, env)
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