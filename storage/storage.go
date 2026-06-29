package aliyun_oss

import (
	"context"
	"io"
)

// ==========================================
// 1. 核心模型与结构体定义
// ==========================================

// ObjectItem 代表遍历或列举文件时返回的通用元信息
type ObjectItem struct {
	Key          string // 文件的相对路径（或绝对路径，取决于客户端初始化时的 Root 配置）
	Size         int64  // 文件大小（字节）
	LastModified int64  // 最后修改时间（秒级 Unix 时间戳）
}

// UploadTask 用于批量上传时的单个任务定义
type UploadTask struct {
	Key  string    // 目标存储路径
	Body io.Reader // 文件内容流
}

// ListCallback 定义了遍历文件时的流式回调函数
// items 每次传入当前页的数据（通常最多 1000 个），业务方返回 error 可主动中断后续遍历
type ListCallback func(ctx context.Context, items ...ObjectItem) error

// MultipartUploadSession 代表一个分片上传的会话凭证
type MultipartUploadSession struct {
	UploadID string // 唯一分片上传事件 ID（云厂商生成，本地磁盘可用 UUID 模拟）
	Key      string // 目标文件路径
}

// UploadPartResponse 代表单个分片上传成功后的元数据
type UploadPartResponse struct {
	PartNumber int    // 分片号（从 1 开始递增）
	ETag       string // 分片的 MD5 校验码（各云厂商的通用凭证，本地磁盘可为空）
}

// ==========================================
// 2. Functional Options (配置选项) 设计
// ==========================================

// IOOptions 封装通用上传/下载/删除的可选参数
type IOOptions struct {
	Quiet         bool              // 批量删除时，是否开启静默模式（true则只返回失败列表）
	ContentType   string            // 上传文件时的媒体类型，如 "image/png"
	VersionID     string            // 针对支持版本控制（Versioning）的存储，操作指定版本的对象
	ConcurrentNum int               // 批量/前缀操作内部并发执行的协程数
	Meta          map[string]string // 上传/分片完成时附加的自定义元数据
}

type IOOption func(*IOOptions)

// WithQuiet 设置批量删除是否开启静默模式
func WithQuiet(quiet bool) IOOption {
	return func(o *IOOptions) { o.Quiet = quiet }
}

// WithContentType 设置上传文件的 Content-Type
func WithContentType(contentType string) IOOption {
	return func(o *IOOptions) { o.ContentType = contentType }
}

// WithVersionID 操作指定历史版本的文件
func WithVersionID(versionID string) IOOption {
	return func(o *IOOptions) { o.VersionID = versionID }
}

// WithConcurrency 设置批处理或前缀删除时的内部并发数
func WithConcurrency(num int) IOOption {
	return func(o *IOOptions) { o.ConcurrentNum = num }
}

// WithMeta 设置上传或分片完成时附加的自定义元数据
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
// 3. 最终统一存储抽象接口
// ==========================================

type Storage interface {

	// --- 路径与元信息辅助 ---

	// GetUsefulUrl 根据相对 key 生成可直接访问的完整 URL（BaseUrl + BaseDir + key）
	GetUsefulUrl(key string) string

	// Stat 获取单个文件的元信息（大小、最后修改时间）
	Stat(ctx context.Context, key string) (ObjectItem, error)

	// --- 基础单文件 CRUD 操作 ---

	// Exists 检查文件是否存在
	Exists(ctx context.Context, key string) (bool, error)

	// Upload 上传单个文件，接收 io.Reader 流，支持各种数据源
	Upload(ctx context.Context, key string, body io.Reader, opts ...IOOption) error

	// Delete 删除单个文件
	Delete(ctx context.Context, key string, opts ...IOOption) error

	// Download 下载单个文件，调用方需要在使用完毕后自行关闭返回的 io.ReadCloser
	Download(ctx context.Context, key string, opts ...IOOption) (io.ReadCloser, error)

	// --- 批量与高级操作（引入 Options 模式） ---

	// BatchUpload 批量上传文件，实现类内部可以通过协程池（Goroutine Pool）并发处理
	BatchUpload(ctx context.Context, tasks []UploadTask, opts ...IOOption) error

	// BatchDelete 批量删除指定的多个 Key（对应一次网络请求删多个文件，单次上限通常为 1000 个）
	// 返回成功删除的 Key 列表或错误
	BatchDelete(ctx context.Context, keys []string, opts ...IOOption) ([]string, error)

	// DeleteByPrefix 根据指定前缀删除所有文件（类似于删除文件夹）
	// 实现类内部应通过“PrefixIterator + BatchDelete”的内部循环安全、高效地实现
	DeleteByPrefix(ctx context.Context, prefix string, opts ...IOOption) error

	// PrefixIterator 迭代遍历指定前缀的文件，通过回调函数分页、流式返回
	// 完美避免海量数据导致内存暴涨，支持外部通过 error 中断整个迭代
	PrefixIterator(ctx context.Context, prefix string, callback ListCallback) error

	// --- 大文件分片上传（Multipart Upload）接口 ---

	// InitiateMultipartUpload 初始化分片上传事件，返回包含当前会话 ID 的凭证
	InitiateMultipartUpload(ctx context.Context, key string) (MultipartUploadSession, error)

	// CompleteMultipartUpload 完成分片上传，通知后端将所有已上传的分片聚合成一个完整文件
	// parts 必须按照 PartNumber 升序排列传入；可通过 opts 附加 WithMeta 等元数据
	CompleteMultipartUpload(ctx context.Context, session MultipartUploadSession, parts []UploadPartResponse, opts ...IOOption) error

	// CancelMultipartUpload 取消分片上传事件，并清理掉已上传的临时分片碎块，防止空间持续计费
	CancelMultipartUpload(ctx context.Context, session MultipartUploadSession) error

	// MultipartUpload 上传单个分片
	// partNumber 必须从 1 开始递增；partSize 为当前分片的准确大小（字节）
	MultipartUpload(ctx context.Context, session MultipartUploadSession, partNumber int, partSize int64, body io.Reader) (UploadPartResponse, error)
}
