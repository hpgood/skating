package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

// yaegiPlugin 把 yaegi 解释器中的插件值适配为 Plugin 接口
// 通过反射调用 Name/Version/Init/Run 方法
type yaegiPlugin struct {
	name    string
	version string
	val     reflect.Value
}

func (p *yaegiPlugin) Name() string    { return p.name }
func (p *yaegiPlugin) Version() string { return p.version }

func (p *yaegiPlugin) Init() error {
	r := p.val.MethodByName("Init").Call(nil)
	if len(r) > 0 && !r[0].IsNil() {
		return r[0].Interface().(error)
	}
	return nil
}

func (p *yaegiPlugin) Run(ctx *Context) error {
	r := p.val.MethodByName("Run").Call([]reflect.Value{
		reflect.ValueOf(ctx.ProjectName),
		reflect.ValueOf(ctx.BuildID),
		reflect.ValueOf(ctx.Status),
		reflect.ValueOf(ctx.Duration),
		reflect.ValueOf(ctx.Output),
	})
	if len(r) > 0 && !r[0].IsNil() {
		return r[0].Interface().(error)
	}
	return nil
}

// LoadPlugins 扫描指定目录下的 .go 文件，用 yaegi 解释器加载插件
// 如果目录不存在则跳过（不报错）
// 加载失败时打印警告但不终止程序
func LoadPlugins(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read plugin dir %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}

		pluginPath := filepath.Join(dir, entry.Name())
		src, err := os.ReadFile(pluginPath)
		if err != nil {
			fmt.Printf("[plugin] warning: read %s: %v\n", entry.Name(), err)
			continue
		}

		i := interp.New(interp.Options{})
		i.Use(stdlib.Symbols)

		_, err = i.Eval(string(src))
		if err != nil {
			fmt.Printf("[plugin] warning: eval %s: %v\n", entry.Name(), err)
			continue
		}

		// yaegi 插件文件要求: package plug; 导出 var Plugin
		v, err := i.Eval("plug.Plugin")
		if err != nil {
			fmt.Printf("[plugin] warning: %s does not export 'plug.Plugin': %v\n", entry.Name(), err)
			continue
		}

		nameVal := v.MethodByName("Name")
		if !nameVal.IsValid() {
			fmt.Printf("[plugin] warning: %s: Plugin has no Name() method\n", entry.Name())
			continue
		}
		name := nameVal.Call(nil)[0].String()

		verMethod := v.MethodByName("Version")
		version := "0.0.0"
		if verMethod.IsValid() {
			version = verMethod.Call(nil)[0].String()
		}

		RegisterPlugin(&yaegiPlugin{
			name:    name,
			version: version,
			val:     v,
		})

		fmt.Printf("[plugin] loaded: %s v%s from %s\n", name, version, entry.Name())
	}

	return nil
}