package registry

// parseEndpoint 已删除：ServiceNode 现在直接携带结构化的 Transport 列表。
// consul.go 的 Register 遍历 node.Transports，每个 Transport 注册为一条 Consul 记录。
