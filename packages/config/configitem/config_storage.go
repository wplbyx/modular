package configitem

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

// Flags 返回存储配置的命令行元数据。
func (Storage) Flags(prefix string) []FlagSpec {
	return []FlagSpec{
		{Name: flagName(prefix, "Type"), Default: "disk", Usage: "存储类型"},
		{Name: flagName(prefix, "PublicBaseURL"), Default: "", Usage: "文件对外访问域名"},
		{Name: flagName(prefix, "Disk.RootDir"), Default: "./storage/upload", Usage: "本地磁盘存储根目录"},
		{Name: flagName(prefix, "Disk.BaseUrl"), Default: "", Usage: "本地磁盘访问域名"},

		{Name: flagName(prefix, "OSS.AccessKeyID"), Default: "", Usage: "OSS AccessKey ID"},
		{Name: flagName(prefix, "OSS.AccessKeySecret"), Default: "", Usage: "OSS AccessKey Secret"},
		{Name: flagName(prefix, "OSS.SecurityToken"), Default: "", Usage: "OSS临时安全令牌"},
		{Name: flagName(prefix, "OSS.Region"), Default: "", Usage: "OSS区域"},
		{Name: flagName(prefix, "OSS.Bucket"), Default: "", Usage: "OSS Bucket"},
		{Name: flagName(prefix, "OSS.Endpoint"), Default: "", Usage: "OSS Endpoint"},
		{Name: flagName(prefix, "OSS.BaseDir"), Default: "", Usage: "OSS对象 key 前缀"},
		{Name: flagName(prefix, "OSS.DisableSSL"), Default: false, Usage: "是否禁用OSS SSL"},
		{Name: flagName(prefix, "OSS.UseCName"), Default: false, Usage: "是否使用OSS CNAME"},
		{Name: flagName(prefix, "OSS.Timeout"), Default: 60 * time.Second, Usage: "OSS请求超时"},
		{Name: flagName(prefix, "OSS.MaxRetries"), Default: 3, Usage: "OSS最大重试次数"},
	}
}
