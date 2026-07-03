package config

import (
	"time"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Storage 存储
type Storage struct {
	Type          string             `mapstructure:"Type" validate:"required,oneof=disk oss"` // 存储类型
	PublicBaseURL string             `mapstructure:"PublicBaseURL"`                           // 文件对外访问域名
	Disk          *DiskStorageConfig `mapstructure:"Disk"`                                    // 本地磁盘存储配置
	OSS           *OSSStorageConfig  `mapstructure:"OSS"`                                     // 阿里云 OSS 对象存储配置
}

// DiskStorageConfig 本地磁盘存储配置
type DiskStorageConfig struct {
	RootDir string `mapstructure:"RootDir"` // 存储根目录（绝对路径，跨平台）
	BaseUrl string `mapstructure:"BaseUrl"` // 访问域名（用于 GetUrl：baseUrl + key）
}

// OSSStorageConfig 阿里云 OSS 对象存储配置
type OSSStorageConfig struct {
	AccessKeyID     string        `mapstructure:"AccessKeyID"`
	AccessKeySecret string        `mapstructure:"AccessKeySecret"`
	SecurityToken   string        `mapstructure:"SecurityToken"`
	Region          string        `mapstructure:"Region"`
	Bucket          string        `mapstructure:"Bucket"`
	Endpoint        string        `mapstructure:"Endpoint"`
	BaseDir         string        `mapstructure:"BaseDir"` // 对象 key 前缀
	DisableSSL      bool          `mapstructure:"DisableSSL"`
	UseCName        bool          `mapstructure:"UseCName"`
	Timeout         time.Duration `mapstructure:"Timeout"`
	MaxRetries      int           `mapstructure:"MaxRetries"`
}
