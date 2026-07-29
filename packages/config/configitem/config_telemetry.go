package configitem

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Telemetry 遥测
type Telemetry struct {
	Logger string `mapstructure:"Logger"` // 日志输出
	Metric string `mapstructure:"Metric"` // 指标输出
	Tracer string `mapstructure:"Tracer"` // 链路输出
}

// Flags 返回遥测配置的命令行元数据。
func (Telemetry) Flags(prefix string) []FlagSpec {
	return []FlagSpec{
		{Name: flagName(prefix, "Logger"), Default: "", Usage: "日志遥测输出"},
		{Name: flagName(prefix, "Metric"), Default: "", Usage: "指标遥测输出"},
		{Name: flagName(prefix, "Tracer"), Default: "", Usage: "链路遥测输出"},
	}
}
