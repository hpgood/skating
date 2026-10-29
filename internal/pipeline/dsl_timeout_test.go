package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".skating.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// timeout 支持 duration 字符串与纯数字（秒）两种写法；缺省走 DefaultTimeout
func TestLoadConfigTimeout(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		want    time.Duration
		wantDef bool // 期望回落到 DefaultTimeout
	}{
		{
			name:    "no timeout field",
			yaml:    "name: a\npipeline:\n  stages:\n    - name: s\n      steps:\n        - name: x\n          type: shell\n          script: echo hi\n",
			wantDef: true,
		},
		{
			name: "duration string",
			yaml: "name: a\ntimeout: 5m\npipeline:\n  stages:\n    - name: s\n      steps:\n        - name: x\n          type: shell\n          script: echo hi\n",
			want: 5 * time.Minute,
		},
		{
			name: "seconds string",
			yaml: "name: a\ntimeout: \"90s\"\npipeline:\n  stages:\n    - name: s\n      steps:\n        - name: x\n          type: shell\n          script: echo hi\n",
			want: 90 * time.Second,
		},
		{
			name: "bare number as seconds",
			yaml: "name: a\ntimeout: 120\npipeline:\n  stages:\n    - name: s\n      steps:\n        - name: x\n          type: shell\n          script: echo hi\n",
			want: 120 * time.Second,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadConfig(writeTempConfig(t, tc.yaml))
			if err != nil {
				t.Fatal(err)
			}
			got := cfg.Timeout.Or(DefaultTimeout)
			want := tc.want
			if tc.wantDef {
				want = DefaultTimeout
			}
			if got != want {
				t.Fatalf("timeout = %v, want %v", got, want)
			}
		})
	}
}

// 非法 timeout 值应报错而不是静默忽略
func TestLoadConfigInvalidTimeout(t *testing.T) {
	yaml := "name: a\ntimeout: notaduration\npipeline:\n  stages:\n    - name: s\n      steps:\n        - name: x\n          type: shell\n          script: echo hi\n"
	if _, err := LoadConfig(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("expected error for invalid timeout, got nil")
	}
}

// ParseDuration 与 yaml timeout 字段共用解析规则：duration 字符串或纯数字（秒）。
func TestParseDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "10m", want: 10 * time.Minute},
		{in: "600s", want: 600 * time.Second},
		{in: "600", want: 600 * time.Second},
		{in: " 90s ", want: 90 * time.Second},
		{in: "1h30m", want: 90 * time.Minute},
		{in: "notaduration", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseDuration(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseDuration(%q) = %v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseDuration(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// step 级 image/docker_options 解析
func TestLoadConfigStepImageAndDockerOptions(t *testing.T) {
	yaml := `name: a
pipeline:
  stages:
    - name: build
      steps:
        - name: compile
          type: shell
          script: go build
        - name: pack
          type: shell
          image: node:20
          workdir: /build/ws
          docker_options:
            - --network
            - host
            - -v
            - /tmp/cache:/var/cache
          script: npm run build
`
	cfg, err := LoadConfig(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	steps := cfg.Pipeline.Stages[0].Steps
	if steps[0].Image != "" || len(steps[0].DockerOptions) != 0 || steps[0].WorkDir != "" {
		t.Fatalf("compile step should have no step-level image/options/workdir, got %q %v %q",
			steps[0].Image, steps[0].DockerOptions, steps[0].WorkDir)
	}

	pack := steps[1]
	if pack.Image != "node:20" {
		t.Fatalf("pack image = %q, want node:20", pack.Image)
	}
	if pack.WorkDir != "/build/ws" {
		t.Fatalf("pack workdir = %q, want /build/ws", pack.WorkDir)
	}
	wantOpts := []string{"--network", "host", "-v", "/tmp/cache:/var/cache"}
	if len(pack.DockerOptions) != len(wantOpts) {
		t.Fatalf("pack docker_options = %v, want %v", pack.DockerOptions, wantOpts)
	}
	for i := range wantOpts {
		if pack.DockerOptions[i] != wantOpts[i] {
			t.Fatalf("pack docker_options[%d] = %q, want %q", i, pack.DockerOptions[i], wantOpts[i])
		}
	}
}
