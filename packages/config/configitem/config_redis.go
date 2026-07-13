package configitem

import (
	"time"

	"github.com/wplbyx/modular/packages/config"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Redis 缓存配置
type Redis struct {
	Urls            []string      `mapstructure:"Urls"`            // 连接URL列表，优先使用
	Host            string        `mapstructure:"Host"`            // Redis主机
	Port            int           `mapstructure:"Port"`            // Redis端口
	Username        string        `mapstructure:"Username"`        // 用户名 (Redis 6.0+)
	Password        string        `mapstructure:"Password"`        // 密码
	Database        int           `mapstructure:"Database"`        // 数据库索引
	PoolSize        int           `mapstructure:"PoolSize"`        // 连接池大小
	MinIdleConn     int           `mapstructure:"MinIdleConn"`     // 最小空闲连接数
	DialTimeout     time.Duration `mapstructure:"DialTimeout"`     // 连接超时
	ReadTimeout     time.Duration `mapstructure:"ReadTimeout"`     // 读取超时
	WriteTimeout    time.Duration `mapstructure:"WriteTimeout"`    // 写入超时
	MaxRetries      int           `mapstructure:"MaxRetries"`      // 操作失败重试次数
	MinRetryBackoff uint32        `mapstructure:"MinRetryBackoff"` // 重试最小时间间隔
	MaxRetryBackoff uint32        `mapstructure:"MaxRetryBackoff"` // 重试最大时间间隔
}

// Flags 返回 Redis 配置的命令行元数据。
func (Redis) Flags(prefix string) []config.FlagSpec {
	return []config.FlagSpec{
		{Name: prefix + ".urls", Default: []string(nil), Usage: "Redis连接URL列表"},
		{Name: prefix + ".host", Default: "127.0.0.1", Usage: "Redis主机"},
		{Name: prefix + ".port", Default: 6379, Usage: "Redis端口"},
		{Name: prefix + ".username", Default: "", Usage: "Redis用户名"},
		{Name: prefix + ".password", Default: "", Usage: "Redis密码"},
		{Name: prefix + ".database", Default: 0, Usage: "Redis数据库索引"},
		{Name: prefix + ".poolsize", Default: 10, Usage: "Redis连接池大小"},
		{Name: prefix + ".minidleconn", Default: 5, Usage: "Redis最小空闲连接数"},
		{Name: prefix + ".dialtimeout", Default: 5 * time.Second, Usage: "Redis连接超时"},
		{Name: prefix + ".readtimeout", Default: 3 * time.Second, Usage: "Redis读取超时"},
		{Name: prefix + ".writetimeout", Default: 3 * time.Second, Usage: "Redis写入超时"},
		{Name: prefix + ".maxretries", Default: 3, Usage: "Redis操作失败重试次数"},
		{Name: prefix + ".minretrybackoff", Default: uint32(8), Usage: "Redis重试最小时间间隔"},
		{Name: prefix + ".maxretrybackoff", Default: uint32(512), Usage: "Redis重试最大时间间隔"},
	}
}
