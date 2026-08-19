package pipeline

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeExecutor 记录执行顺序和并发状态，可由测试配置返回值
type fakeExecutor struct {
	mu      sync.Mutex
	results map[string]fakeResult // step name -> result
	// 验证并发：每个 step 启动时记录时间戳
	startTimes map[string]time.Time
	endTimes   map[string]time.Time
}

type fakeResult struct {
	output string
	err    error
}

func newFakeExecutor() *fakeExecutor {
	return &fakeExecutor{
		results:    map[string]fakeResult{},
		startTimes: map[string]time.Time{},
		endTimes:   map[string]time.Time{},
	}
}

func (f *fakeExecutor) Execute(step Step, env map[string]string) (string, error) {
	f.mu.Lock()
	f.startTimes[step.Name] = time.Now()
	r, ok := f.results[step.Name]
	if !ok {
		r = fakeResult{output: "ok"}
	}
	f.mu.Unlock()

	// 模拟工作耗时
	time.Sleep(50 * time.Millisecond)

	f.mu.Lock()
	f.endTimes[step.Name] = time.Now()
	f.mu.Unlock()

	return r.output, r.err
}

func (f *fakeExecutor) setResult(name, output string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[name] = fakeResult{output: output, err: err}
}

func TestRunPipeline_SerialStage_StopsOnFailure(t *testing.T) {
	exec := newFakeExecutor()
	exec.setResult("step1", "ok", nil)
	exec.setResult("step2", "", fmt.Errorf("step2 failed"))
	exec.setResult("step3", "ok", nil)

	p := &Pipeline{
		Stages: []Stage{
			{
				Name: "build",
				Steps: []Step{
					{Name: "step1", Type: "shell"},
					{Name: "step2", Type: "shell"},
					{Name: "step3", Type: "shell"},
				},
			},
		},
	}

	results, err := RunPipeline(p, exec, map[string]string{"SKA_BUILD_ID": "1"}, func(s string) {}, nil)
	if err == nil {
		t.Fatal("expected error from failed step")
	}

	// step3 不应该被执行（serial 失败即停）
	if _, ran := exec.startTimes["step3"]; ran {
		t.Error("step3 should NOT have run after step2 failed")
	}

	// 应该只记录到 step1 和 step2
	if _, ran := exec.startTimes["step1"]; !ran {
		t.Error("step1 should have run")
	}
	if _, ran := exec.startTimes["step2"]; !ran {
		t.Error("step2 should have run")
	}

	// 结果数量 = 2（step1 success + step2 failed），不含 step3
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestRunPipeline_ParallelStage_RunsConcurrently(t *testing.T) {
	exec := newFakeExecutor()
	p := &Pipeline{
		Stages: []Stage{
			{
				Name:     "parallel-test",
				Parallel: true,
				Steps: []Step{
					{Name: "a", Type: "shell"},
					{Name: "b", Type: "shell"},
					{Name: "c", Type: "shell"},
				},
			},
		},
	}

	_, err := RunPipeline(p, exec, nil, func(s string) {}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// 3 个 step 都要执行
	for _, name := range []string{"a", "b", "c"} {
		if _, ran := exec.startTimes[name]; !ran {
			t.Errorf("step %s should have run", name)
		}
	}

	// 并行证据：最晚结束时间应远小于 3*50ms=150ms
	maxEnd := time.Time{}
	for _, end := range exec.endTimes {
		if end.After(maxEnd) {
			maxEnd = end
		}
	}
	minStart := time.Now()
	for _, start := range exec.startTimes {
		if start.Before(minStart) {
			minStart = start
		}
	}
	elapsed := maxEnd.Sub(minStart)
	// 串行 = 150ms；并行 ≈ 50ms；阈值设为 100ms 留 buffer
	if elapsed > 100*time.Millisecond {
		t.Errorf("parallel stage took %v (expected ~50ms), suggests serial execution", elapsed)
	}
}

func TestExecuteStep_WhenConditionSkips(t *testing.T) {
	exec := newFakeExecutor()
	step := Step{Name: "test", Type: "shell", When: "SKA_BUILD_ID == 99"}
	result := executeStep("stage", step, exec, map[string]string{"SKA_BUILD_ID": "1"}, func(s string) {})

	if result.Status != "skipped" {
		t.Errorf("expected skipped, got %s", result.Status)
	}
	if _, ran := exec.startTimes["test"]; ran {
		t.Error("skipped step should not be executed")
	}
}

func TestExecuteStep_WhenConditionPasses(t *testing.T) {
	exec := newFakeExecutor()
	step := Step{Name: "test", Type: "shell", When: "SKA_BUILD_ID == 1"}
	result := executeStep("stage", step, exec, map[string]string{"SKA_BUILD_ID": "1"}, func(s string) {})

	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
}

func TestExecuteStep_WhenConditionEvalError(t *testing.T) {
	exec := newFakeExecutor()
	step := Step{Name: "test", Type: "shell", When: "garbage no operator"}
	result := executeStep("stage", step, exec, map[string]string{}, func(s string) {})

	if result.Status != "failed" {
		t.Errorf("expected failed (eval error), got %s", result.Status)
	}
}

func TestRunPipeline_MultiStage_StopsAfterFailedStage(t *testing.T) {
	exec := newFakeExecutor()
	exec.setResult("bad", "", fmt.Errorf("bad"))

	p := &Pipeline{
		Stages: []Stage{
			{Name: "s1", Steps: []Step{{Name: "bad", Type: "shell"}}},
			{Name: "s2", Steps: []Step{{Name: "never", Type: "shell"}}},
		},
	}

	_, err := RunPipeline(p, exec, nil, func(s string) {}, nil)
	if err == nil {
		t.Fatal("expected error from s1 failure")
	}
	if _, ran := exec.startTimes["never"]; ran {
		t.Error("s2 should not run after s1 failure")
	}
}

func TestRunPipeline_AllSuccess(t *testing.T) {
	exec := newFakeExecutor()
	p := &Pipeline{
		Stages: []Stage{
			{Name: "s1", Steps: []Step{{Name: "a"}, {Name: "b"}}},
		},
	}

	results, err := RunPipeline(p, exec, nil, func(s string) {}, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "success" {
			t.Errorf("%s status = %s, want success", r.StepName, r.Status)
		}
	}
}