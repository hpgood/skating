package main

import (
	"fmt"
	"os"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	secretValue    string // --value 直接指定值
	secretFromFile string // --from-file 从文件读取（如私钥）
)

var secretCmd = &cobra.Command{
	Use: "secret",
	Short: i18n.T("管理全局认证信息（~/.skating/secrets.json，yaml 中仅引用名字）",
		"manage global credentials (~/.skating/secrets.json; referenced by name in yaml)"),
}

var secretSetCmd = &cobra.Command{
	Use: "set <name>",
	Short: i18n.T("设置一条 secret（无 --value/--from-file 时终端隐藏输入）",
		"set a secret (hidden prompt when --value/--from-file is omitted)"),
	Args: cobra.ExactArgs(1),
	RunE: runSecretSet,
}

var secretListCmd = &cobra.Command{
	Use:   "list",
	Short: i18n.T("列出全部 secret 名字（不显示值）", "list secret names (values are never shown)"),
	Args:  cobra.NoArgs,
	RunE:  runSecretList,
}

var secretDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: i18n.T("删除一条 secret", "delete a secret"),
	Args:  cobra.ExactArgs(1),
	RunE:  runSecretDelete,
}

func init() {
	secretSetCmd.Flags().StringVar(&secretValue, "value", "",
		i18n.T("直接指定值（注意会进 shell 历史）", "value inline (beware shell history)"))
	secretSetCmd.Flags().StringVar(&secretFromFile, "from-file", "",
		i18n.T("从文件读取值（如私钥 ~/.ssh/id_rsa）", "read value from file (e.g. key ~/.ssh/id_rsa)"))
	secretCmd.AddCommand(secretSetCmd, secretListCmd, secretDeleteCmd)
}

func newStoreOrExit() *store.Store {
	s, err := store.NewStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, i18n.T("错误: %v\n", "Error: %v\n"), err)
		os.Exit(1)
	}
	return s
}

func runSecretSet(cmd *cobra.Command, args []string) error {
	s := newStoreOrExit()
	name := args[0]

	var value string
	switch {
	case secretValue != "" && secretFromFile != "":
		return fmt.Errorf("%s", i18n.T("--value 与 --from-file 只能选一个", "--value and --from-file are mutually exclusive"))
	case secretValue != "":
		value = secretValue
	case secretFromFile != "":
		data, err := os.ReadFile(secretFromFile)
		if err != nil {
			return fmt.Errorf(i18n.T("读取文件失败: %w", "read file failed: %w"), err)
		}
		value = string(data)
	default:
		// 终端隐藏输入，不回显、不进 shell 历史
		fmt.Print(i18n.T("输入值（不回显）: ", "Enter value (hidden): "))
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return fmt.Errorf(i18n.T("读取输入失败: %w", "read input failed: %w"), err)
		}
		value = string(b)
	}

	if err := s.SetSecret(name, value); err != nil {
		return err
	}
	fmt.Println(i18n.T("已保存 secret ", "saved secret ") + name)
	return nil
}

func runSecretList(cmd *cobra.Command, args []string) error {
	s := newStoreOrExit()
	list, err := s.ListSecrets()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println(i18n.T("(空，用 skating secret set <name> 创建)", "(empty, create one via skating secret set <name>)"))
		return nil
	}
	for _, e := range list {
		fmt.Printf("%s  (updated %s)\n", e.Name, e.UpdatedAt)
	}
	return nil
}

func runSecretDelete(cmd *cobra.Command, args []string) error {
	s := newStoreOrExit()
	name := args[0]
	if err := s.RemoveSecret(name); err != nil {
		return err
	}
	fmt.Println(i18n.T("已删除 secret ", "deleted secret ") + name)
	return nil
}
