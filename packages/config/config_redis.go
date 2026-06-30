package config

import (
	"time"
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
