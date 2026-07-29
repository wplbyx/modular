package config

import "github.com/wplbyx/modular/packages/config/configitem"

// FlagSpec 描述一个可由命令行覆盖的配置项。
//
// 类型由 configitem 持有，以保持叶子配置对象不反向依赖加载器包。
type FlagSpec = configitem.FlagSpec

// FlagProvider 由需要暴露命令行参数的配置对象实现。
type FlagProvider = configitem.FlagProvider
