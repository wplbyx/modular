# Storage 融合 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把根目录遗留 `storage/`（`luffa_micro_services` 来源、`aliyun_oss` 包、v1 OSS SDK）融合进 `packages/infra/storage/`：采用其单个富 `Storage` 接口为唯一标准，OSS 用 v2 SDK 重写，只保留 `disk` + `oss` 两个后端，删除其余实现与根 `storage/`。

**Architecture:** `packages/infra/storage` 重写为单一富 `Storage` 接口（CRUD + Stat + Batch + PrefixIterator + Multipart + functional options）。`DiskStorage` 为纯标准库移植；`OssStorage` 以 v2 SDK 重写，通过 `ossClient` 接口隔离便于 mock。配置沿用 `modular/packages/config`（`config.Storage` 收敛为 `disk|oss`）。每个任务保持仓库可编译、可测试，TDD + 频繁提交。

**Tech Stack:** Go 1.26（`module modular`）、`github.com/aliyun/alibabacloud-oss-go-sdk-v2` v1.5.1（OSS v2）、`github.com/google/uuid`、`golang.org/x/sync/errgroup`、`github.com/stretchr/testify`。

## Global Constraints

- 模块路径固定为 `modular`（`go.mod` 已由用户创建：`module modular`, go 1.26.0）。所有内部 import 用 `modular/packages/...`。
- storage 包名固定为 `storage`（目录 `packages/infra/storage/`）。
- OSS 仅用 **v2 SDK**（`github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss`）；不得引入或保留 v1 `aliyun-oss-go-sdk`。
- 只实现两个后端：`disk`、`oss`。移除 s3 / minio / ftp。
- 不保留全局单例（`Init*`/`Get*` 一律不迁移）。
- 不引入 logger 依赖（移植时丢弃遗留 `luffa_micro_services/pkg/logger` 调用）。
- 每个任务结束：`go build ./...` 通过；涉及测试的任务 `go test ./packages/infra/storage/...` 通过；然后提交。
- 工作分支：`feat/storage-merge`（已存在）。提交信息用 conventional commits（`feat:`/`refactor:`/`chore:`/`test:`）。

---

## File Structure

最终 `packages/infra/storage/` 目录：

| 文件 | 职责 |
|---|---|
| `storage.go` | 富 `Storage` 接口；类型（`ObjectItem`/`UploadTask`/`ListCallback`/`MultipartUploadSession`/`UploadPartResponse`/`IOOptions`/`IOOption`）；`With*` options；`applyIOOptions`；`NewStorage(cfg)` 工厂；`ErrUnsupportedStorageType`。 |
| `disk.go` | `DiskStorage`：本地磁盘实现（移植自遗留 `storage/storage_disk.go`），纯标准库。 |
| `oss.go` | `OssStorage`：v2 OSS 实现；`ossClient` 接口（mock 用）；OSS 专属辅助（`buildObjectKey`/URL/endpoint）。 |
| `disk_test.go` | `DiskStorage` 测试（移植自遗留 `storage/storage_disk_test.go`）。 |
| `oss_test.go` | `OssStorage` 测试（基于 mock `ossClient`）。 |

删除：`local.go`、`s3.go`、`object_helpers.go`、`object_storage_test.go`、`ftp/client.go`（及空目录 `ftp/`）、根 `storage/`。

`packages/config/config_storage.go`：`Storage` 收敛为 `{Type, PublicBaseURL, Disk, OSS}`；`DiskStorageConfig{RootDir, BaseUrl}`；`OSSStorageConfig`（现有字段 + `BaseDir`）。

---

## Task 1: 落地富接口与类型，移除旧实现

**Files:**
- Create: `packages/infra/storage/storage.go`（整体替换旧文件）
- Delete: `packages/infra/storage/local.go`, `packages/infra/storage/s3.go`, `packages/infra/storage/object_helpers.go`, `packages/infra/storage/object_storage_test.go`, `packages/infra/storage/oss.go`, `packages/infra/storage/ftp/client.go`, `packages/infra/storage/ftp/`（空目录）

**Interfaces:**
- Consumes: `modular/packages/config`（`config.Storage`，旧结构暂保留，Task 2 再改）。
- Produces: 富 `Storage` 接口、全部类型/options、`NewStorage(cfg *config.Storage) (Storage, error)`（本任务返回 `ErrUnsupportedStorageType`，后继任务接入后端）。

- [ ] **Step 1: 排查外部调用方**

Run:
```bash
grep -rn "modular/packages/infra/storage" --include="*.go" . | grep -v "packages/infra/storage/"
grep -rnE "storage\.(NewStorage|LocalStorage|OSSStorage|S3Storage|MinIOStorage|FileStorage|GetURL|Object)\b|\.GetURL\(|FileStorage" --include="*.go" packages/app packages 2>/dev/null
```
Expected: 若仅命中 storage 包内部，无需处理。若 `packages/app` 或其他包引用了旧 API（`NewStorage`/`LocalStorage`/`FileStorage`/`Object`/`GetURL`），记录这些位置——这些调用方在本任务让包能编译后可能需要适配（多数为库代码，预期无外部调用方；若有，在本任务把对旧类型的引用先改为 `storage.Storage` 接口占位或删除该调用，保证 `go build ./...` 通过）。

- [ ] **Step 2: 删除旧实现文件**

Run:
```bash
git rm packages/infra/storage/local.go \
       packages/infra/storage/s3.go \
       packages/infra/storage/object_helpers.go \
       packages/infra/storage/object_storage_test.go \
       packages/infra/storage/oss.go \
       packages/infra/storage/ftp/client.go
rmdir packages/infra/storage/ftp 2>/dev/null || true
```

- [ ] **Step 3: 写入新的 `storage.go`（接口 + 类型 + options + 工厂）**

用以下内容**整体覆盖** `packages/infra/storage/storage.go`：

```go
package storage

import (
	"context"
	"fmt"
	"io"

	"modular/packages/config"
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
	Quiet         bool              // 批量删除是否静默模式（true 只返回失败列表）
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

// applyIOOptions 将可选参数合并为 IOOptions。
func applyIOOptions(opts []IOOption) *IOOptions {
	o := &IOOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o
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

// NewStorage 根据配置创建 Storage 实例。
// Task 3/4 接入 disk/oss 后，此处 switch 填充对应分支。
func NewStorage(cfg *config.Storage) (Storage, error) {
	if cfg == nil {
		return nil, fmt.Errorf("storage config is nil")
	}
	switch cfg.Type {
	// case "disk":  // Task 3 接入
	// case "oss":   // Task 4 接入
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedStorageType, cfg.Type)
	}
}
```

- [ ] **Step 4: 确认编译（根 `storage/` 仍报错属预期）**

Run:
```bash
go build ./packages/... ./packages/infra/storage/...
```
Expected: `packages/infra/storage` 编译通过；根 `storage/` 仍因 `luffa_micro_services` import 报错（Task 5 删除）。`go build ./...` 此时仅剩根 `storage/` 报错。

- [ ] **Step 5: 提交**

```bash
git add packages/infra/storage/storage.go
git add -A packages/infra/storage   # 纳入文件删除
git commit -m "refactor(storage): 落地富 Storage 接口与类型，移除旧实现"
```

---

## Task 2: 配置收敛为 disk|oss

**Files:**
- Modify: `packages/config/config_storage.go`（整体替换）

**Interfaces:**
- Consumes: 无。
- Produces: `config.Storage{Type, PublicBaseURL, Disk *DiskStorageConfig, OSS *OSSStorageConfig}`；`config.DiskStorageConfig{RootDir, BaseUrl}`；`config.OSSStorageConfig`（含 `BaseDir`）。

- [ ] **Step 1: 排查对旧配置字段的引用**

Run:
```bash
grep -rnE "\.Local\b|LocalStorageConfig|\.S3\b|S3StorageConfig|\.Minio\b|MinioStorageConfig|config\.Storage\b" --include="*.go" packages | grep -v "config_storage.go"
```
Expected: 命中 storage 包内 Task 1 已删的引用应为空；若命中其他包，记录并在 Step 3 一并修正（预期为空——只有 storage 包消费这些字段）。

- [ ] **Step 2: 写测试（配置校验）**

Create `packages/config/config_storage_new_test.go`:

```go
package config

import "testing"

func TestStorageConfig_DiskFields(t *testing.T) {
	c := &Storage{Type: "disk", PublicBaseURL: "https://cdn.example.com",
		Disk: &DiskStorageConfig{RootDir: "/data", BaseUrl: "cdn.example.com"}}
	if c.Disk.RootDir != "/data" || c.Disk.BaseUrl != "cdn.example.com" {
		t.Fatalf("unexpected disk config: %+v", c.Disk)
	}
	if c.Type != "disk" {
		t.Fatalf("unexpected type: %s", c.Type)
	}
}

func TestStorageConfig_OSSBaseDir(t *testing.T) {
	c := &Storage{Type: "oss", OSS: &OSSStorageConfig{Bucket: "b", Region: "cn-hangzhou", BaseDir: "prefix"}}
	if c.OSS.BaseDir != "prefix" {
		t.Fatalf("BaseDir not set: %+v", c.OSS)
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./packages/config/ -run TestStorageConfig_ -v`
Expected: 编译失败（`S3`/`Minio`/`Local` 字段不存在——因为新结构还没写）或 `Disk`/`OSS` 字段不存在。先失败。

- [ ] **Step 4: 整体覆盖 `config_storage.go`**

```go
package config

import (
	"time"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Storage 存储
type Storage struct {
	Type          string              `mapstructure:"Type" validate:"required,oneof=disk oss"` // 存储类型
	PublicBaseURL string              `mapstructure:"PublicBaseURL"`                           // 文件对外访问域名
	Disk          *DiskStorageConfig  `mapstructure:"Disk"`                                     // 本地磁盘存储配置
	OSS           *OSSStorageConfig   `mapstructure:"OSS"`                                      // 阿里云 OSS 对象存储配置
}

// DiskStorageConfig 本地磁盘存储配置
type DiskStorageConfig struct {
	RootDir string `mapstructure:"RootDir"` // 存储根目录（绝对路径，跨平台）
	BaseUrl string `mapstructure:"BaseUrl"` // 访问域名（用于 GetUsefulUrl：baseUrl + key）
}

// OSSStorageConfig 阿里云 OSS 对象存储配置
type OSSStorageConfig struct {
	AccessKeyID     string        `mapstructure:"AccessKeyID" validate:"required"`
	AccessKeySecret string        `mapstructure:"AccessKeySecret" validate:"required"`
	SecurityToken   string        `mapstructure:"SecurityToken"`
	Region          string        `mapstructure:"Region" validate:"required"`
	Bucket          string        `mapstructure:"Bucket" validate:"required"`
	Endpoint        string        `mapstructure:"Endpoint"`
	BaseDir         string        `mapstructure:"BaseDir"` // 对象 key 前缀
	DisableSSL      bool          `mapstructure:"DisableSSL"`
	UseCName        bool          `mapstructure:"UseCName"`
	Timeout         time.Duration `mapstructure:"Timeout"`
	MaxRetries      int           `mapstructure:"MaxRetries"`
}
```

> 说明：保留旧文件的 `//go:generate gomodifytags` 指令；仅 `time` 被使用（`time.Duration`），不再需要 `os`。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./packages/config/ -run TestStorageConfig_ -v`
Expected: PASS。

- [ ] **Step 6: 全量编译**

Run: `go build ./packages/...`
Expected: 通过（根 `storage/` 仍报错，不在 `./packages/...` 范围）。

- [ ] **Step 7: 提交**

```bash
git add packages/config/config_storage.go packages/config/config_storage_new_test.go
git commit -m "refactor(config): 存储配置收敛为 disk|oss，OSS 增 BaseDir"
```

---

## Task 3: DiskStorage 移植（TDD）

**Files:**
- Create: `packages/infra/storage/disk.go`
- Create: `packages/infra/storage/disk_test.go`
- Modify: `packages/infra/storage/storage.go`（接入 `case "disk"`）

**Interfaces:**
- Consumes: `Storage` 接口（Task 1）、`config.Storage.Disk`（Task 2）、`github.com/google/uuid`、`golang.org/x/sync/errgroup`。
- Produces: `DiskStorage`（实现 `Storage`）、`NewDiskStorage(cfg *config.Storage) (*DiskStorage, error)`。

- [ ] **Step 1: 移植测试（先写测试）**

把根目录 `storage/storage_disk_test.go` 复制到 `packages/infra/storage/disk_test.go`，做如下精确替换：

1. 包名：`package aliyun_oss` → `package storage`
2. 构造调用：所有 `NewDiskStorage(&LocalDiskStorage{RootDir: ..., BaseUrl: ...})` → `NewDiskStorage(&config.Storage{Type: "disk", Disk: &config.DiskStorageConfig{RootDir: ..., BaseUrl: ...}})`（在文件 import 块加入 `"modular/packages/config"`）
3. 删除（若有）针对 `InitDiskStorage`/`GetDiskStorage` 单例的测试用例（新设计无单例）。
4. 其余类型名（`DiskStorage`、`ObjectItem`、`UploadTask`、`MultipartUploadSession`、`UploadPartResponse`、`ListCallback`）保持不变（Task 1 已定义同名）。

Run（复制 + 替换示例，若偏好命令行）:
```bash
cp storage/storage_disk_test.go packages/infra/storage/disk_test.go
# 然后用编辑器执行上述 4 条替换
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./packages/infra/storage/ -run TestDisk -v`
Expected: 编译失败——`DiskStorage`/`NewDiskStorage` 未定义。

- [ ] **Step 3: 创建 `disk.go`（移植实现）**

把根目录 `storage/storage_disk.go` 复制为 `packages/infra/storage/disk.go`，应用以下精确改动（其余方法**逐字保留**）：

```bash
cp storage/storage_disk.go packages/infra/storage/disk.go
```

**改动 1 — 包名与 import：**
```go
// 旧
package aliyun_oss

import (
	...
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"luffa_micro_services/pkg/logger"
)
// 新
package storage

import (
	...
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"modular/packages/config"
)
```
（删除 `luffa_micro_services/pkg/logger` import；新增 `modular/packages/config`。其余标准库 import 不变。）

**改动 2 — 删除单例与替换构造：** 删除 `var diskStorageInstance *DiskStorage`、`InitDiskStorage`、`GetDiskStorage` 三个声明；把构造函数改为：

```go
// NewDiskStorage 构造一个新的本地磁盘 Storage 实例。
func NewDiskStorage(cfg *config.Storage) (*DiskStorage, error) {
	if cfg == nil || cfg.Disk == nil {
		return nil, errors.New("disk storage config is nil")
	}
	if cfg.Disk.RootDir == "" {
		return nil, errors.New("DiskStorageConfig.RootDir is empty")
	}

	rootDir, err := filepath.Abs(cfg.Disk.RootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root dir: %w", err)
	}
	if err = os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create root dir: %w", err)
	}

	baseUrl := cfg.Disk.BaseUrl
	baseUrl = strings.TrimPrefix(baseUrl, "https://")
	baseUrl = strings.TrimPrefix(baseUrl, "http://")
	baseUrl = strings.TrimRight(baseUrl, "/")

	return &DiskStorage{rootDir: rootDir, baseUrl: baseUrl}, nil
}
```

> `DiskStorage` 结构体定义、`var _ Storage = (*DiskStorage)(nil)` 断言、`safeFilePath`、`multipartDir`、`GetUsefulUrl`、`Exists`、`Upload`、`Delete`、`Download`、`Stat`、`BatchUpload`、`BatchDelete`、`DeleteByPrefix`、`PrefixIterator`、`InitiateMultipartUpload`、`MultipartUpload`、`CompleteMultipartUpload`、`CancelMultipartUpload` —— 全部**逐字保留**（这些方法体不依赖 logger/config，仅标准库 + uuid + errgroup）。

- [ ] **Step 4: 接入工厂**

在 `packages/infra/storage/storage.go` 的 `NewStorage` switch 中取消注释并填充：

```go
	switch cfg.Type {
	case "disk":
		return NewDiskStorage(cfg)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedStorageType, cfg.Type)
	}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./packages/infra/storage/ -run TestDisk -v`
Expected: PASS（移植自 646 行遗留测试，覆盖 CRUD/Batch/Prefix/Multipart）。

- [ ] **Step 6: 编译期断言 + 全量编译**

确认 `disk.go` 含 `var _ Storage = (*DiskStorage)(nil)`（已在移植文件中）。
Run: `go build ./packages/... && go vet ./packages/infra/storage/`
Expected: 通过。

- [ ] **Step 7: 提交**

```bash
git add packages/infra/storage/disk.go packages/infra/storage/disk_test.go packages/infra/storage/storage.go
git commit -m "feat(storage): 移植 DiskStorage（本地磁盘富接口实现）"
```

---

## Task 4: OssStorage v2 重写（TDD）

**Files:**
- Create: `packages/infra/storage/oss.go`
- Create: `packages/infra/storage/oss_test.go`
- Modify: `packages/infra/storage/storage.go`（接入 `case "oss"`）

**Interfaces:**
- Consumes: `Storage` 接口（Task 1）、`config.Storage.OSS`（Task 2）、v2 SDK（`alibabacloud-oss-go-sdk-v2/oss` + `.../oss/credentials`）。
- Produces: `OssStorage`（实现 `Storage`）、`NewOSSStorage(cfg *config.Storage) (*OssStorage, error)`、内部 `ossClient` 接口。

> v2 SDK 关键事实（已核实，编码时直接用）：所有 request 字段为指针，用 `oss.Ptr(x)`；`HeadObjectResult.ContentLength` 为 `int64` 值，`LastModified *time.Time`；批量删除 `request.Delete = &oss.Delete{Objects: []oss.ObjectIdentifier{{Key: oss.Ptr(k)}}, Quiet: bool}`，结果 `DeletedObjects []DeletedInfo{Key *string}`；列举用 `ListObjectsV2` + `ContinuationToken`/`NextContinuationToken`/`IsTruncated`；分片方法为 `InitiateMultipartUpload`，upload-id 字段 `UploadId`；`CompleteMultipartUploadRequest.CompleteMultipartUpload.Parts []oss.UploadPart{PartNumber int32, ETag *string}`；`UploadPartResult` 只有 `ETag`，PartNumber 自行记录；错误用 `var se *oss.ServiceError; errors.As(err,&se)` 读 `se.StatusCode`/`se.Code`。

- [ ] **Step 1: 写 mock 与测试（先写测试）**

Create `packages/infra/storage/oss_test.go`:

```go
package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockOSSClient 实现 ossClient，按需覆盖单个方法。
type mockOSSClient struct {
	putObject    func(context.Context, *aliyunoss.PutObjectRequest) (*aliyunoss.PutObjectResult, error)
	getObject    func(context.Context, *aliyunoss.GetObjectRequest) (*aliyunoss.GetObjectResult, error)
	deleteObject func(context.Context, *aliyunoss.DeleteObjectRequest) (*aliyunoss.DeleteObjectResult, error)
	headObject   func(context.Context, *aliyunoss.HeadObjectRequest) (*aliyunoss.HeadObjectResult, error)
	deleteMulti  func(context.Context, *aliyunoss.DeleteMultipleObjectsRequest) (*aliyunoss.DeleteMultipleObjectsResult, error)
	listV2       func(context.Context, *aliyunoss.ListObjectsV2Request) (*aliyunoss.ListObjectsV2Result, error)
	initMultipart func(context.Context, *aliyunoss.InitiateMultipartUploadRequest) (*aliyunoss.InitiateMultipartUploadResult, error)
	uploadPart   func(context.Context, *aliyunoss.UploadPartRequest) (*aliyunoss.UploadPartResult, error)
	completeMulti func(context.Context, *aliyunoss.CompleteMultipartUploadRequest) (*aliyunoss.CompleteMultipartUploadResult, error)
	abortMultipart func(context.Context, *aliyunoss.AbortMultipartUploadRequest) (*aliyunoss.AbortMultipartUploadResult, error)
}

func (m *mockOSSClient) PutObject(ctx context.Context, r *aliyunoss.PutObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.PutObjectResult, error) {
	return m.putObject(ctx, r)
}
func (m *mockOSSClient) GetObject(ctx context.Context, r *aliyunoss.GetObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.GetObjectResult, error) {
	return m.getObject(ctx, r)
}
func (m *mockOSSClient) DeleteObject(ctx context.Context, r *aliyunoss.DeleteObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.DeleteObjectResult, error) {
	return m.deleteObject(ctx, r)
}
func (m *mockOSSClient) HeadObject(ctx context.Context, r *aliyunoss.HeadObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.HeadObjectResult, error) {
	return m.headObject(ctx, r)
}
func (m *mockOSSClient) DeleteMultipleObjects(ctx context.Context, r *aliyunoss.DeleteMultipleObjectsRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.DeleteMultipleObjectsResult, error) {
	return m.deleteMulti(ctx, r)
}
func (m *mockOSSClient) ListObjectsV2(ctx context.Context, r *aliyunoss.ListObjectsV2Request, _ ...func(*aliyunoss.Options)) (*aliyunoss.ListObjectsV2Result, error) {
	return m.listV2(ctx, r)
}
func (m *mockOSSClient) InitiateMultipartUpload(ctx context.Context, r *aliyunoss.InitiateMultipartUploadRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.InitiateMultipartUploadResult, error) {
	return m.initMultipart(ctx, r)
}
func (m *mockOSSClient) UploadPart(ctx context.Context, r *aliyunoss.UploadPartRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.UploadPartResult, error) {
	return m.uploadPart(ctx, r)
}
func (m *mockOSSClient) CompleteMultipartUpload(ctx context.Context, r *aliyunoss.CompleteMultipartUploadRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.CompleteMultipartUploadResult, error) {
	return m.completeMulti(ctx, r)
}
func (m *mockOSSClient) AbortMultipartUpload(ctx context.Context, r *aliyunoss.AbortMultipartUploadRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.AbortMultipartUploadResult, error) {
	return m.abortMultipart(ctx, r)
}

func newMockOSSStorage(c ossClient) *OssStorage {
	return &OssStorage{client: c, bucket: "bk", region: "cn-hangzhou", endpoint: "", useCName: false, publicBaseURL: "https://cdn.example.com", baseDir: "prefix"}
}

func TestOSS_UploadDownloadDelete(t *testing.T) {
	var gotKey string
	m := &mockOSSClient{
		putObject: func(_ context.Context, r *aliyunoss.PutObjectRequest) (*aliyunoss.PutObjectResult, error) {
			gotKey = *r.Key
			return &aliyunoss.PutObjectResult{}, nil
		},
		getObject: func(_ context.Context, r *aliyunoss.GetObjectRequest) (*aliyunoss.GetObjectResult, error) {
			return &aliyunoss.GetObjectResult{Body: io.NopCloser(bytes.NewReader([]byte("hello")))}, nil
		},
		deleteObject: func(_ context.Context, r *aliyunoss.DeleteObjectRequest) (*aliyunoss.DeleteObjectResult, error) {
			return &aliyunoss.DeleteObjectResult{}, nil
		},
	}
	s := newMockOSSStorage(m)
	require.NoError(t, s.Upload(context.Background(), "a/b.txt", bytes.NewReader([]byte("hello"))))
	assert.Equal(t, "prefix/a/b.txt", gotKey) // baseDir 前缀生效

	rc, err := s.Download(context.Background(), "a/b.txt")
	require.NoError(t, err)
	b, _ := io.ReadAll(rc)
	rc.Close()
	assert.Equal(t, "hello", string(b))

	require.NoError(t, s.Delete(context.Background(), "a/b.txt"))
}

func TestOSS_Exists_Stat(t *testing.T) {
	notFound := &aliyunoss.ServiceError{StatusCode: 404, Code: "NoSuchKey"}
	m := &mockOSSClient{
		headObject: func(_ context.Context, r *aliyunoss.HeadObjectRequest) (*aliyunoss.HeadObjectResult, error) {
			if *r.Key == "missing" {
				return nil, notFound
			}
			return &aliyunoss.HeadObjectResult{ContentLength: 42}, nil
		},
	}
	s := newMockOSSStorage(m)
	exists, err := s.Exists(context.Background(), "missing")
	require.NoError(t, err)
	assert.False(t, exists)

	exists, err = s.Exists(context.Background(), "present")
	require.NoError(t, err)
	assert.True(t, exists)

	item, err := s.Stat(context.Background(), "present")
	require.NoError(t, err)
	assert.Equal(t, int64(42), item.Size)
}

func TestOSS_BatchDelete_Quiet(t *testing.T) {
	var seenQuiet bool
	m := &mockOSSClient{
		deleteMulti: func(_ context.Context, r *aliyunoss.DeleteMultipleObjectsRequest) (*aliyunoss.DeleteMultipleObjectsResult, error) {
			seenQuiet = r.Delete.Quiet
			// 静默模式不返回已删除列表
			return &aliyunoss.DeleteMultipleObjectsResult{}, nil
		},
	}
	s := newMockOSSStorage(m)
	deleted, err := s.BatchDelete(context.Background(), []string{"a", "b"}, WithQuiet(true))
	require.NoError(t, err)
	assert.True(t, seenQuiet)
	assert.Empty(t, deleted) // quiet 模式无返回
}

func TestOSS_PrefixIterator_Pagination(t *testing.T) {
	calls := 0
	m := &mockOSSClient{
		listV2: func(_ context.Context, r *aliyunoss.ListObjectsV2Request) (*aliyunoss.ListObjectsV2Result, error) {
			calls++
			if calls == 1 {
				return &aliyunoss.ListObjectsV2Result{
					IsTruncated:           true,
					NextContinuationToken: aliyunoss.Ptr("tok2"),
					Contents: []aliyunoss.ObjectProperties{
						{Key: aliyunoss.Ptr("prefix/1"), Size: 10},
					},
				}, nil
			}
			return &aliyunoss.ListObjectsV2Result{
				IsTruncated: false,
				Contents: []aliyunoss.ObjectProperties{
					{Key: aliyunoss.Ptr("prefix/2"), Size: 20},
				},
			}, nil
		},
	}
	s := newMockOSSStorage(m)
	var keys []string
	err := s.PrefixIterator(context.Background(), "prefix", func(_ context.Context, items ...ObjectItem) error {
		for _, it := range items {
			keys = append(keys, it.Key)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"prefix/1", "prefix/2"}, keys) // baseDir="prefix" 被剥离前缀？注意：此处 key 本身含 prefix
	assert.Equal(t, 2, calls)
}

func TestOSS_MultipartFlow(t *testing.T) {
	m := &mockOSSClient{
		initMultipart: func(_ context.Context, r *aliyunoss.InitiateMultipartUploadRequest) (*aliyunoss.InitiateMultipartUploadResult, error) {
			return &aliyunoss.InitiateMultipartUploadResult{Bucket: aliyunoss.Ptr("bk"), Key: r.Key, UploadId: aliyunoss.Ptr("uid-1")}, nil
		},
		uploadPart: func(_ context.Context, r *aliyunoss.UploadPartRequest) (*aliyunoss.UploadPartResult, error) {
			return &aliyunoss.UploadPartResult{ETag: aliyunoss.Ptr("etag-" + (*r.UploadId))}, nil
		},
		completeMulti: func(_ context.Context, r *aliyunoss.CompleteMultipartUploadRequest) (*aliyunoss.CompleteMultipartUploadResult, error) {
			if *r.UploadId != "uid-1" {
				return nil, errors.New("bad upload id")
			}
			return &aliyunoss.CompleteMultipartUploadResult{}, nil
		},
		abortMultipart: func(_ context.Context, r *aliyunoss.AbortMultipartUploadRequest) (*aliyunoss.AbortMultipartUploadResult, error) {
			return &aliyunoss.AbortMultipartUploadResult{}, nil
		},
	}
	s := newMockOSSStorage(m)
	ctx := context.Background()
	sess, err := s.InitiateMultipartUpload(ctx, "big/file")
	require.NoError(t, err)
	assert.Equal(t, "uid-1", sess.UploadID)
	assert.Equal(t, "prefix/big/file", sess.Key) // 完整 objectKey

	pr, err := s.MultipartUpload(ctx, sess, 1, 5, bytes.NewReader([]byte("part1")))
	require.NoError(t, err)
	assert.Equal(t, 1, pr.PartNumber)
	assert.Equal(t, "etag-uid-1", pr.ETag)

	require.NoError(t, s.CompleteMultipartUpload(ctx, sess, []UploadPartResponse{{PartNumber: 1, ETag: "etag-uid-1"}}))
	require.NoError(t, s.CancelMultipartUpload(ctx, sess))
}
```

> 注意 `TestOSS_PrefixIterator_Pagination`：mock 返回的 key 已是 `"prefix/1"`；`OssStorage.PrefixIterator` 会剥离 `baseDir("prefix")+"/"` 前缀。因 mock key 以 `prefix/` 开头，剥离后得到 `1`、`2`。**实现时以剥离后的值为准**；如断言与实现不一致，调整断言为 `[]string{"1","2"}`（实现正确性优先）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./packages/infra/storage/ -run TestOSS_ -v`
Expected: 编译失败——`OssStorage`/`ossClient`/`NewOSSStorage` 未定义。

- [ ] **Step 3: 创建 `oss.go`（v2 实现）**

Create `packages/infra/storage/oss.go`:

```go
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"golang.org/x/sync/errgroup"

	"modular/packages/config"
)

var _ Storage = (*OssStorage)(nil)

// ossClient 隔离 v2 Client，便于测试 mock。*aliyunoss.Client 天然满足该接口。
type ossClient interface {
	PutObject(context.Context, *aliyunoss.PutObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.PutObjectResult, error)
	GetObject(context.Context, *aliyunoss.GetObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.GetObjectResult, error)
	DeleteObject(context.Context, *aliyunoss.DeleteObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.DeleteObjectResult, error)
	HeadObject(context.Context, *aliyunoss.HeadObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.HeadObjectResult, error)
	DeleteMultipleObjects(context.Context, *aliyunoss.DeleteMultipleObjectsRequest, ...func(*aliyunoss.Options)) (*aliyunoss.DeleteMultipleObjectsResult, error)
	ListObjectsV2(context.Context, *aliyunoss.ListObjectsV2Request, ...func(*aliyunoss.Options)) (*aliyunoss.ListObjectsV2Result, error)
	InitiateMultipartUpload(context.Context, *aliyunoss.InitiateMultipartUploadRequest, ...func(*aliyunoss.Options)) (*aliyunoss.InitiateMultipartUploadResult, error)
	UploadPart(context.Context, *aliyunoss.UploadPartRequest, ...func(*aliyunoss.Options)) (*aliyunoss.UploadPartResult, error)
	CompleteMultipartUpload(context.Context, *aliyunoss.CompleteMultipartUploadRequest, ...func(*aliyunoss.Options)) (*aliyunoss.CompleteMultipartUploadResult, error)
	AbortMultipartUpload(context.Context, *aliyunoss.AbortMultipartUploadRequest, ...func(*aliyunoss.Options)) (*aliyunoss.AbortMultipartUploadResult, error)
}

// OssStorage 是 Storage 的阿里云 OSS（v2 SDK）实现。
type OssStorage struct {
	client        ossClient
	bucket        string
	region        string
	endpoint      string
	useCName      bool
	publicBaseURL string
	baseDir       string // 对象 key 前缀（不带首尾斜杠）
}

// NewOSSStorage 构造 OSS Storage。
func NewOSSStorage(cfg *config.Storage) (*OssStorage, error) {
	if cfg == nil || cfg.OSS == nil {
		return nil, errors.New("oss storage config is nil")
	}
	oc := cfg.OSS
	if oc.Bucket == "" || oc.Region == "" || oc.AccessKeyID == "" || oc.AccessKeySecret == "" {
		return nil, errors.New("oss bucket/region/access-key are required")
	}
	endpoint := normalizeEndpoint(oc.Endpoint, oc.DisableSSL)

	sdkCfg := aliyunoss.LoadDefaultConfig().
		WithRegion(oc.Region).
		WithCredentialsProvider(osscredentials.NewStaticCredentialsProvider(oc.AccessKeyID, oc.AccessKeySecret, oc.SecurityToken)).
		WithUseCName(oc.UseCName).
		WithDisableSSL(oc.DisableSSL)
	if endpoint != "" {
		sdkCfg = sdkCfg.WithEndpoint(endpoint)
	}
	if oc.MaxRetries > 0 {
		sdkCfg = sdkCfg.WithRetryMaxAttempts(oc.MaxRetries)
	}
	if oc.Timeout > 0 {
		sdkCfg = sdkCfg.
			WithConnectTimeout(oc.Timeout).
			WithReadWriteTimeout(oc.Timeout).
			WithHttpClient(&http.Client{Timeout: oc.Timeout})
	}

	return &OssStorage{
		client:        aliyunoss.NewClient(sdkCfg),
		bucket:        oc.Bucket,
		region:        oc.Region,
		endpoint:      endpoint,
		useCName:      oc.UseCName,
		publicBaseURL: cfg.PublicBaseURL,
		baseDir:       strings.Trim(oc.BaseDir, "/"),
	}, nil
}

// buildObjectKey 把相对 key 拼成 OSS 内部 objectKey。
func (s *OssStorage) buildObjectKey(key string) string {
	key = strings.TrimPrefix(key, "/")
	if s.baseDir != "" {
		return s.baseDir + "/" + key
	}
	return key
}

// GetUsefulUrl 生成可直接访问的完整 URL。
func (s *OssStorage) GetUsefulUrl(key string) string {
	if key == "" {
		return ""
	}
	fallback := ossDefaultURL(s.bucket, s.region, s.endpoint, s.buildObjectKey(key), s.useCName)
	return objectURL(s.publicBaseURL, fallback, s.buildObjectKey(key))
}

// Exists 检查对象是否存在。
func (s *OssStorage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &aliyunoss.HeadObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
	})
	if err == nil {
		return true, nil
	}
	if isOSSNotFound(err) {
		return false, nil
	}
	return false, err
}

// Upload 上传单个文件。
func (s *OssStorage) Upload(ctx context.Context, key string, body io.Reader, opts ...IOOption) error {
	o := applyIOOptions(opts)
	req := &aliyunoss.PutObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
		Body:   body,
	}
	if o.ContentType != "" {
		req.ContentType = aliyunoss.Ptr(o.ContentType)
	}
	if len(o.Meta) > 0 {
		req.Metadata = o.Meta
	}
	if _, err := s.client.PutObject(ctx, req); err != nil {
		return fmt.Errorf("put oss object %s: %w", key, err)
	}
	return nil
}

// Delete 删除单个文件。
func (s *OssStorage) Delete(ctx context.Context, key string, _ ...IOOption) error {
	if _, err := s.client.DeleteObject(ctx, &aliyunoss.DeleteObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
	}); err != nil {
		return fmt.Errorf("delete oss object %s: %w", key, err)
	}
	return nil
}

// Download 下载单个文件，调用方需关闭返回的 io.ReadCloser。
func (s *OssStorage) Download(ctx context.Context, key string, _ ...IOOption) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &aliyunoss.GetObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
	})
	if err != nil {
		return nil, fmt.Errorf("get oss object %s: %w", key, err)
	}
	return out.Body, nil
}

// Stat 获取元信息。
func (s *OssStorage) Stat(ctx context.Context, key string) (ObjectItem, error) {
	res, err := s.client.HeadObject(ctx, &aliyunoss.HeadObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
	})
	if err != nil {
		return ObjectItem{}, err
	}
	item := ObjectItem{Key: key, Size: res.ContentLength}
	if res.LastModified != nil {
		item.LastModified = res.LastModified.Unix()
	}
	return item, nil
}

// BatchUpload 批量上传（errgroup 并发，错误聚合）。
func (s *OssStorage) BatchUpload(ctx context.Context, tasks []UploadTask, opts ...IOOption) error {
	if len(tasks) == 0 {
		return nil
	}
	o := applyIOOptions(opts)
	concurrency := o.ConcurrentNum
	if concurrency <= 0 {
		concurrency = 5
	}
	eg := new(errgroup.Group)
	eg.SetLimit(concurrency)
	var (
		mu   sync.Mutex
		errs []error
	)
	for _, task := range tasks {
		eg.Go(func() error {
			if err := s.Upload(ctx, task.Key, task.Body, opts...); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("upload %s: %w", task.Key, err))
				mu.Unlock()
			}
			return nil
		})
	}
	_ = eg.Wait()
	return errors.Join(errs...)
}

// BatchDelete 批量删除，返回成功删除的 key（相对路径）。
func (s *OssStorage) BatchDelete(ctx context.Context, keys []string, opts ...IOOption) ([]string, error) {
	o := applyIOOptions(opts)
	prefix := ""
	if s.baseDir != "" {
		prefix = s.baseDir + "/"
	}

	var (
		deleted []string
		errs    []error
	)
	const batchSize = 1000
	for start := 0; start < len(keys); start += batchSize {
		end := start + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		objects := make([]aliyunoss.ObjectIdentifier, 0, end-start)
		for _, k := range keys[start:end] {
			if k == "" {
				continue
			}
			objects = append(objects, aliyunoss.ObjectIdentifier{Key: aliyunoss.Ptr(s.buildObjectKey(k))})
		}
		if len(objects) == 0 {
			continue
		}
		res, err := s.client.DeleteMultipleObjects(ctx, &aliyunoss.DeleteMultipleObjectsRequest{
			Bucket: aliyunoss.Ptr(s.bucket),
			Delete: &aliyunoss.Delete{Objects: objects, Quiet: o.Quiet},
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("batch[%d-%d]: %w", start, end-1, err))
			continue
		}
		if o.Quiet {
			// 静默模式：无错误即认为本批全部成功
			for _, k := range keys[start:end] {
				deleted = append(deleted, k)
			}
		} else {
			for _, d := range res.DeletedObjects {
				if d.Key != nil {
					deleted = append(deleted, strings.TrimPrefix(*d.Key, prefix))
				}
			}
		}
	}
	return deleted, errors.Join(errs...)
}

// DeleteByPrefix 按前缀删除（PrefixIterator + 每 1000 条 BatchDelete）。
func (s *OssStorage) DeleteByPrefix(ctx context.Context, prefix string, opts ...IOOption) error {
	if prefix == "" {
		return errors.New("DeleteByPrefix: prefix must not be empty")
	}
	const deleteBatch = 1000
	var batch []string
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, err := s.BatchDelete(ctx, batch, opts...)
		batch = batch[:0]
		return err
	}
	err := s.PrefixIterator(ctx, prefix, func(ctx context.Context, items ...ObjectItem) error {
		for _, item := range items {
			batch = append(batch, item.Key)
			if len(batch) >= deleteBatch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

// PrefixIterator 流式分页遍历。
func (s *OssStorage) PrefixIterator(ctx context.Context, prefix string, callback ListCallback) error {
	fullPrefix := s.buildObjectKey(prefix)
	strip := ""
	if s.baseDir != "" {
		strip = s.baseDir + "/"
	}
	var continuationToken string
	for {
		res, err := s.client.ListObjectsV2(ctx, &aliyunoss.ListObjectsV2Request{
			Bucket:            aliyunoss.Ptr(s.bucket),
			Prefix:            aliyunoss.Ptr(fullPrefix),
			ContinuationToken: aliyunoss.Ptr(continuationToken),
			MaxKeys:           1000,
		})
		if err != nil {
			return err
		}
		items := make([]ObjectItem, 0, len(res.Contents))
		for _, obj := range res.Contents {
			k := ""
			if obj.Key != nil {
				k = strings.TrimPrefix(*obj.Key, strip)
			}
			items = append(items, ObjectItem{Key: k, Size: obj.Size})
		}
		if err = callback(ctx, items...); err != nil {
			return err
		}
		if !res.IsTruncated {
			break
		}
		if res.NextContinuationToken != nil {
			continuationToken = *res.NextContinuationToken
		} else {
			break
		}
	}
	return nil
}

// InitiateMultipartUpload 初始化分片上传。session.Key 存完整 objectKey。
func (s *OssStorage) InitiateMultipartUpload(ctx context.Context, key string) (MultipartUploadSession, error) {
	objKey := s.buildObjectKey(key)
	res, err := s.client.InitiateMultipartUpload(ctx, &aliyunoss.InitiateMultipartUploadRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(objKey),
	})
	if err != nil {
		return MultipartUploadSession{}, fmt.Errorf("InitiateMultipartUpload: %w", err)
	}
	return MultipartUploadSession{UploadID: aliyunoss.ToString(res.UploadId), Key: objKey}, nil
}

// MultipartUpload 上传单个分片。
func (s *OssStorage) MultipartUpload(ctx context.Context, session MultipartUploadSession, partNumber int, partSize int64, body io.Reader) (UploadPartResponse, error) {
	if partNumber < 1 {
		return UploadPartResponse{}, errors.New("partNumber must be >= 1")
	}
	res, err := s.client.UploadPart(ctx, &aliyunoss.UploadPartRequest{
		Bucket:   aliyunoss.Ptr(s.bucket),
		Key:      aliyunoss.Ptr(session.Key),
		UploadId: aliyunoss.Ptr(session.UploadID),
		PartNumber: int32(partNumber),
		Body:     body,
	})
	if err != nil {
		return UploadPartResponse{}, fmt.Errorf("UploadPart %d: %w", partNumber, err)
	}
	return UploadPartResponse{PartNumber: partNumber, ETag: aliyunoss.ToString(res.ETag)}, nil
}

// CompleteMultipartUpload 完成分片上传。
func (s *OssStorage) CompleteMultipartUpload(ctx context.Context, session MultipartUploadSession, parts []UploadPartResponse, _ ...IOOption) error {
	if len(parts) == 0 {
		return errors.New("no parts to complete")
	}
	ossParts := make([]aliyunoss.UploadPart, 0, len(parts))
	for _, p := range parts {
		ossParts = append(ossParts, aliyunoss.UploadPart{PartNumber: int32(p.PartNumber), ETag: aliyunoss.Ptr(p.ETag)})
	}
	_, err := s.client.CompleteMultipartUpload(ctx, &aliyunoss.CompleteMultipartUploadRequest{
		Bucket:   aliyunoss.Ptr(s.bucket),
		Key:      aliyunoss.Ptr(session.Key),
		UploadId: aliyunoss.Ptr(session.UploadID),
		CompleteMultipartUpload: &aliyunoss.CompleteMultipartUpload{Parts: ossParts},
	})
	return err
}

// CancelMultipartUpload 取消分片上传。
func (s *OssStorage) CancelMultipartUpload(ctx context.Context, session MultipartUploadSession) error {
	_, err := s.client.AbortMultipartUpload(ctx, &aliyunoss.AbortMultipartUploadRequest{
		Bucket:   aliyunoss.Ptr(s.bucket),
		Key:      aliyunoss.Ptr(session.Key),
		UploadId: aliyunoss.Ptr(session.UploadID),
	})
	return err
}

// --- 辅助 ---

func isOSSNotFound(err error) bool {
	var se *aliyunoss.ServiceError
	if errors.As(err, &se) {
		return se.StatusCode == http.StatusNotFound || se.Code == "NoSuchKey" || se.Code == "NoSuchBucket"
	}
	return false
}

func objectURL(publicBaseURL, fallbackBaseURL, key string) string {
	base := strings.TrimSpace(publicBaseURL)
	if base == "" {
		base = fallbackBaseURL
	}
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(key, "/")
}

func normalizeEndpoint(endpoint string, disableSSL bool) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return strings.TrimRight(endpoint, "/")
	}
	scheme := "https://"
	if disableSSL {
		scheme = "http://"
	}
	return strings.TrimRight(scheme+endpoint, "/")
}

func ossDefaultURL(bucket, region, endpoint, key string, useCName bool) string {
	key = strings.TrimLeft(key, "/")
	if endpoint != "" {
		if useCName {
			return strings.TrimRight(endpoint, "/") + "/" + key
		}
		return joinEndpointPath(endpoint, bucket, key)
	}
	if bucket == "" || region == "" {
		return ""
	}
	return fmt.Sprintf("https://%s.oss-%s.aliyuncs.com/%s", bucket, region, key)
}

func joinEndpointPath(endpoint, bucket, key string) string {
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint == "" {
		return ""
	}
	// 虚拟主机风格：bucket.endpoint/key
	u := endpoint
	if !strings.Contains(endpoint, "://") {
		u = "https://" + endpoint
	}
	// 简化：直接拼 bucket 前缀
	host := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return "https://" + bucket + "." + host + "/" + strings.Trim(path.Join("/", key), "/")
}
```

> 说明：`aliyunoss.ToString` 为 v2 SDK 提供的指针解包辅助（`func ToString(p *string) string`）。`int32(partNumber)` 对应 `UploadPartRequest.PartNumber int32`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./packages/infra/storage/ -run TestOSS_ -v`
Expected: PASS。如 `TestOSS_PrefixIterator_Pagination` 断言与剥离逻辑不符，依 Step 1 注释把断言改为剥离后的值（`[]string{"1","2"}`）——以实现正确性为准。

- [ ] **Step 5: 接入工厂**

在 `packages/infra/storage/storage.go` 的 `NewStorage` switch 加入：
```go
	case "oss":
		return NewOSSStorage(cfg)
```
（位于 `case "disk"` 之后。）

- [ ] **Step 6: 全量编译 + vet**

Run: `go build ./packages/... && go vet ./packages/infra/storage/`
Expected: 通过。

- [ ] **Step 7: 提交**

```bash
git add packages/infra/storage/oss.go packages/infra/storage/oss_test.go packages/infra/storage/storage.go
git commit -m "feat(storage): OssStorage v2 SDK 重写（富接口 + mock 测试）"
```

---

## Task 5: 删除根 storage/、清理依赖、全量验证

**Files:**
- Delete: `storage/`（整目录：`storage.go`、`StorageDisk.go`、`storage_disk.go`、`storage_disk_test.go`、`storage_oss.go`）

**Interfaces:**
- Consumes: Task 1–4 全部产出。
- Produces: 干净的 `packages/infra/storage`；`go.mod` 去除 v1 OSS SDK 与 aws/minio/ftp 依赖；全绿构建。

- [ ] **Step 1: 删除根 storage/**

Run:
```bash
git rm -r storage/
```

- [ ] **Step 2: 清理依赖**

Run:
```bash
go mod tidy
```
Expected: `go.mod` 移除 `github.com/aliyun/aliyun-oss-go-sdk`（v1）、`aws-sdk-go-v2*`、`jlaffaye/ftp` 及其间接依赖；保留 `alibabacloud-oss-go-sdk-v2`、`google/uuid`、`golang.org/x/sync`、`stretchr/testify` 等。检查：
```bash
grep -E "aliyun-oss-go-sdk |aws-sdk-go-v2|jlaffaye/ftp" go.mod || echo "clean: 无 v1/aws/ftp 依赖"
```
Expected: 输出 `clean: ...`。

- [ ] **Step 3: 全量构建 / vet / 测试**

Run:
```bash
go build ./...
go vet ./...
go test ./packages/infra/storage/... ./packages/config/...
```
Expected: 全部通过，无 `luffa_micro_services` / `aliyun_oss` 残留报错。

- [ ] **Step 4: 残留引用扫描**

Run:
```bash
grep -rn "luffa_micro_services\|aliyun_oss\|holographic/packages" --include="*.go" . | grep -v "_test.go" || echo "no legacy refs"
```
Expected: `no legacy refs`（`packages/telemetry/gin.go` 的 tracer 字符串 `"holographic-server/gin"` 是非 import 的字面量，不在本次范围；如出现可忽略或单独处理）。

- [ ] **Step 5: 提交**

```bash
git add -A go.mod go.sum
git commit -m "chore(storage): 删除根 storage/，清理 v1/aws/ftp 依赖"
```

---

## Self-Review

**Spec coverage：**
- §5 接口 → Task 1 `storage.go`。✓
- §6.1 DiskStorage → Task 3（移植）。✓
- §6.2 OssStorage v2 → Task 4（含 mock 接口）。✓
- §6.3 S3/MinIO/FTP 移除 → Task 1 删除 + Task 5 依赖清理。✓
- §7 配置（disk/oss、BaseDir、DiskStorageConfig）→ Task 2。✓
- §8 工厂 `NewStorage` → Task 1 骨架 + Task 3/4 接入。✓
- §9 删除根 storage/ + 测试 → Task 3/4 测试 + Task 5 删除。✓
- §10 go.mod（已存在，去 v1/aws/ftp）→ Task 5 `go mod tidy`。✓
- §11 验收（build/vet/test 通过、无 legacy 引用、`var _ Storage` 断言）→ 各任务 + Task 5。✓

**Placeholder scan：** 无 TBD/TODO。DiskStorage 的"逐字保留"指向具体源文件 `storage/storage_disk.go`（在 Task 5 删除前可读），属明确的机械移植，非占位。

**Type consistency：** `Storage` 接口方法签名、`ObjectItem`/`UploadTask`/`MultipartUploadSession`/`UploadPartResponse`/`IOOptions`/`IOOption` 在 Task 1 定义，Task 3/4 一致引用；`config.Storage.Disk/.OSS`、`DiskStorageConfig`/`OSSStorageConfig` 在 Task 2 定义，Task 3/4 一致引用；`ossClient` 接口方法签名与 mock（Task 4 Step 1）逐方法对应。v2 SDK 字段名（`UploadId`/`ContentLength`/`PartNumber int32`/`DeletedObjects`/`NextContinuationToken`/`IsTruncated`/`ToString`）已据 SDK 源核实。
