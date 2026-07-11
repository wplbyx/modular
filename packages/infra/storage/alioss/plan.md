# AliOSS 直传预签名接口

Summary

- 新增独立直传接口，不改 storage.Storage，不影响 disk/oss 现有调用方。
- 第一版采用 OSS SDK v2 Client.Presign 生成预签名 PUT/GET URL；不引入 STS 依赖。
- 支持单文件直传 PUT、直连下载 GET、分片直传 initiate/part/complete/abort。
- 在 alioss 包内提供 OSS 业务回调 HTTP handler 的核心逻辑，业务服务按需挂载路由并注入处理函数。

Public API

- 在 packages/infra/storage/storage.go 新增：
    - DirectStorage：与 Storage 同层级的可选接口。
    - DirectTransferRequest：返回 Key、Method、URL、Headers、Body、ExpiresAt、PublicURL。
    - DirectUploadOptions：Expires、ContentType、ContentMD5、Meta、ForbidOverwrite、可选 OSS Callback / CallbackVar。
    - DirectDownloadOptions：Expires。
    - DirectMultipartInitiateOptions / DirectMultipartPartOptions / DirectMultipartCompleteOptions / DirectMultipartAbortOptions。

- DirectStorage 方法：
    - PresignUpload(ctx, key, DirectUploadOptions)
    - PresignDownload(ctx, key, DirectDownloadOptions)
    - PresignMultipartInitiate(ctx, key, DirectMultipartInitiateOptions)
    - PresignMultipartUploadPart(ctx, key, uploadID, partNumber, DirectMultipartPartOptions)
    - PresignMultipartComplete(ctx, key, uploadID, parts, DirectMultipartCompleteOptions)
    - PresignMultipartAbort(ctx, key, uploadID, DirectMultipartAbortOptions)

- Expires 默认 15 分钟；超过 7 天直接报错，避免 SDK v4 签名限制在运行时才暴露。
- 不加入 ContentLength 作为安全约束；浏览器不能可靠设置该请求头，预签名 PUT 也不等价于 POST policy 的 content-length-range。

Implementation Changes

- 在 packages/infra/storage/alioss/oss.go 让 OssStorage 实现 storage.DirectStorage。
- PresignUpload 构造 oss.PutObjectRequest，绑定 bucket、baseDir 后的 object key、Content-Type、Content-MD5、metadata、x-oss-forbid-overwrite、可选 callback headers，然后返回 SDK 的预签名结果。

- PresignDownload 构造 oss.GetObjectRequest，用于私有 bucket/CDN 场景下的限时直连下载；现有 GetUrl 保持不变。
- PresignMultipartInitiate 构造 oss.InitiateMultipartUploadRequest，返回用于获取 uploadId 的 POST 预签名请求。
- PresignMultipartUploadPart 构造 oss.UploadPartRequest，返回单个 part 的 PUT 预签名请求，可绑定 Content-MD5。
- PresignMultipartComplete 构造 oss.CompleteMultipartUploadRequest，按 PartNumber 排序并返回完成分片所需 XML Body，可携带 Callback / CallbackVar。
- PresignMultipartAbort 构造 oss.AbortMultipartUploadRequest，返回取消分片的 DELETE 预签名请求。
- 返回给前端的 Headers 必须原样用于 PUT 请求，尤其是 Content-Type、Content-MD5、x-oss-* 这类参与签名的 header。
- 不新增配置项；AccessKeyID/AccessKeySecret/SecurityToken 仍只存在服务端配置中，绝不返回给前端。

Callback Handler

- 在 packages/infra/storage/alioss/callback.go 新增：
    - NewCallbackHandler(processor, opts...)：返回标准库 http.Handler，业务服务负责挂路由。
    - VerifyCallbackSignature：校验 Authorization、x-oss-pub-key-url、OSS 回调签名。
    - ParseCallbackPayload：解析 JSON 或 application/x-www-form-urlencoded 回调 body。
    - WithCallbackPublicKeyFetcher：允许测试或业务侧替换公钥加载逻辑。
- Handler 只实现通用核心逻辑：方法校验、读取 body、验签、解析 payload、调用业务 processor、返回 OSS 期望的 {"Status":"OK"}。

Test Plan

- 新增/扩展 packages/infra/storage/alioss/oss_test.go：
    - PresignUpload 返回 PUT、URL 包含 bucket/key/baseDir、query 签名参数存在、不包含 AccessKeySecret。
    - PresignUpload 带 ContentType、ContentMD5、Meta、ForbidOverwrite 时返回对应 signed headers。
    - PresignDownload 返回 GET，可用于同一 object key 的限时下载。
    - PresignMultipartInitiate / UploadPart / Complete / Abort 返回正确 method、query、headers 和 complete body。
    - 空 key、超过 7 天的过期时间返回明确错误。
- 新增 packages/infra/storage/alioss/callback_test.go：
    - 合法 OSS 回调签名可通过验签。
    - body 被篡改、public key URL 非阿里云允许域名时拒绝。
    - URL 解码 path + 原始 query 的签名串行为符合 OSS 规则。
    - JSON / 表单回调 payload 可解析。
    - HTTP handler 可验签、解析、调用 processor 并返回 {"Status":"OK"}。

- 运行 go test ./packages/infra/storage/...，必要时再运行 go test ./...。

Assumptions

- 第一版不实现 STS 临时凭证。
- OSS bucket 的 CORS 需要由部署方配置，至少允许前端 origin、PUT/GET/HEAD、以及 Content-Type、Content-MD5、x-oss-* headers。
- 业务服务负责鉴权、生成安全 object key、校验文件类型/大小意图，并在上传完成后用回调或 HeadObject 做最终确认。
