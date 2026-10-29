package pipeline

import (
	"fmt"
	"runtime"
	"strings"
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
	// 记录每个 step 收到的 env，用于验证平台环境变量注入
	envs map[string]map[string]string
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
		envs:       map[string]map[string]string{},
	}
}

func (f *fakeExecutor) Execute(step Step, env map[string]string, logFn func(string)) (string, error) {
	f.mu.Lock()
	f.startTimes[step.Name] = time.Now()
	f.envs[step.Name] = env
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

	results, err := RunPipeline(p, exec, map[string]string{"SKA_BUILD_ID": "1"}, func(s string) {}, nil, nil)
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

	_, err := RunPipeline(p, exec, nil, func(s string) {}, nil, nil)
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
	result := executeStep("stage", step, exec, map[string]string{"SKA_BUILD_ID": "1"}, func(s string) {}, nil)

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
	result := executeStep("stage", step, exec, map[string]string{"SKA_BUILD_ID": "1"}, func(s string) {}, nil)

	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
}

func TestExecuteStep_WhenConditionEvalError(t *testing.T) {
	exec := newFakeExecutor()
	step := Step{Name: "test", Type: "shell", When: "garbage no operator"}
	result := executeStep("stage", step, exec, map[string]string{}, func(s string) {}, nil)

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

	_, err := RunPipeline(p, exec, nil, func(s string) {}, nil, nil)
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

	results, err := RunPipeline(p, exec, nil, func(s string) {}, nil, nil)
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

func TestRunPipeline_GroupSelectsSubset(t *testing.T) {
	exec := newFakeExecutor()
	p := &Pipeline{
		Stages: []Stage{
			{Name: "s1", Steps: []Step{{Name: "a", Type: "shell"}}},
			{Name: "s2", Steps: []Step{{Name: "b", Type: "shell"}}},
			{Name: "s3", Steps: []Step{{Name: "c", Type: "shell"}}},
		},
	}
	opts := &RunOptions{Stages: map[string]bool{"s1": true, "s3": true}}

	results, err := RunPipeline(p, exec, nil, func(string) {}, opts, nil)
	if err != nil {
		t.Fatalf("group subset run: %v", err)
	}

	if _, ran := exec.startTimes["a"]; !ran {
		t.Error("s1 should have run")
	}
	if _, ran := exec.startTimes["b"]; ran {
		t.Error("s2 should NOT have run (not selected by group)")
	}
	if _, ran := exec.startTimes["c"]; !ran {
		t.Error("s3 should have run")
	}

	status := map[string]string{}
	reason := map[string]string{}
	for _, r := range results {
		key := r.StageName + "/" + r.StepName
		status[key] = r.Status
		reason[key] = r.Output
	}
	if status["s1/a"] != "success" || status["s3/c"] != "success" {
		t.Errorf("selected stages should succeed: %v", status)
	}
	if status["s2/b"] != "skipped" {
		t.Errorf("s2 should be skipped, got %q", status["s2/b"])
	}
	if reason["s2/b"] != "stage not selected by --group filter" {
		t.Errorf("s2 skip reason = %q, want group-filter reason", reason["s2/b"])
	}
}

func TestRunPipeline_GroupStopsOnFailure(t *testing.T) {
	exec := newFakeExecutor()
	exec.setResult("a", "", fmt.Errorf("a failed"))
	p := &Pipeline{
		Stages: []Stage{
			{Name: "s1", Steps: []Step{{Name: "a", Type: "shell"}}},
			{Name: "s2", Steps: []Step{{Name: "b", Type: "shell"}}},
			{Name: "s3", Steps: []Step{{Name: "c", Type: "shell"}}},
		},
	}
	opts := &RunOptions{Stages: map[string]bool{"s1": true, "s2": true, "s3": true}}

	results, err := RunPipeline(p, exec, nil, func(string) {}, opts, nil)
	if err == nil {
		t.Fatal("expected error from failed stage s1")
	}
	if _, ran := exec.startTimes["b"]; ran {
		t.Error("s2 should NOT run after s1 failure")
	}
	if _, ran := exec.startTimes["c"]; ran {
		t.Error("s3 should NOT run after s1 failure")
	}

	status := map[string]string{}
	reason := map[string]string{}
	for _, r := range results {
		key := r.StageName + "/" + r.StepName
		status[key] = r.Status
		reason[key] = r.Output
	}
	if status["s1/a"] != "failed" {
		t.Errorf("s1/a status = %q, want failed", status["s1/a"])
	}
	if status["s2/b"] != "skipped" || status["s3/c"] != "skipped" {
		t.Errorf("stages after failure should be skipped: %v", status)
	}
	if reason["s2/b"] != "skipped after earlier stage failure" {
		t.Errorf("s2 skip reason = %q, want earlier-failure reason", reason["s2/b"])
	}
}

func TestStep_ResolveEnv_PlatformGroups(t *testing.T) {
	step := Step{
		Name: "compile",
		Env: map[string]map[string]string{
			"default": {"myEnv": "abc", "GOARCH": "amd64"},
			"windows": {"GOOS": "windows"},
			"linux":   {"GOOS": "linux"},
		},
	}

	// windows 平台：default + windows
	w := step.ResolveEnv("windows")
	if w["myEnv"] != "abc" || w["GOARCH"] != "amd64" || w["GOOS"] != "windows" {
		t.Errorf("windows env = %v, want myEnv=abc GOARCH=amd64 GOOS=windows", w)
	}
	if len(w) != 3 {
		t.Errorf("windows env len = %d, want 3", len(w))
	}

	// linux 平台：default + linux
	l := step.ResolveEnv("linux")
	if l["myEnv"] != "abc" || l["GOOS"] != "linux" {
		t.Errorf("linux env = %v, want myEnv=abc GOOS=linux", l)
	}
	if len(l) != 3 {
		t.Errorf("linux env len = %d, want 3", len(l))
	}

	// darwin 平台：仅 default
	d := step.ResolveEnv("darwin")
	if d["myEnv"] != "abc" || d["GOOS"] != "" {
		t.Errorf("darwin env = %v, want only default group", d)
	}

	// 平台专属变量覆盖 default 同名变量
	ov := Step{Env: map[string]map[string]string{
		"default": {"X": "base"},
		"linux":   {"X": "linux-value"},
	}}
	if got := ov.ResolveEnv("linux")["X"]; got != "linux-value" {
		t.Errorf("platform override failed: X = %q, want linux-value", got)
	}

	// 空 env 返回 nil
	if empty := (Step{}).ResolveEnv("windows"); empty != nil {
		t.Errorf("empty step env = %v, want nil", empty)
	}
}

func TestExecuteStep_InjectsPlatformEnv(t *testing.T) {
	exec := newFakeExecutor()
	step := Step{
		Name: "compile",
		Env: map[string]map[string]string{
			"default": {"myEnv": "abc"},
			"windows": {"GOOS": "windows"},
			"linux":   {"GOOS": "linux"},
		},
	}
	globalEnv := map[string]string{"SKA_BUILD_ID": "1"}
	result := executeStep("stage", step, exec, globalEnv, func(s string) {}, nil)

	if result.Status != "success" {
		t.Fatalf("expected success, got %s", result.Status)
	}

	got := exec.envs["compile"]
	if got["myEnv"] != "abc" {
		t.Errorf("default group not injected: %v", got)
	}
	if got["SKA_BUILD_ID"] != "1" {
		t.Errorf("global env lost: %v", got)
	}
	// 当前编译平台的分组应被注入（windows 或 linux 二选一）
	plat, hasPlat := got["GOOS"]
	if !hasPlat || (plat != runtime.GOOS) {
		t.Errorf("platform group not injected: GOOS = %q, want %q", plat, runtime.GOOS)
	}

	// 共享的全局 env 不能被 step env 污染
	if _, leaked := globalEnv["myEnv"]; leaked {
		t.Error("step env leaked into shared global env")
	}
}

func TestStage_ResolveEnv_PlatformGroups(t *testing.T) {
	st := Stage{
		Name: "build",
		Env: map[string]map[string]string{
			"default": {"version": "0.2.1"},
			"windows": {"Ext": ".exe"},
			"linux":   {"Ext": ""},
		},
	}

	// windows 平台：default + windows
	w := st.ResolveEnv("windows")
	if w["version"] != "0.2.1" || w["Ext"] != ".exe" {
		t.Errorf("windows env = %v, want version=0.2.1 Ext=.exe", w)
	}

	// linux 平台：default + linux（空值变量同样注入）
	l := st.ResolveEnv("linux")
	if l["version"] != "0.2.1" {
		t.Errorf("linux env = %v, want version=0.2.1", l)
	}
	if ext, ok := l["Ext"]; !ok || ext != "" {
		t.Errorf("linux env Ext = %q (exists %v), want empty string", ext, ok)
	}

	// darwin 平台：仅 default，无 Ext 键
	d := st.ResolveEnv("darwin")
	if d["version"] != "0.2.1" {
		t.Errorf("darwin env = %v, want version=0.2.1", d)
	}
	if _, ok := d["Ext"]; ok {
		t.Errorf("darwin env should not contain Ext: %v", d)
	}

	// 空 env 返回 nil
	if empty := (Stage{}).ResolveEnv("windows"); empty != nil {
		t.Errorf("empty stage env = %v, want nil", empty)
	}
}

func TestRunPipeline_StageEnvSharedBySteps(t *testing.T) {
	exec := newFakeExecutor()
	pl := &Pipeline{Stages: []Stage{{
		Name: "build",
		Env: map[string]map[string]string{
			"default": {"version": "0.2.1", "shared": "from-stage"},
		},
		Steps: []Step{
			{Name: "plain", Type: "shell"},
			// step env 覆盖 stage env 同名变量
			{Name: "override", Type: "shell", Env: map[string]map[string]string{
				"default": {"shared": "from-step"},
			}},
			// When 条件使用 stage env 变量
			{Name: "cond", Type: "shell", When: "version == \"0.2.1\""},
		},
	}}}
	globalEnv := map[string]string{"SKA_BUILD_ID": "1"}

	results, err := RunPipeline(pl, exec, globalEnv, func(string) {}, nil, nil)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	for _, r := range results {
		if r.Status != "success" {
			t.Errorf("%s status = %s (err %s), want success", r.StepName, r.Status, r.Error)
		}
	}

	// stage env 注入到每个 step
	if got := exec.envs["plain"]["version"]; got != "0.2.1" {
		t.Errorf("plain version = %q, want 0.2.1 (stage env not shared)", got)
	}
	if got := exec.envs["plain"]["shared"]; got != "from-stage" {
		t.Errorf("plain shared = %q, want from-stage", got)
	}
	// step env 覆盖 stage env 同名变量
	if got := exec.envs["override"]["shared"]; got != "from-step" {
		t.Errorf("override shared = %q, want from-step (step env should win)", got)
	}
	// When 条件可读取 stage env（cond 未被跳过即视为通过）
	if results[2].Status == "skipped" {
		t.Error("cond step skipped: When cannot see stage env")
	}
	// 共享的全局 env 不能被 stage env 污染
	if _, leaked := globalEnv["version"]; leaked {
		t.Error("stage env leaked into shared global env")
	}
}

// TestExecuteStep_EnvValueInterpolation 验证 step env 值支持 ${VAR}/$VAR 展开与拼接，
// 引用可来自构建 env、step env 内的其他键（含引用链）。
func TestExecuteStep_EnvValueInterpolation(t *testing.T) {
	exec := newFakeExecutor()
	step := Step{
		Name: "compile",
		Env: map[string]map[string]string{
			"default": {
				"version": "0.2.${SKA_BUILD_ID}",
				"label":   "v${version}-${GOOS}",
			},
		},
	}
	globalEnv := map[string]string{"SKA_BUILD_ID": "7", "GOOS": "linux"}

	result := executeStep("stage", step, exec, globalEnv, func(string) {}, nil)
	if result.Status != "success" {
		t.Fatalf("expected success, got %s", result.Status)
	}

	got := exec.envs["compile"]
	if got["version"] != "0.2.7" {
		t.Errorf("version = %q, want 0.2.7", got["version"])
	}
	// ${version} 是 step env 内的键，${GOOS} 来自构建 env，需展开后再拼接
	if got["label"] != "v0.2.7-linux" {
		t.Errorf("label = %q, want v0.2.7-linux", got["label"])
	}
	// 共享的构建 env 不能被污染
	if _, leaked := globalEnv["version"]; leaked {
		t.Errorf("step env leaked into shared global env: %v", globalEnv)
	}
}

// TestRunPipeline_StageEnvInterpolation 验证 stage 级 env 值支持跨引用构建 env 展开。
func TestRunPipeline_StageEnvInterpolation(t *testing.T) {
	exec := newFakeExecutor()
	pl := &Pipeline{Stages: []Stage{{
		Name: "build",
		Env: map[string]map[string]string{
			"default": {"version": "0.2.${SKA_BUILD_ID}"},
		},
		Steps: []Step{{Name: "compile", Type: "shell"}},
	}}}
	globalEnv := map[string]string{"SKA_BUILD_ID": "9"}

	if _, err := RunPipeline(pl, exec, globalEnv, func(string) {}, nil, nil); err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if got := exec.envs["compile"]["version"]; got != "0.2.9" {
		t.Errorf("stage env version = %q, want 0.2.9", got)
	}
	if _, leaked := globalEnv["version"]; leaked {
		t.Errorf("stage env leaked into shared global env: %v", globalEnv)
	}
}

// resultsByKey 把结果按 stage/step 建索引，便于断言状态与跳过原因。
func resultsByKey(results []*PipelineResult) (map[string]string, map[string]string) {
	status := map[string]string{}
	reason := map[string]string{}
	for _, r := range results {
		key := r.StageName + "/" + r.StepName
		status[key] = r.Status
		reason[key] = r.Output
	}
	return status, reason
}

func TestRunPipeline_StageWhenFalseSkipsStage(t *testing.T) {
	exec := newFakeExecutor()
	p := &Pipeline{
		Stages: []Stage{{
			Name: "deploy",
			When: "false",
			Steps: []Step{
				{Name: "push", Type: "shell"},
				{Name: "notify", Type: "shell"},
			},
		}},
	}
	globalEnv := map[string]string{"SKA_BUILD_ID": "1"}

	results, err := RunPipeline(p, exec, globalEnv, func(string) {}, nil, nil)
	if err != nil {
		t.Fatalf("disabled stage should not fail the build: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected one result per step, got %d", len(results))
	}
	status, reason := resultsByKey(results)
	for _, key := range []string{"deploy/push", "deploy/notify"} {
		if status[key] != "skipped" {
			t.Errorf("%s status = %q, want skipped", key, status[key])
		}
		if reason[key] != "stage condition not met" {
			t.Errorf("%s reason = %q, want %q", key, reason[key], "stage condition not met")
		}
	}
	if len(exec.startTimes) != 0 {
		t.Errorf("no step should execute, got %v", exec.startTimes)
	}
	// 共享构建 env 不能被 stage when 求值污染
	if _, leaked := globalEnv["branch"]; leaked {
		t.Errorf("git context leaked into shared env: %v", globalEnv)
	}
}

func TestRunPipeline_StageWhenTrueRunsSteps(t *testing.T) {
	exec := newFakeExecutor()
	p := &Pipeline{
		Stages: []Stage{{
			Name:  "build",
			When:  "TRUE",
			Steps: []Step{{Name: "compile", Type: "shell"}},
		}},
	}

	results, err := RunPipeline(p, exec, nil, func(string) {}, nil, nil)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if results[0].Status != "success" {
		t.Errorf("status = %q, want success", results[0].Status)
	}
	if _, ran := exec.startTimes["compile"]; !ran {
		t.Error("compile should have run")
	}
}

func TestRunPipeline_StageWhenExpression(t *testing.T) {
	exec := newFakeExecutor()
	p := &Pipeline{
		Stages: []Stage{
			{
				Name: "windows-only",
				When: "SKA_OS == windows",
				Env:  map[string]map[string]string{"default": {"SKA_OS": "linux"}},
				Steps: []Step{
					{Name: "a", Type: "shell"},
				},
			},
			{
				Name: "stage-env-visible",
				When: `version == "0.2.1"`,
				Env:  map[string]map[string]string{"default": {"version": "0.2.1"}},
				Steps: []Step{
					{Name: "b", Type: "shell"},
				},
			},
		},
	}
	globalEnv := map[string]string{"SKA_OS": "windows"}

	results, err := RunPipeline(p, exec, globalEnv, func(string) {}, nil, nil)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	status, _ := resultsByKey(results)
	// stage env 覆盖构建 env，条件因此不成立
	if status["windows-only/a"] != "skipped" {
		t.Errorf("windows-only/a = %q, want skipped", status["windows-only/a"])
	}
	if status["stage-env-visible/b"] != "success" {
		t.Errorf("stage-env-visible/b = %q, want success", status["stage-env-visible/b"])
	}
}

func TestRunPipeline_StageWhenSeesVarPool(t *testing.T) {
	exec := newFakeExecutor()
	pool := NewVarPool()
	pool.Set("deploy_target", "prod")
	p := &Pipeline{
		Stages: []Stage{
			{Name: "build", Steps: []Step{{Name: "compile", Type: "shell"}}},
			{
				Name:  "deploy",
				When:  `deploy_target == "prod"`,
				Steps: []Step{{Name: "push", Type: "shell"}},
			},
		},
	}

	results, err := RunPipeline(p, exec, nil, func(string) {}, nil, pool)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	status, _ := resultsByKey(results)
	if status["deploy/push"] != "success" {
		t.Errorf("deploy/push = %q, want success (var pool not visible)", status["deploy/push"])
	}
}

func TestRunPipeline_StageWhenEvalError(t *testing.T) {
	exec := newFakeExecutor()
	p := &Pipeline{
		Stages: []Stage{{
			Name: "deploy",
			When: "garbage no operator",
			Steps: []Step{
				{Name: "push", Type: "shell"},
				{Name: "notify", Type: "shell"},
			},
		}},
	}

	results, err := RunPipeline(p, exec, nil, func(string) {}, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid stage when condition")
	}
	if len(results) != 2 {
		t.Fatalf("expected one result per step, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "failed" {
			t.Errorf("%s/%s status = %q, want failed", r.StageName, r.StepName, r.Status)
		}
		if !strings.Contains(r.Error, "eval stage when") {
			t.Errorf("%s/%s error = %q, want eval stage when prefix", r.StageName, r.StepName, r.Error)
		}
	}
	if len(exec.startTimes) != 0 {
		t.Errorf("no step should execute, got %v", exec.startTimes)
	}
}

func TestRunPipeline_GroupContinuesAfterDisabledStage(t *testing.T) {
	exec := newFakeExecutor()
	p := &Pipeline{
		Stages: []Stage{
			{Name: "s1", Steps: []Step{{Name: "a", Type: "shell"}}},
			{Name: "s2", When: "false", Steps: []Step{{Name: "b", Type: "shell"}}},
			{Name: "s3", Steps: []Step{{Name: "c", Type: "shell"}}},
		},
	}
	opts := &RunOptions{Stages: map[string]bool{"s1": true, "s2": true, "s3": true}}

	results, err := RunPipeline(p, exec, nil, func(string) {}, opts, nil)
	if err != nil {
		t.Fatalf("disabled stage should not fail the group run: %v", err)
	}
	status, _ := resultsByKey(results)
	if status["s1/a"] != "success" || status["s3/c"] != "success" {
		t.Errorf("stages around disabled one should run: %v", status)
	}
	if status["s2/b"] != "skipped" {
		t.Errorf("s2/b = %q, want skipped", status["s2/b"])
	}
	if _, ran := exec.startTimes["b"]; ran {
		t.Error("disabled stage step should not run")
	}
}

func TestRunPipeline_StepFilterCannotBypassStageWhen(t *testing.T) {
	exec := newFakeExecutor()
	p := &Pipeline{
		Stages: []Stage{{
			Name: "deploy",
			When: "false",
			Steps: []Step{
				{Name: "push", Type: "shell"},
				{Name: "notify", Type: "shell"},
			},
		}},
	}
	opts := &RunOptions{StageName: "deploy", StepName: "push"}

	results, err := RunPipeline(p, exec, nil, func(string) {}, opts, nil)
	if err != nil {
		t.Fatalf("disabled stage should not fail the build: %v", err)
	}
	status, _ := resultsByKey(results)
	if status["deploy/push"] != "skipped" {
		t.Errorf("deploy/push = %q, want skipped", status["deploy/push"])
	}
	if len(exec.startTimes) != 0 {
		t.Errorf("no step should execute, got %v", exec.startTimes)
	}
}

func TestRunPipeline_ParallelStageWhenFalseStartsNoGoroutines(t *testing.T) {
	exec := newFakeExecutor()
	p := &Pipeline{
		Stages: []Stage{{
			Name:     "parallel-deploy",
			Parallel: true,
			When:     "false",
			Steps: []Step{
				{Name: "a", Type: "shell"},
				{Name: "b", Type: "shell"},
			},
		}},
	}

	results, err := RunPipeline(p, exec, nil, func(string) {}, nil, nil)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "skipped" {
			t.Errorf("%s status = %q, want skipped", r.StepName, r.Status)
		}
	}
	if len(exec.startTimes) != 0 {
		t.Errorf("no step should execute, got %v", exec.startTimes)
	}
}
