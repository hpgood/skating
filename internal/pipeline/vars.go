package pipeline

import (
	"maps"
	"sync"
)

// VarPool 构建级共享变量池：step 通过 "::set-var KEY=VALUE" 输出标记或
// Lua set_env 产生的变量，供后续 step（脚本环境变量与 When 条件）使用。
// 并行 stage 下多个 step 可能并发读写，内部以互斥锁保护。
type VarPool struct {
	mu sync.Mutex
	m  map[string]string
}

// NewVarPool 创建空的变量池
func NewVarPool() *VarPool {
	return &VarPool{m: map[string]string{}}
}

// Set 写入一个变量（并发安全）；nil 接收者或空 key 安全忽略
func (p *VarPool) Set(key, value string) {
	if p == nil || key == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[key] = value
}

// Merged 返回合并了池变量的 env 副本：池为空（或 p 为 nil）时原样返回 env
// 不做克隆；否则克隆 env 并以池变量覆盖（池变量优先级最高）。
// env 本身不会被修改。
func (p *VarPool) Merged(env map[string]string) map[string]string {
	if p == nil {
		return env
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.m) == 0 {
		return env
	}
	merged := maps.Clone(env)
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, p.m)
	return merged
}
