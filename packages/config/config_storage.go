package config

import (
	"os"
	"time"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Storage 存储
type Storage struct {
	Type          string              `mapstructure:"Type" validate:"required,oneof=local s3 minio oss"` // 存储类型
	PublicBaseURL string              `mapstructure:"PublicBaseURL"`                                     // 文件对外访问域名
	Local         *LocalStorageConfig `mapstructure:"Local"`                                             // 本地存储配置
	S3            *S3StorageConfig    `mapstructure:"S3"`                                                // AWS S3 或兼容服务配置
	Minio         *MinioStorageConfig `mapstructure:"Minio"`                                             // MinIO 对象存储配置
	OSS           *OSSStorageConfig   `mapstructure:"OSS"`                                               // 阿里云 OSS 对象存储配置
}

// LocalStorageConfig 本地文件系统存储配置
type LocalStorageConfig struct {
	RootPath       string        `mapstructure:"RootPath"`       // 存储根目录
	URLPath        string        `mapstructure:"URLPath"`        // 本地文件对外访问路径前缀
	Perm           os.FileMode   `mapstructure:"Perm"`           // 新建文件的默认权限
	CleanupTimeout time.Duration `mapstructure:"CleanupTimeout"` // 清理操作超时
}

// S3StorageConfig AWS S3 或 S3 兼容存储配置
type S3StorageConfig struct {
	AccessKeyID     string        `mapstructure:"AccessKeyID" validate:"required"`     // 访问密钥ID
	SecretAccessKey string        `mapstructure:"SecretAccessKey" validate:"required"` // 访问密钥
	Region          string        `mapstructure:"Region" validate:"required"`          // 存储桶所在区域
	Bucket          string        `mapstructure:"Bucket" validate:"required"`          // 存储桶名称
	Endpoint        string        `mapstructure:"Endpoint"`                            // 自定义端点 (用于MinIO等)
	DisableSSL      bool          `mapstructure:"DisableSSL"`                          // 是否禁用SSL
	ForcePathStyle  bool          `mapstructure:"ForcePathStyle"`                      // 是否强制路径样式
	Timeout         time.Duration `mapstructure:"Timeout"`                             // 请求超时
	MaxRetries      int           `mapstructure:"MaxRetries"`                          // 最大重试次数
	PartSize        int64         `mapstructure:"PartSize"`                            // 分块上传的块大小
}

// MinioStorageConfig MinIO 对象存储配置 (与S3高度兼容)
type MinioStorageConfig struct {
	AccessKeyID     string        `mapstructure:"AccessKeyID" validate:"required"`
	SecretAccessKey string        `mapstructure:"SecretAccessKey" validate:"required"`
	Region          string        `mapstructure:"Region" validate:"required"`
	Bucket          string        `mapstructure:"Bucket" validate:"required"`
	Endpoint        string        `mapstructure:"Endpoint" validate:"required"` // MinIO 必须提供 Endpoint
	DisableSSL      bool          `mapstructure:"DisableSSL"`
	ForcePathStyle  bool          `mapstructure:"ForcePathStyle"` // MinIO 通常需要
	Timeout         time.Duration `mapstructure:"Timeout"`
	MaxRetries      int           `mapstructure:"MaxRetries"`
	PartSize        int64         `mapstructure:"PartSize"`
}

// OSSStorageConfig 阿里云 OSS 对象存储配置
type OSSStorageConfig struct {
	AccessKeyID     string        `mapstructure:"AccessKeyID" validate:"required"`
	AccessKeySecret string        `mapstructure:"AccessKeySecret" validate:"required"`
	SecurityToken   string        `mapstructure:"SecurityToken"`
	Region          string        `mapstructure:"Region" validate:"required"`
	Bucket          string        `mapstructure:"Bucket" validate:"required"`
	Endpoint        string        `mapstructure:"Endpoint"`
	DisableSSL      bool          `mapstructure:"DisableSSL"`
	UseCName        bool          `mapstructure:"UseCName"`
	Timeout         time.Duration `mapstructure:"Timeout"`
	MaxRetries      int           `mapstructure:"MaxRetries"`
}
