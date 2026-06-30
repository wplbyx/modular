# Storage 融合设计

- 日期：2026-06-30
- 范围：把根目录遗留的 `storage/` 融合进 `packages/infra/storage/`
- 关联后续工作：为 `packages/` 全部代码在 `agent/` 下生成 skills（独立 spec，待本 spec 落地后启动）

## 1. 背景与问题

仓库根目录的 `storage/` 是从外部项目 `luffa_micro_services` 拷贝而来的遗留代码：

- 包名 `aliyun_oss`；logger 引用 `luffa_micro_services/pkg/logger`；OSS 配置引用 `luffa_micro_services/app/pkg/options`。
- 文件：`storage.go`（单个富 `Storage` 接口）、`StorageDisk.go`（`LocalDiskStorage` 配置 + pflag）、`storage_disk.go`（`DiskStorage`，纯标准库，407 行）、`storage_oss.go`（`OssStorage`，v1 SDK，355 行）、`storage_disk_test.go`（测试，646 行）。
- **接口最全**：单个 `Storage` 接口含 `Stat`、批量上传/删除、`DeleteByPrefix`、`PrefixIterator` 流式遍历、完整 Multipart 分片上传套件、functional options（`WithQuiet/WithContentType/WithVersionID/WithConcurrency/WithMeta`）。

`packages/infra/storage/` 是本项目（`modular`）原生实现，但接口较简单：

- `Upload` 返回带 hash/content-type 的 `*Object`；有 `GetURL`、`FileStorage`(List/Move/Copy)；后端 local/oss/s3/minio/ftp；与 `config.Storage` + `NewStorage(cfg)` 集成。
- 只有基础 CRUD，缺少批量/分片/前缀迭代等能力。

两套 OSS 实现用不同代 SDK：遗留 `storage_oss.go` 为 v1（`aliyun-oss-go-sdk/oss`），现有 `packages/infra/storage/oss.go` 为 v2（`alibabacloud-oss-go-sdk-v2/oss`）。

仓库已建立 `go.mod`（`module modular`，go 1.26.0，由用户手动创建），import 路径为 `modular/packages/...`。

## 2. 目标

1. 以**遗留 `storage/` 的单个富 `Storage` 接口为唯一标准**，落地到 `packages/infra/storage/`（包名 `storage`）。
2. OSS 后端**整套用 v2 SDK 重写**（不引入 v1 SDK）。
3. **只实现两个后端：`disk` 与 `oss`**。
4. 配置沿用本项目 `config` 包模式，参数项取两者并集。
5. 在用户已创建的 `go.mod`（`module modular`）基础上，保证模块可编译、可测试。
6. 融合完成后删除根 `storage/`，消除重复与外部项目引用。

## 3. 非目标

- 不保留/实现 s3 / minio / ftp 后端（从 `packages/infra/storage` 移除）。
- 不保留现有 `*Object` 元数据返回、`FileStorage`、`GetURL`（被遗留接口取代）。
- 不重构与本目标无关的包。
- 不在本 spec 内生成 skills（独立 spec）。
- 不改变 `config` 包的加载机制（`Configurer` / Loader）。

## 4. 关键决策

| 决策点 | 选择 | 理由 |
|---|---|---|
| 接口标准 | 采用遗留的单个富 `Storage` 接口 | 用户指定；接口最全，统一无拆分 |
| OSS SDK | v2 重写（不保留 v1） | 用户指定；避免同包混入两代 SDK |
| 后端范围 | 仅 `disk` + `oss` | 用户指定；二者即遗留现有实现，s3/minio/ftp 移除 |
| 包位置/名 | `packages/infra/storage/`，包名 `storage` | 沿用本项目结构 |
| 配置 | 沿用 `config` 包（mapstructure + `config.Storage`） | 本项目既有模式；参数取并集（见 §7 待确认项） |

## 5. 接口设计（`packages/infra/storage/storage.go`）

采用遗留 `storage/storage.go` 的接口，包名改为 `storage`：

```go
type Storage interface {
    // 路径与元信息
    GetUsefulUrl(key string) string
    Stat(ctx context.Context, key string) (ObjectItem, error)

    // 基础单文件 CRUD（functional options）
    Exists(ctx context.Context, key string) (bool, error)
    Upload(ctx context.Context, key string, body io.Reader, opts ...IOOption) error
    Delete(ctx context.Context, key string, opts ...IOOption) error
    Download(ctx context.Context, key string, opts ...IOOption) (io.ReadCloser, error)

    // 批量与高级
    BatchUpload(ctx context.Context, tasks []UploadTask, opts ...IOOption) error
    BatchDelete(ctx context.Context, keys []string, opts ...IOOption) ([]string, error)
    DeleteByPrefix(ctx context.Context, prefix string, opts ...IOOption) error
    PrefixIterator(ctx context.Context, prefix string, callback ListCallback) error

    // 大文件分片上传
    InitiateMultipartUpload(ctx context.Context, key string) (MultipartUploadSession, error)
    CompleteMultipartUpload(ctx context.Context, session MultipartUploadSession, parts []UploadPartResponse, opts ...IOOption) error
    CancelMultipartUpload(ctx context.Context, session MultipartUploadSession) error
    MultipartUpload(ctx context.Context, session MultipartUploadSession, partNumber int, partSize int64, body io.Reader) (UploadPartResponse, error)
}
```

迁入的类型与 functional options（包名 `storage`）：

- `ObjectItem{ Key; Size; LastModified }`
- `UploadTask{ Key; Body io.Reader }`
- `ListCallback func(ctx, items ...ObjectItem) error`
- `MultipartUploadSession{ UploadID; Key }`
- `UploadPartResponse{ PartNumber; ETag }`
- `IOOptions{ Quiet; ContentType; VersionID; ConcurrentNum; Meta }` + `IOOption` + `applyIOOptions`
- `WithQuiet / WithContentType / WithVersionID / WithConcurrency / WithMeta`

> 说明：保留遗留方法名 `GetUsefulUrl`（忠于"采用遗留接口"）。`Upload` 返回 `error`，不再返回 `*Object`。

## 6. 后端实现

### 6.1 DiskStorage（标准库移植）

把遗留 `storage_disk.go` 的 `DiskStorage` 整体迁入 `packages/infra/storage/`（如 `disk.go`）：

- 纯标准库 + `github.com/google/uuid` + `golang.org/x/sync/errgroup`，无云 SDK 依赖，移植风险最低。
- 全部富方法照搬：`Stat`、`BatchUpload`（errgroup 并发，默认 5，`errors.Join` 聚合）、`BatchDelete`（幂等）、`DeleteByPrefix`（PrefixIterator + 每 1000 条）、`PrefixIterator`（`filepath.WalkDir`，每 1000 条回调）、Multipart 套件（临时目录在 `os.TempDir()`，分片 MD5 作 ETag，完成时按 PartNumber 升序合并并清理）。
- 替换 `luffa_micro_services/pkg/logger` → `modular/packages/log`（或移除非必要日志）。
- 构造改为从 `config.Storage` 读取（见 §7）：`RootDir`、`BaseUrl`。
- 单测：移植 `storage_disk_test.go`（646 行，适配新包名/类型）。

### 6.2 OssStorage（v1 → v2 重写）

以遗留 `storage_oss.go` 的方法语义为蓝本，用 v2 SDK（`alibabacloud-oss-go-sdk-v2/oss`）重写到 `packages/infra/storage/oss.go`：

- 基础：`PutObject / GetObject / DeleteObject / HeadObject`（v2，与现有 oss.go 一致的客户端构造方式）。
- `Stat`：`HeadObject` 解析 `Content-Length` / `Last-Modified`。
- `BatchUpload`：errgroup 并发调 `Upload`。
- `BatchDelete`：v2 批量删除（按 1000 分批），区分 `Quiet`/详细模式，剥除 `BaseDir` 前缀还原相对 key。
- `PrefixIterator`：v2 列举分页器（ContinuationToken），回调前剥前缀。
- `DeleteByPrefix`：`PrefixIterator` + 每 1000 条 `BatchDelete`。
- Multipart 套件：v2 的 `CreateMultipartUpload / UploadPart / CompleteMultipartUpload / AbortMultipartUpload`。
- `opts` 接入：`WithContentType`→PutObject content-type；`WithMeta`→用户元数据；`WithConcurrency`→BatchUpload 并发；`WithVersionID`→版本控制对象（v2 支持时）。
- 构造从 `config.Storage` 读取（见 §7）。
- 测试：为 OSS 新增单测；如需 mock，定义覆盖所需 v2 方法的接口（不沿用旧 4 方法 `ossObjectClient`）。

> 实现风险：v2 的分页器、批量删除、multipart 确切签名需编码时对照官方 API 核实。

## 7. 配置（`packages/config/config_storage.go`）— ⚠️ 待确认

沿用本项目 `config` 包模式（mapstructure、`config.Storage` 聚合、`Configurer` 体系）。收敛为两后端：

- `Storage.Type` 校验改为 `oneof=disk oss`。
- `LocalStorageConfig` 重命名为 `DiskStorageConfig`，只保留磁盘所需：`RootDir`、`BaseUrl`（对外访问域名，用于 `GetUsefulUrl`）；移除不再使用的 `URLPath`/`Perm`/`CleanupTimeout`。
- `OSSStorageConfig`：保留现有字段（AccessKeyID/AccessKeySecret/SecurityToken/Region/Bucket/Endpoint/DisableSSL/UseCName/Timeout/MaxRetries），**新增 `BaseDir`**（对象 key 前缀，来自遗留）。
- 移除 `S3StorageConfig`、`MinioStorageConfig`（后端已删）。

> **待确认**：用户曾指示"配置模式按老的来"。本设计理解为"沿用 modular 现有 `config` 包模式"。若实际指遗留 `storage/` 自带的 pflag 配置风格（`LocalDiskStorage` 内置于 storage 包、`AddFlags`/`Validate() []error`），则改为 storage 包内置配置——请确认。

## 8. 工厂

`packages/infra/storage/storage.go` 提供：

```go
func NewStorage(cfg *config.Storage) (Storage, error)
```

按 `cfg.Type` 分发：`disk` → `*DiskStorage`，`oss` → `*OssStorage`，其余返回 unsupported 错误。

遗留的全局单例 `InitDiskStorage/GetDiskStorage/InitOSSStorage/GetOSSStorage` 是否保留待确认（默认不保留，由调用方自行持有 `Storage` 实例）。

## 9. 清理与测试

- 融合完成、构建与测试通过后**删除根 `storage/` 目录**。
- `packages/infra/storage/` 内移除：现有 `LocalStorage`、`OSSStorage`(旧 v2 简单版)、`S3Storage`、`ftp/client.go`、`*Object`、`FileStorage`、`GetURL`、`object_helpers.go` 中仅为 `*Object` 服务的部分（`trackedUploadReader`/`contentSniffer`）；保留对 v2 OSS 仍有用的工具（如 `normalizeEndpoint`，按需）。
- 测试目标：
  - 移植 `storage_disk_test.go` → `DiskStorage` 全量富接口测试。
  - `OssStorage` v2 新增测试（BatchDelete Quiet/详细、PrefixIterator 分页、Multipart 全流程）。
- 编译期断言：`var _ Storage = (*DiskStorage)(nil)`、`var _ Storage = (*OssStorage)(nil)`。

## 10. 构建与依赖（`go.mod`）

- `go.mod` 已由用户创建：`module modular`，go 1.26.0。
- 当前 `go.mod` 仍含 v1 `aliyun-oss-go-sdk`、`aws-sdk-go-v2`、`jlaffaye/ftp` 等——它们仅服务于待删的遗留 `storage/` 与现有 s3/ftp 后端。
- 目标态：删除上述代码并执行 `go mod tidy` 后，仅保留 v2 `alibabacloud-oss-go-sdk-v2`、`google/uuid`、`golang.org/x/sync`、`spf13/pflag`、`spf13/viper`、validator 等；**不引入** v1 OSS SDK，**移除** aws/minio/ftp 依赖。

## 11. 验收标准

- 根目录 `go build ./...` 与 `go vet ./...` 通过。
- `go test ./packages/infra/storage/...` 通过。
- 根 `storage/` 已删除；仓库内不再出现 `luffa_micro_services` 或 `aliyun_oss` 引用。
- `packages/infra/storage` 仅含 `Storage` 富接口 + `DiskStorage` + `OssStorage`；二者通过 `var _ Storage` 编译期断言。
- `config.Storage.Type` 校验为 `oneof=disk oss`；`OSSStorageConfig` 含 `BaseDir`；s3/minio 配置已移除。
- `NewStorage(cfg)` 对 `disk`/`oss` 可用。
- `go.mod` 已存在（`module modular`）；`go build ./...` 通过。

## 12. 风险

- v2 SDK 的列举分页器、批量删除、multipart 签名需实现阶段核实（以官方文档为准，必要时单独加单测）。
- 配置模式（§7）需用户确认；若改用 storage 包内置配置，会影响 §7/§8 的构造签名。
- 首次补 `go.mod` 时 `go mod tidy` 可能暴露版本冲突，需以全仓可编译为前置门槛。
- 移除 s3/minio/ftp 为破坏性删除，确认无其他包引用后再删（实现阶段 grep 确认）。

## 13. 实施顺序（概览）

1. `go.mod` 已由用户创建（`module modular`）；先确认 `go build ./...` 当前基线（前置）。
2. 在 `packages/infra/storage/storage.go` 落地富 `Storage` 接口 + 迁入类型/options。
3. `DiskStorage` 移植 + 移植单测。
4. `OssStorage` v2 重写 + 新增单测。
5. `config_storage.go` 收敛为 disk/oss + `BaseDir`；`NewStorage` 工厂。
6. 移除旧实现（LocalStorage/S3/MinIO/ftp/*Object/FileStorage/旧 helpers）。
7. 删除根 `storage/`；全量 `go build`/`go vet`/`go test` 验证。
