package config

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Logging 日志配置
type Logging struct {
	Level  string     `mapstructure:"Level" validate:"required,oneof=debug info warn error"` // 日志级别
	Output []string   `mapstructure:"Output"`                                                // 输出目标
	File   FileConfig `mapstructure:"File"`
	OTel   OTelConfig `mapstructure:"OTel"`
}

// FileConfig 是文件输出的配置
type FileConfig struct {
	Filename   string `mapstructure:"Filename"`   // 文件名 (当 output 为 file 时)
	MaxSize    int    `mapstructure:"MaxSize"`    // 单个日志文件最大大小
	MaxBackups int    `mapstructure:"MaxBackups"` // 保留的旧日志文件数量
	MaxAge     int    `mapstructure:"MaxAge"`     // 保留日志文件的最大天数
	Compress   bool   `mapstructure:"Compress"`   // 是否压缩/归档旧日志文件
	SplitRange string `mapstructure:"SplitRange"` // 日志分片逻辑：每天
}

// OTelConfig 是 OpenTelemetry 的配置
type OTelConfig struct {
	Endpoint string `mapstructure:"Endpoint"`
	Insecure bool   `mapstructure:"Insecure"`
}
