package configitem

import "strings"

// FlagSpec 描述一个可由命令行覆盖的配置项。
type FlagSpec struct {
	Name      string
	Aliases   []string
	Shorthand string
	Default   any
	Usage     string
}

// FlagProvider 由需要暴露命令行参数的配置对象实现。
type FlagProvider interface {
	Flags(prefix string) []FlagSpec
}

func flagName(prefix, name string) string {
	prefix = strings.Trim(prefix, ".")
	name = strings.Trim(name, ".")
	if prefix == "" {
		return name
	}
	if name == "" {
		return prefix
	}
	return prefix + "." + name
}
