package configitem

import (
	"time"

	"github.com/wplbyx/modular/packages/config"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// HTTP HTTP 服务器配置
type HTTP struct {
	Host              string        `mapstructure:"Host" validate:"required"`                    // 监听主机
	Port              int           `mapstructure:"Port" validate:"required,min=1000,max=65535"` // HTTP服务端口
	ReadHeaderTimeout time.Duration `mapstructure:"ReadHeaderTimeout"`                           // 读取请求头超时 default:"5s"
	ReadTimeout       time.Duration `mapstructure:"ReadTimeout"`                                 // 读取请求体超时 default:"30s"
	WriteTimeout      time.Duration `mapstructure:"WriteTimeout"`                                // 写入响应超时 default:"30s"
	IdleTimeout       time.Duration `mapstructure:"IdleTimeout"`                                 // 空闲超时    default:"120s"
	ShutdownTimeout   time.Duration `mapstructure:"ShutdownTimeout"`                             // 优雅关闭超时
	EnableTLS         bool          `mapstructure:"EnableTLS"`                                   // 是否启用TLS
	TLSKeyFile        string        `mapstructure:"TLSKeyFile"`                                  // TLS私钥文件路径
	TLSCertFile       string        `mapstructure:"TLSCertFile"`                                 // TLS证书文件路径
}

// Flags 返回 HTTP 配置的命令行元数据。
func (HTTP) Flags(prefix string) []config.FlagSpec {
	return []config.FlagSpec{
		{Name: prefix + ".host", Aliases: []string{"host"}, Default: "0.0.0.0", Usage: "HTTP监听主机"},
		{Name: prefix + ".port", Aliases: []string{"port"}, Default: 18000, Usage: "HTTP服务端口"},
		{Name: prefix + ".readheadertimeout", Default: 5 * time.Second, Usage: "读取请求头超时"},
		{Name: prefix + ".readtimeout", Default: 30 * time.Second, Usage: "读取请求体超时"},
		{Name: prefix + ".writetimeout", Default: 30 * time.Second, Usage: "写入响应超时"},
		{Name: prefix + ".idletimeout", Default: 120 * time.Second, Usage: "空闲连接超时"},
		{Name: prefix + ".shutdowntimeout", Default: 30 * time.Second, Usage: "HTTP优雅关闭超时"},
		{Name: prefix + ".enabletls", Default: false, Usage: "是否启用HTTP TLS"},
		{Name: prefix + ".tlskeyfile", Default: "", Usage: "HTTP TLS私钥文件路径"},
		{Name: prefix + ".tlscertfile", Default: "", Usage: "HTTP TLS证书文件路径"},
	}
}
