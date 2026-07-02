package storage

import (
	"context"
	"fmt"
	"io"
)

// ==========================================
// 核心模型与结构体
// ==========================================

// ObjectItem 代表遍历或列举文件时返回的通用元信息。
type ObjectItem struct {
	Key          string // 文件相对路径
	Size         int64  // 字节数
	LastModified int64  // 秒级 Unix 时间戳
}

// UploadTask 用于批量上传时的单个任务定义。
type UploadTask struct {
	Key  string    // 目标存储路径
	Body io.Reader // 文件内容流
}

// ListCallback 定义遍历文件时的流式回调：items 为当前页（通常最多 1000 个），
// 业务方返回 error 可主动中断后续遍历。
type ListCallback func(ctx context.Context, items ...ObjectItem) error

// MultipartUploadSession 代表一个分片上传会话凭证。
type MultipartUploadSession struct {
	UploadID string // 分片上传事件 ID（云厂商生成；本地磁盘用 UUID 模拟）
	Key      string // 目标对象 key（OSS 下为完整 objectKey）
}

// UploadPartResponse 代表单个分片上传成功后的元数据。
type UploadPartResponse struct {
	PartNumber int    // 分片号（从 1 开始）
	ETag       string // 分片校验码
}

// ==========================================
// Functional Options
// ==========================================

// IOOptions 封装通用上传/下载/删除的可选参数。
type IOOptions struct {
	Quiet         bool              // 批量删除时是否开启静默模式（OSS quiet 模式下服务端不返回已删除对象列表，故 BatchDelete 返回空切片）
	ContentType   string            // 上传媒体类型，如 "image/png"
	VersionID     string            // 操作指定版本的对象
	ConcurrentNum int               // 批处理内部并发数
	Meta          map[string]string // 自定义元数据
}

// IOOption 配置 IOOptions。
type IOOption func(*IOOptions)

// WithQuiet 设置批量删除是否开启静默模式。
func WithQuiet(quiet bool) IOOption {
	return func(o *IOOptions) { o.Quiet = quiet }
}

// WithContentType 设置上传文件的 Content-Type。
func WithContentType(contentType string) IOOption {
	return func(o *IOOptions) { o.ContentType = contentType }
}

// WithVersionID 操作指定历史版本的文件。
func WithVersionID(versionID string) IOOption {
	return func(o *IOOptions) { o.VersionID = versionID }
}

// WithConcurrency 设置批处理内部并发数。
func WithConcurrency(num int) IOOption {
	return func(o *IOOptions) { o.ConcurrentNum = num }
}

// WithMeta 设置上传或分片完成时附加的自定义元数据。
func WithMeta(meta map[string]string) IOOption {
	return func(o *IOOptions) {
		if o.Meta == nil {
			o.Meta = make(map[string]string)
		}
		for k, v := range meta {
			o.Meta[k] = v
		}
	}
}

// ==========================================
// 统一存储抽象接口
// ==========================================

// Storage 是统一的存储抽象接口（disk / oss 均完整实现）。
type Storage interface {
	// --- 路径与元信息 ---
	GetUsefulUrl(key string) string
	Stat(ctx context.Context, key string) (ObjectItem, error)

	// --- 基础单文件 CRUD ---
	Exists(ctx context.Context, key string) (bool, error)
	Upload(ctx context.Context, key string, body io.Reader, opts ...IOOption) error
	Delete(ctx context.Context, key string, opts ...IOOption) error
	Download(ctx context.Context, key string, opts ...IOOption) (io.ReadCloser, error)

	// --- 批量与高级 ---
	BatchUpload(ctx context.Context, tasks []UploadTask, opts ...IOOption) error
	BatchDelete(ctx context.Context, keys []string, opts ...IOOption) ([]string, error)
	DeleteByPrefix(ctx context.Context, prefix string, opts ...IOOption) error
	PrefixIterator(ctx context.Context, prefix string, callback ListCallback) error

	// --- 大文件分片上传 ---
	InitiateMultipartUpload(ctx context.Context, key string) (MultipartUploadSession, error)
	CompleteMultipartUpload(ctx context.Context, session MultipartUploadSession, parts []UploadPartResponse, opts ...IOOption) error
	CancelMultipartUpload(ctx context.Context, session MultipartUploadSession) error
	MultipartUpload(ctx context.Context, session MultipartUploadSession, partNumber int, partSize int64, body io.Reader) (UploadPartResponse, error)
}

// ErrUnsupportedStorageType 表示配置的存储类型不支持。
var ErrUnsupportedStorageType = fmt.Errorf("unsupported storage type")

//// NewStorage 根据配置创建 Storage 实例。
//func NewStorage(cfg *config.Storage) (Storage, error) {
//	if cfg == nil {
//		return nil, fmt.Errorf("storage config is nil")
//	}
//	switch cfg.Type {
//	case "disk":
//		return disk.NewDiskStorage(cfg)
//	case "oss":
//		return aliyunoss.NewOSSStorage(cfg)
//	default:
//		return nil, fmt.Errorf("%w: %s", ErrUnsupportedStorageType, cfg.Type)
//	}
//}

// ApplyIOOptions merges variable options into a final IOOptions.
func ApplyIOOptions(opts []IOOption) IOOptions {
	o := IOOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}
