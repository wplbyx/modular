---
name: requirement
description: "Use this skill whenever the user wants to turn vague product ideas, PRDs, feature lists, or rough backend requirements into executable protobuf interface contracts. It clarifies requirements, decomposes feature IDs, chooses svc/surface/RPC boundaries, and writes or updates modular-compatible proto/<svc>/*.proto files. Trigger this skill for requests like 整理需求, 写 PRD, 拆功能点, 拆接口, 接口设计, 从需求落到 API, proto, protobuf, gRPC, 后端接口契约, 后端功能模块接口, or 检查需求是否可实现, even if the user does not explicitly mention a skill."
---

# Requirement Skill

这个 skill 将模糊的产品想法、业务需求、PRD、功能清单或项目笔记，转成后端可实现的 Protobuf 接口契约。默认最终产物是 `.proto` 源文件，而不是 Markdown 需求文档。

## 输出目标

默认直接生成或更新 `proto/<svc>/*.proto` 文件。只有在无法确定项目路径、Go module、svc、surface 或接口边界时，才先在回复里给出 `.proto` 草案和必须确认的问题。

不要依赖 `document/` 目录或旧的需求文档模板。需求澄清、feature ID、权限、幂等、事务、并发和错误语义，都应服务于 `.proto` 接口契约。

## 先读取项目事实

开始写接口前，先用本地文件确认事实，不要凭空猜：

- 读取目标项目的 `go.mod`，获取 module path，用于 `option go_package`。
- 查看是否存在 `proto/`、`buf.yaml`、`buf.gen.yaml`、`common/`、`internal/`、`config/`。
- 如果已有 `proto/<svc>/*.proto`，先读现有文件，延续 package、go_package、service、message、enum、字段编号和注释风格。
- 如果项目使用 modular 约定，遵循 `proto/<svc>` 作为接口源目录，`common/<svc>` 作为生成目录的边界。
- 不要手写或修改 `common/**/*.pb.go`、`common/**/*_grpc.pb.go`，这些文件只允许由生成器产生。

## 核心流程

除非用户明确只要某一阶段，否则按这个顺序推进：

```text
模糊需求
-> 业务目标和边界
-> feature ID
-> svc / surface / RPC 划分
-> request / response / message / enum
-> proto 文件更新
-> 未决问题和后续实现提示
```

不要从一句模糊需求直接跳到字段设计。先弄清楚业务动作、操作者、状态变化和失败场景。

## 阶段 1：澄清业务目标

先捕获这些信息：

- 原始需求表述。
- 背景和业务目标。
- 目标用户、系统调用方或集成方。
- 主要业务流程。
- 异常流程和失败场景。
- 需求范围内和范围外的能力。
- 关键业务规则。
- 权限、数据归属、金额、库存、状态机、幂等、并发等高风险点。

如果信息不足，先记录为未决问题。不要把未确认的假设写成确定接口。

## 阶段 2：拆 feature ID

为每个可实现能力分配稳定 feature ID，再映射到 RPC。使用短模块前缀和数字编号：

```text
AUTH-001 用户登录
ORD-001 创建订单
ORD-002 取消订单
STK-001 调整库存
```

feature ID 用于连接业务需求、RPC 注释、错误场景、数据影响和后续测试。稳定 ID 比完美命名更重要。

每个 feature ID 至少要明确：

- 一句话业务动作。
- 调用方或操作者。
- 前置条件。
- 输入和输出。
- 成功规则。
- 失败场景。
- 数据影响。
- 权限、幂等、事务、并发要求。

如果一个 feature 仍需要实现者决定业务意图，它还没有拆到可执行粒度。

## 阶段 3：确定 svc、surface 和 RPC

把 feature 映射到 modular 的接口边界：

- `svc` 是业务模块名，使用小写 snake_case，例如 `user`、`order`、`inventory`。
- `surface` 是接口面，默认 `public`；管理端用 `admin`，平台集成用 `platform`，开放接口用 `openapi`，内部任务按实际语义命名。
- `public` surface 写入 `proto/<svc>/<svc>.proto`。
- 非 `public` surface 写入 `proto/<svc>/<surface>.proto`。
- `public` service 命名为 `<Svc>Service`，例如 `OrderService`。
- 非 `public` service 命名为 `<Svc><Surface>Service`，例如 `UserAdminService`。
- RPC 方法名使用 PascalCase，表达业务动作，例如 `CreateOrder`、`CancelOrder`、`ListDisabledUsers`。

同一个 svc 的多个 surface 共享 `common/<svc>` 生成包，因此 message 和 enum 名称不能互相冲突。优先使用 `<Method>Request` / `<Method>Response`，避免通用的 `Request`、`Response`、`Item`、`Data`。

## 阶段 4：设计 `.proto` 契约

生成或更新 proto 时使用纯 gRPC 契约，不添加 `google.api.http` 或 grpc-gateway 注解。HTTP 路径、权限、幂等、事务、并发和错误语义写在 RPC 或 message 注释里，后续由 `internal/<svc>/api/<surface>/http.go` 适配。

基础结构：

```proto
syntax = "proto3";

package order;

option go_package = "example.com/project/common/order";

service OrderService {
  // ORD-001 创建订单。
  // Caller: public frontend.
  // Auth: required.
  // Idempotency: required by idempotency_key.
  // Data effects: creates order and order items.
  // Transaction: order and inventory reservation must commit atomically.
  // Errors: INVALID_ARGUMENT when item list is empty; FAILED_PRECONDITION when stock is insufficient.
  rpc CreateOrder(CreateOrderRequest) returns (CreateOrderResponse);
}
```

字段规则：

- 字段名使用 `snake_case`。
- 字段编号稳定递增；不要重排、复用或随意删除已发布字段编号。
- 新增字段追加新的编号。
- 如果需要废弃字段，保留编号并使用 `reserved`。
- `string id` 用于外部 ID；不要把数据库自增 ID 泄漏成接口契约，除非需求明确要求。
- 时间字段优先使用 `google.protobuf.Timestamp`；如项目没有使用 well-known types 且用户希望避免 import，可用 `int64 unix_time`，但要在注释中说明单位。
- 金额字段优先使用最小货币单位的整数，例如 `int64 amount_cent`；不要用 `float` 表示金额。
- 分页请求使用 `page_size` 和 `page_token`，或沿用现有项目分页风格。
- 列表响应包含 `repeated Xxx items` 和 `next_page_token`，或沿用现有项目风格。
- bool 字段命名表达真实业务语义，例如 `is_disabled`、`allow_backorder`。
- 不要使用含糊字段名：`data`、`info`、`payload`、`status`，除非它们在上下文中有清晰枚举或消息类型。

enum 规则：

- enum 名称使用 PascalCase，值使用大写 snake_case。
- 第一个值必须是 `*_UNSPECIFIED = 0`。
- 状态机 enum 要在注释中说明可见状态，不要把状态流转规则藏在实现里。

## 阶段 5：写入或更新文件

写文件时保持变更聚焦：

- 如果目标 `.proto` 不存在，创建完整文件：`syntax`、`package`、`go_package`、`service`、RPC、message、enum。
- 如果目标 `.proto` 已存在，只追加或调整相关 RPC、message、enum，不重排无关内容。
- 删除脚手架占位的 `Example` RPC、`ExampleRequest`、`ExampleResponse`，前提是它们没有被真实接口引用。
- 不要改动生成目录 `common/`。
- 不要为了需求接口生成去改 `cmd/`、`internal/`、`config/`，除非用户明确要求继续实现。
- 如果项目有 `buf.yaml`，在最终回复中建议后续运行 `buf generate`；不要在用户只要求需求或 proto 时擅自生成 Go 代码。

## 注释内容

每个 RPC 注释尽量包含这些实现者真正需要的信息：

- feature ID 和一句话业务动作。
- Caller：frontend、admin、system task、integration 或其他明确调用方。
- Auth：是否需要登录、角色、数据归属校验。
- Idempotency：是否需要幂等键、天然幂等还是不需要。
- Data effects：会创建、更新、删除或读取哪些业务对象。
- Transaction：写操作的事务边界。
- Concurrency：库存、余额、名额、状态流转等并发保护。
- Errors：业务错误码或 gRPC status 语义和触发条件。

保持注释是接口契约的一部分，不要写实现代码、SQL、UI 文案或页面布局。

## 高风险需求处理

遇到这些需求时，必须先澄清或在注释中明确约束：

- 支付、退款、余额、积分、优惠券、库存、秒杀、订单取消。
- 账号权限、角色管理、数据归属、管理员操作。
- 状态机流转，例如订单、工单、审核、发布、冻结。
- 幂等写操作，例如创建订单、提交支付、领取权益。
- 并发竞争，例如库存扣减、名额占用、重复提交。
- 跨服务一致性或事件投递。

如果关键规则不明确，不要写成确定字段；在回复中列出需要产品或技术负责人确认的问题，并给出可继续推进的最小 proto 草案。

## 默认回复结构

完成一次需求到 proto 的工作后，按这个顺序回复：

1. 当前阶段和已更新的 `.proto` 文件。
2. 新增或调整的 service / RPC / message / enum。
3. 已写入 proto 注释的关键业务约束。
4. 未决问题。
5. 后续建议，例如运行 `buf generate` 或继续补 HTTP adapter。

如果只是评审需求或接口草案，按这个顺序回复：

1. 当前可确定的业务动作。
2. 可以落到 proto 的 RPC。
3. 缺失或高风险信息。
4. 建议的 svc / surface / 文件路径。
5. 需要用户确认的问题。

## 避免的问题

避免这些失败模式：

- 继续产出旧的 Markdown PRD，而不是 `.proto`。
- 在未澄清业务动作前直接设计字段。
- 把 HTTP 注解写进 proto，导致当前 modular 脚手架无法自然匹配。
- 把数据库表结构、索引、SQL 或 ORM tag 写进 proto。
- 把 UI 布局、颜色、组件或交互文案写进后端接口契约。
- 手写生成文件。
- 重排已有字段编号或复用已删除字段编号。
- 用隐藏假设填补权限、幂等、事务、并发等关键规则。
