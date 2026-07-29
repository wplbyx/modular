package configitem

import (
	"time"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// HTTP HTTP 服务器配置
type HTTP struct {
	Host              string        `mapstructure:"Host" validate:"required"`                     // 监听主机
	Port              int           `mapstructure:"Port" validate:"omitempty,min=1000,max=65535"` // HTTP服务端口，0 表示使用临时端口
	ReadHeaderTimeout time.Duration `mapstructure:"ReadHeaderTimeout"`                            // 读取请求头超时 default:"5s"
	ReadTimeout       time.Duration `mapstructure:"ReadTimeout"`                                  // 读取请求体超时 default:"30s"
	WriteTimeout      time.Duration `mapstructure:"WriteTimeout"`                                 // 写入响应超时 default:"30s"
	IdleTimeout       time.Duration `mapstructure:"IdleTimeout"`                                  // 空闲超时    default:"120s"
	ShutdownTimeout   time.Duration `mapstructure:"ShutdownTimeout"`                              // 优雅关闭超时
	EnableTLS         bool          `mapstructure:"EnableTLS"`                                    // 是否启用TLS
	TLSKeyFile        string        `mapstructure:"TLSKeyFile"`                                   // TLS私钥文件路径
	TLSCertFile       string        `mapstructure:"TLSCertFile"`                                  // TLS证书文件路径
}

// Flags 返回 HTTP 配置的命令行元数据。
func (HTTP) Flags(prefix string) []FlagSpec {
	return []FlagSpec{
		{Name: flagName(prefix, "Host"), Aliases: []string{"Host"}, Default: "0.0.0.0", Usage: "HTTP监听主机"},
		{Name: flagName(prefix, "Port"), Aliases: []string{"Port"}, Default: 18000, Usage: "HTTP服务端口"},
		{Name: flagName(prefix, "ReadHeaderTimeout"), Default: 5 * time.Second, Usage: "读取请求头超时"},
		{Name: flagName(prefix, "ReadTimeout"), Default: 30 * time.Second, Usage: "读取请求体超时"},
		{Name: flagName(prefix, "WriteTimeout"), Default: 30 * time.Second, Usage: "写入响应超时"},
		{Name: flagName(prefix, "IdleTimeout"), Default: 120 * time.Second, Usage: "空闲连接超时"},
		{Name: flagName(prefix, "ShutdownTimeout"), Default: 30 * time.Second, Usage: "HTTP优雅关闭超时"},
		{Name: flagName(prefix, "EnableTLS"), Default: false, Usage: "是否启用HTTP TLS"},
		{Name: flagName(prefix, "TLSKeyFile"), Default: "", Usage: "HTTP TLS私钥文件路径"},
		{Name: flagName(prefix, "TLSCertFile"), Default: "", Usage: "HTTP TLS证书文件路径"},
	}
}
