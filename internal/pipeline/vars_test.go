package pipeline

import (
	"fmt"
	"sync"
	"testing"
)

func TestVarPool_SetAndMerge(t *testing.T) {
	env := map[string]string{"A": "1"}

	// nil 池安全：原样返回 env
	var nilPool *VarPool
	if got := nilPool.Merged(env); got["A"] != "1" || len(got) != 1 {
		t.Errorf("nil pool merged = %v, want unchanged env", got)
	}

	p := NewVarPool()
	// 池为空：内容不变
	if got := p.Merged(env); got["A"] != "1" || len(got) != 1 {
		t.Errorf("empty pool merged = %v, want unchanged env", got)
	}

	// Set 后合并：池变量覆盖同名 env；空 key 忽略
	p.Set("A", "overridden")
	p.Set("B", "2")
	p.Set("", "ignored")
	got := p.Merged(env)
	if got["A"] != "overridden" || got["B"] != "2" || len(got) != 2 {
		t.Errorf("merged = %v, want A=overridden B=2 (len 2)", got)
	}

	// 合并不得污染原始 env
	if env["A"] != "1" {
		t.Errorf("original env polluted: %v", env)
	}

	// 并发 Set 安全（-race 下验证）
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p.Set(fmt.Sprintf("K%d", i), "v")
		}(i)
	}
	wg.Wait()
	if got := p.Merged(nil); got["K3"] != "v" || got["K9"] != "v" {
		t.Errorf("concurrent sets lost: %v", got)
	}
}

// TestExecuteStep_VarPoolMerged 验证池变量注入 step env，且不污染共享 env
func TestExecuteStep_VarPoolMerged(t *testing.T) {
	exec := newFakeExecutor()
	vars := NewVarPool()
	vars.Set("MYVAR", "from-pool")
	step := Step{Name: "uses-pool", Type: "shell"}
	globalEnv := map[string]string{"SKA_BUILD_ID": "1"}

	result := executeStep("stage", step, exec, globalEnv, func(s string) {}, vars)
	if result.Status != "success" {
		t.Fatalf("expected success, got %s", result.Status)
	}

	got := exec.envs["uses-pool"]
	if got["MYVAR"] != "from-pool" {
		t.Errorf("pool var not merged into step env: %v", got)
	}
	if got["SKA_BUILD_ID"] != "1" {
		t.Errorf("global env lost: %v", got)
	}
	if _, leaked := globalEnv["MYVAR"]; leaked {
		t.Error("pool var leaked into shared global env")
	}
}

// TestExecuteStep_WhenSeesVarPool 验证 When 条件能读到池变量
func TestExecuteStep_WhenSeesVarPool(t *testing.T) {
	exec := newFakeExecutor()
	vars := NewVarPool()
	vars.Set("REL", "v2")

	step := Step{Name: "cond", Type: "shell", When: "REL == v2"}
	result := executeStep("stage", step, exec, map[string]string{}, func(s string) {}, vars)
	if result.Status != "success" {
		t.Errorf("When should see pool var, status = %s", result.Status)
	}

	// 值不匹配时走 skipped
	step2 := Step{Name: "cond2", Type: "shell", When: "REL == v3"}
	result2 := executeStep("stage", step2, exec, map[string]string{}, func(s string) {}, vars)
	if result2.Status != "skipped" {
		t.Errorf("expected skipped, got %s", result2.Status)
	}
}
