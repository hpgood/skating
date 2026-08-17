package i18n

// Lang 语言类型
type Lang string

const (
	EN   Lang = "en"
	ZhCN Lang = "zh-CN"
)

// LangPtr 全局语言设置，由命令行 --lang 标志设置
var LangPtr Lang = EN

// SetLang 设置全局语言
func SetLang(lang string) {
	switch lang {
	case "zh-CN", "zh":
		LangPtr = ZhCN
	default:
		LangPtr = EN
	}
}

// T 翻译键，返回对应语言的文本
func T(zh, en string) string {
	if LangPtr == ZhCN {
		return zh
	}
	return en
}

// IsZhCN 判断当前是否为中文
func IsZhCN() bool {
	return LangPtr == ZhCN
}
