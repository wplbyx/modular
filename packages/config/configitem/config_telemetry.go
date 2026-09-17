package configitem

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Telemetry 遥测
type Telemetry struct {
	UseTLS      bool     `mapstructure:"UseTLS"`
	CAFile      string   `mapstructure:"CAFile"`
	CertFile    string   `mapstructure:"CertFile" validate:"required_with=KeyFile"`
	KeyFile     string   `mapstructure:"KeyFile" validate:"required_with=CertFile"`
	ServerName  string   `mapstructure:"ServerName"`
	Headers     []string `mapstructure:"Headers"` // key=value; credentials are never logged.
	Sampler     string   `mapstructure:"Sampler" validate:"omitempty,oneof=always never parentbased ratio"`
	SampleRatio float64  `mapstructure:"SampleRatio" validate:"gte=0,lte=1"`

	Logger string `mapstructure:"Logger"` // 日志输出
	Metric string `mapstructure:"Metric"` // 指标输出
	Tracer string `mapstructure:"Tracer"` // 链路输出
}

// Flags 返回遥测配置的命令行元数据。
func (Telemetry) Flags(prefix string) []FlagSpec {
	return []FlagSpec{
		{Name: flagName(prefix, "UseTLS"), Default: false, Usage: "Enable OTLP TLS"},
		{Name: flagName(prefix, "CAFile"), Default: "", Usage: "OTLP trusted CA file"},
		{Name: flagName(prefix, "CertFile"), Default: "", Usage: "OTLP client certificate"},
		{Name: flagName(prefix, "KeyFile"), Default: "", Usage: "OTLP client private key"},
		{Name: flagName(prefix, "ServerName"), Default: "", Usage: "OTLP TLS server name"},
		{Name: flagName(prefix, "Headers"), Default: []string(nil), Usage: "OTLP authentication headers (key=value)"},
		{Name: flagName(prefix, "Sampler"), Default: "always", Usage: "Trace sampler: always|never|parentbased|ratio"},
		{Name: flagName(prefix, "SampleRatio"), Default: float64(1), Usage: "Trace sampling ratio"},

		{Name: flagName(prefix, "Logger"), Default: "", Usage: "日志遥测输出"},
		{Name: flagName(prefix, "Metric"), Default: "", Usage: "指标遥测输出"},
		{Name: flagName(prefix, "Tracer"), Default: "", Usage: "链路遥测输出"},
	}
}
