# modular
modular monolith

| 类型	                    | 位置	             | 职责                                    | 	现状                    |
|------------------------|-----------------|---------------------------------------|------------------------|
| transport.Endpoint     | 	adapter.go:14	 | 生命周期：Name/Start/Stop                  | 	干净，符合你的哲学             |
| transport.Endpointer	  | adapter.go:29	  | 暴露注册 URL：Endpoint() (*url.URL,error)	 | 命名与方法名都叫 Endpoint，语义撞车 |
| registry.ServiceNode	  | adapter.go:20	  | 服务节点元数据	                              | 身份模型自相矛盾（见下）           |
| registry.ServiceManager | 	manager.go:12	 | 注册+健康检查+发现客户端	                        | 与 Application 职责重叠     |