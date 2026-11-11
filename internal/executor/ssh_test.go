package executor

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/hpgood/skating/internal/testssh"
)

// --- 测试（in-process SSH server 见 internal/testssh） ---

func TestResolveSSHPort(t *testing.T) {
	tests := []struct {
		name    string
		port    any
		env     map[string]string
		want    int
		wantErr bool
	}{
		{name: "empty defaults to 22", want: 22},
		{name: "int", port: 2222, want: 2222},
		{name: "int zero defaults to 22", port: 0, want: 22},
		{name: "numeric string", port: "2222", want: 2222},
		{name: "string zero defaults to 22", port: "0", want: 22},
		{name: "variable expansion", port: "${SSH_PORT}", env: map[string]string{"SSH_PORT": "2200"}, want: 2200},
		{name: "undefined variable", port: "${SSH_PORT}", wantErr: true},
		{name: "not a number", port: "abc", wantErr: true},
		{name: "int out of range", port: 70000, wantErr: true},
		{name: "string out of range", port: "70000", wantErr: true},
		{name: "negative int", port: -1, wantErr: true},
		{name: "negative string", port: "-1", wantErr: true},
		{name: "unsupported type", port: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSSHPort(Step{Name: "remote", Port: tt.port}, tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveSSHPort(%q) = %d, want error", tt.port, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSSHPort(%q): %v", tt.port, err)
			}
			if got != tt.want {
				t.Errorf("resolveSSHPort(%q) = %d, want %d", tt.port, got, tt.want)
			}
		})
	}
}

func TestRunner_ExecuteSsh_Password(t *testing.T) {
	addr := testssh.Start(t, testssh.PasswordConfig("testuser", "testpass"))
	host, portStr, _ := net.SplitHostPort(addr)

	r := NewRunner()
	got := map[string]string{}
	r.OnSetVar(func(k, v string) { got[k] = v })

	step := Step{
		Name: "remote", Type: "ssh",
		Host: host, Port: portStr, User: "testuser",
		Auth:   "password",
		Secret: "testpass",
		Script: "echo \"::set-var TV=hello\"\necho done",
	}
	out, err := r.Execute(step, "ignored", nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got["TV"] != "hello" {
		t.Errorf("set-var callback got %v, want TV=hello", got)
	}
	// 标记行不进返回输出；echo 执行结果正常进
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "::set-var ") {
			t.Errorf("marker leaked into output: %q", out)
		}
	}
	if !strings.Contains(out, "done") {
		t.Errorf("normal output lost: %q", out)
	}
}

func TestRunner_ExecuteSsh_KeyAuth(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(key, "test-key")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	pemBytes := string(pem.EncodeToMemory(block))

	addr := testssh.Start(t, testssh.KeyConfig(signer.PublicKey()))
	host, portStr, _ := net.SplitHostPort(addr)

	r := NewRunner()
	step := Step{
		Name: "remote", Type: "ssh",
		Host: host, Port: portStr, User: "testuser",
		Auth:   "key",
		Secret: pemBytes,
		Script: "echo ok",
	}
	out, err := r.Execute(step, "ignored", nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("output = %q, want contains ok", out)
	}
}

func TestRunner_ExecuteSsh_RemoteFailure(t *testing.T) {
	addr := testssh.Start(t, testssh.PasswordConfig("testuser", "testpass"))
	host, portStr, _ := net.SplitHostPort(addr)

	r := NewRunner()
	step := Step{
		Name: "remote", Type: "ssh",
		Host: host, Port: portStr, User: "testuser",
		Auth: "password", Secret: "testpass",
		Script: "echo boom\necho REMOTE-FAIL",
	}
	out, err := r.Execute(step, "ignored", nil, nil)
	if err == nil {
		t.Fatal("expected error from remote exit status 1")
	}
	if !strings.Contains(err.Error(), "remote exit status 1") {
		t.Errorf("err = %v, want remote exit status", err)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("output lost on failure: %q", out)
	}
}

func TestRunner_ExecuteSsh_BadPassword(t *testing.T) {
	addr := testssh.Start(t, testssh.PasswordConfig("testuser", "testpass"))
	host, portStr, _ := net.SplitHostPort(addr)

	r := NewRunner()
	step := Step{
		Name: "remote", Type: "ssh",
		Host: host, Port: portStr, User: "testuser",
		Auth: "password", Secret: "wrongpass",
		Script: "echo hi",
	}
	if _, err := r.Execute(step, "ignored", nil, nil); err == nil {
		t.Fatal("expected auth error")
	}
}

func TestRunner_ExecuteSsh_MissingFields(t *testing.T) {
	r := NewRunner()
	cases := []Step{
		{Name: "a", Type: "ssh", User: "u", Secret: "s"},                          // 缺 host
		{Name: "b", Type: "ssh", Host: "h", Secret: "s"},                          // 缺 user
		{Name: "c", Type: "ssh", Host: "h", User: "u"},                            // 缺 secret
		{Name: "d", Type: "ssh", Host: "h", User: "u", Secret: "s", Auth: "ldap"}, // 未知 auth
	}
	for _, step := range cases {
		if _, err := r.Execute(step, "", nil, nil); err == nil {
			t.Errorf("step %q: expected error", step.Name)
		}
	}
}

func TestBuildRemoteScript(t *testing.T) {
	step := Step{
		Script:  "echo start\n./deploy.sh",
		WorkDir: "/opt/app",
	}
	script := buildRemoteScript(step, map[string]string{
		"SKA_BUILD_ID": "42",
		"MSG":          "it's ok",
	})
	lines := strings.Split(script, "\n")
	if lines[0] != "export 'MSG=it'\\''s ok'" {
		t.Errorf("first line = %q, want export with quoted value", lines[0])
	}
	if lines[1] != "export 'SKA_BUILD_ID=42'" {
		t.Errorf("second line = %q, want sorted export", lines[1])
	}
	if lines[2] != "cd '/opt/app'" {
		t.Errorf("third line = %q, want cd", lines[2])
	}
	if !strings.HasSuffix(script, "./deploy.sh\n") {
		t.Errorf("script should end with user script + newline: %q", script)
	}

	// workdir 支持 ${VAR} 展开
	wd := Step{Script: "echo x", WorkDir: "/opt/${APP}"}
	got := buildRemoteScript(wd, map[string]string{"APP": "myapp"})
	if !strings.Contains(got, "cd '/opt/myapp'") {
		t.Errorf("workdir expansion failed: %q", got)
	}
	// 未定义变量保留原样
	wd2 := Step{Script: "echo x", WorkDir: "/opt/${NOT_SET}"}
	got2 := buildRemoteScript(wd2, map[string]string{})
	if !strings.Contains(got2, "cd '/opt/${NOT_SET}'") {
		t.Errorf("undefined var should stay as-is: %q", got2)
	}
}

// TestRunner_ExecuteSsh_VarExpansion 验证 host/user 支持 ${VAR} 展开：
// host 写成 "127.0.0.${SEG}"，user 写成 "${SSH_USER}"，均从 env 取值
func TestRunner_ExecuteSsh_VarExpansion(t *testing.T) {
	addr := testssh.Start(t, testssh.PasswordConfig("testuser", "testpass"))
	host, portStr, _ := net.SplitHostPort(addr)
	seg := host[strings.LastIndex(host, ".")+1:]

	r := NewRunner()
	step := Step{
		Name: "remote", Type: "ssh",
		Host: "127.0.0.${SEG}", Port: portStr,
		User: "${SSH_USER}",
		Auth: "password", Secret: "testpass",
		Script: "echo expanded-ok",
	}
	out, err := r.Execute(step, "ignored", map[string]string{
		"SEG":      seg,
		"SSH_USER": "testuser",
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "expanded-ok") {
		t.Errorf("output = %q, want contains expanded-ok", out)
	}

	// 变量缺失时保留原样，连接失败并暴露原始值便于排查
	step.Host = "no-such-${MISSING}.host"
	_, err = r.Execute(step, "ignored", map[string]string{"SSH_USER": "testuser"}, nil)
	if err == nil {
		t.Fatal("Execute with missing var should fail")
	}
	if !strings.Contains(err.Error(), "${MISSING}") {
		t.Errorf("error should expose original placeholder, got: %v", err)
	}
}

func TestSshQuote(t *testing.T) {
	cases := map[string]string{
		"abc":   "'abc'",
		"it's":  `'it'\''s'`,
		"a b":   "'a b'",
		"$HOME": "'$HOME'",
	}
	for in, want := range cases {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
