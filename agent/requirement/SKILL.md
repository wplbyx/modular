---
name: requirement
description: "Turn vague product ideas, PRDs, feature lists, or rough backend requirements into implementable modular-monolith Business Module contracts and use-case briefs. Use whenever the user asks to clarify requirements, split features, design backend interfaces, define module boundaries, or turn requirements into Go code, even when they do not explicitly name this skill."
---

# Requirement Skill

把模糊的产品想法、业务需求、PRD 或功能清单，收敛成可实现的
Business Module 边界、业务动作和手写 Go contract。默认产物是业务代码与
必要的简短决策记录，不是 proto、Buf 配置或传输 DTO。

## 输出目标

优先直接创建或更新：

- `internal/modules/<module>/contract` 中的公开 Go 接口；
- Command、Query、Result 和必要的公开值类型；
- 与真实复杂度匹配的 app/domain 实现骨架；
- `.modular/architecture.yaml` 中明确且无环的模块依赖。

不要创建 `proto/`、`common/`、Buf 配置或生成式 Module Port。HTTP、
gRPC 和消息协议是 Application 边缘的外部契约，由 adapter 显式映射到
模块的 Go 类型；只有用户单独要求外部协议设计时才处理这些传输资产。

## 先读取项目事实

开始设计前先确认：

- 读取 `.modular/architecture.yaml`，确认 Application、已有模块和依赖；
- 读取目标模块的 `contract`、`app`、`domain` 和 wiring；
- 读取消费方实际需要的能力，不为假想复用扩大接口；
- 确认共享 DB、事务接口、EventBus 或外部 client 的注入方式；
- 若模块尚未声明，使用 modular CLI 添加，而不是手改受管 framework 文件。

## 核心流程

```text
模糊需求
-> 业务目标、操作者与成功条件
-> feature ID 和失败场景
-> 限界上下文与模块依赖
-> Command / Query / Result
-> 窄 Go contract
-> 用例、事务与并发规则
-> wiring 和实现
```

不要从一句模糊需求直接跳到字段或表结构。先确定业务动作、状态变化、
数据所有者和失败语义；无法确认的内容保留为未决问题。

## 需求收敛

每个 feature 使用稳定的短 ID，例如：

```text
AUTH-001 用户登录
ORD-001 创建订单
ORD-002 取消订单
STK-001 调整库存
```

每个 feature 至少明确：

- 操作者与授权规则；
- 前置条件、输入、输出和成功条件；
- 业务失败原因；
- 数据写入所有者；
- 幂等、事务和并发要求；
- 是否调用其他模块，以及依赖方向。

幂等、重试和补偿只在业务或外部系统交互确实需要时引入。不要因为代码被
划分为模块，就模拟网络故障或提前实现分布式事务。

## 模块与依赖

Business Module 默认对应一个限界上下文，拥有自己的业务规则、用例、
公开 contract 和数据写入。名称使用 lower_snake_case，例如 `order`、
`inventory`。

跨模块同步调用遵循：

- consumer 只导入 provider 的 `internal/modules/<provider>/contract`；
- contract 由 provider 定义，但以调用方真实需求保持窄接口；
- consumer 不导入 provider 的 `internal`、repository、ORM model 或表；
- `.modular/architecture.yaml` 中声明依赖，整个图必须保持 DAG；
- 不为了消除依赖环创建一个装满 DTO 的 shared/common 业务包。

出现依赖环时，重新检查业务所有权、合并错误切分的上下文，或把跨模块流程
提升到明确的发起方；不要用事件或运行时容器隐藏同步环。

## Go contract 设计

公开接口使用 `context.Context` 和业务语义命名：

```go
package contract

import "context"

type PlaceOrderCommand struct {
    CustomerID string
    Items      []OrderItem
}

type PlaceOrderResult struct {
    OrderID string
}

type Service interface {
    PlaceOrder(context.Context, PlaceOrderCommand) (PlaceOrderResult, error)
}
```

约束：

- 写操作使用 `Command`，读操作使用 `Query`，返回值使用 `Result`；
- 类型表达业务语义，不复用 HTTP/gRPC request/response；
- 不暴露 ORM model、数据库事务句柄或基础设施 client；
- 接口按调用能力拆分，避免一个包含所有方法的宽 Service；
- stable reason 定义在拥有该业务规则的模块；
- 输入校验分清格式校验与业务不变量，后者留在 app/domain；
- 简单 CRUD 可留在 app；只有聚合、不变量或策略真实存在时才创建 domain。

## 事务与一致性

单体允许跨模块 ACID 事务，但边界必须显式：

- 跨模块工作流归发起方模块所有；
- 发起方只调用其他模块的公开 contract；
- 在具体用例旁定义最小事务接口，由项目 adapter 实现；
- 不在 modular core 增加通用 UoW，也不把 repository 暴露给其他模块；
- 本地 EventBus 只表达进程内通知，不假装可靠的跨系统消息。

与外部系统交互时，再按真实失败模型决定 timeout、重试、幂等键、Outbox/
Inbox、补偿和对账。不要把这些成本施加给纯进程内模块调用。

## Application 边缘

入站 adapter 负责认证、传输格式校验和 DTO 映射，然后调用模块 contract。
出站 adapter 把外部 client、存储或消息系统映射到模块需要的窄端口。

外部 HTTP/gRPC/消息契约可以独立版本化，但不能替代模块 Go contract，也
不能被其他模块当作本地调用接口。项目明确采用 gRPC 时，其标准 protobuf
文件和生成流程由项目自行管理，modular scaffolder 不参与。

## 实现顺序

用户要求实现时，按以下顺序推进：

1. 确认或添加模块及 DAG 依赖；
2. 写 provider 的公开 contract 与稳定错误；
3. 写发起方用例和必要 domain 规则；
4. 写 repository/external port 及 adapter；
5. 在 `WireApplication(platform)` 中按依赖顺序装配；
6. 最后映射 HTTP/gRPC/event 边缘。

不要修改 `framework.gen.go` 等 managed 文件。业务 wiring、模块代码和
project-defined transaction adapter 属于用户维护范围。

## 默认回复结构

完成实现后说明：

1. 已落地的模块、contract 和用例；
2. 关键业务规则、事务与依赖方向；
3. 仍未确定的业务问题；
4. 实际执行的验证。

仅做需求评审时，说明可确定的业务动作、建议模块边界、高风险缺口和必须由
产品或技术负责人确认的问题。

## 避免的问题

- 把模块契约设计成 proto 或传输 DTO；
- 为将来可能微服务化提前加入 Remote Adapter、幂等、重试或柔性事务；
- 用数据库表或 ORM model 定义模块边界；
- 让模块通过全局容器、裸 DB 或 repository 相互调用；
- 把未知的权限、金额、库存和状态机规则当作确定事实；
- 为追求固定目录结构创建没有业务价值的空 domain/repository 包。
